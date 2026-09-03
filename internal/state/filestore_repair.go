package state

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// QuarantineName is the sibling file that receives malformed or torn lines
// removed from an events log ("events.jsonl" → "events.quarantine.jsonl").
func QuarantineName(eventsPath string) string {
	dir := filepath.Dir(eventsPath)
	base := filepath.Base(eventsPath)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	return filepath.Join(dir, stem+".quarantine"+ext)
}

// hasTornTail reports whether the file is non-empty and does not end in a
// newline — the signature of a writer that died mid-line. The file offset is
// restored to 0 afterwards.
func hasTornTail(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil || fi.Size() == 0 {
		return false
	}
	var last [1]byte
	if _, err := f.ReadAt(last[:], fi.Size()-1); err != nil {
		return false
	}
	return last[0] != '\n'
}

// tornFragment returns the byte offset of the start of the torn final line
// and the fragment itself. offset == size when the tail is intact.
func tornFragment(path string) (offset int64, fragment []byte, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return 0, nil, err
	}
	size := fi.Size()
	if size == 0 || !hasTornTail(f) {
		return size, nil, nil
	}
	// Walk backwards in chunks to find the last newline.
	const chunk = 64 << 10
	end := size
	for end > 0 {
		start := end - chunk
		if start < 0 {
			start = 0
		}
		buf := make([]byte, end-start)
		if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
			return 0, nil, err
		}
		if i := bytes.LastIndexByte(buf, '\n'); i >= 0 {
			offset = start + int64(i) + 1
			break
		}
		end = start
	}
	fragment = make([]byte, size-offset)
	if _, err := f.ReadAt(fragment, offset); err != nil && err != io.EOF {
		return 0, nil, err
	}
	return offset, fragment, nil
}

// repairTornTailLocked moves a torn final fragment into the quarantine file
// and truncates the log back to its last complete line so the next append
// cannot glue onto the fragment. Caller holds fs.mu.
func (fs *FileStore) repairTornTailLocked() error {
	offset, fragment, err := tornFragment(fs.path)
	if err != nil {
		return err
	}
	if len(fragment) == 0 {
		return nil
	}
	if err := appendQuarantine(fs.path, [][]byte{fragment}); err != nil {
		return err
	}
	if err := os.Truncate(fs.path, offset); err != nil {
		return fmt.Errorf("truncate torn tail: %w", err)
	}
	log.Printf("[state] quarantined torn write (%d bytes) from %s → %s",
		len(fragment), fs.path, QuarantineName(fs.path))
	return nil
}

// appendQuarantine appends each raw line (newline-terminated) to the
// quarantine file next to eventsPath.
func appendQuarantine(eventsPath string, lines [][]byte) error {
	q, err := os.OpenFile(QuarantineName(eventsPath), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open quarantine: %w", err)
	}
	defer q.Close()
	for _, l := range lines {
		l = bytes.TrimRight(l, "\n")
		if _, err := q.Write(append(l, '\n')); err != nil {
			return fmt.Errorf("write quarantine: %w", err)
		}
	}
	return q.Sync()
}

// quarantineLinesLocked moves the given malformed lines out of the live log
// (lenient mode). The log is rewritten atomically via temp file + rename and
// the append handle is reopened on the new inode. Caller holds fs.mu (write).
func (fs *FileStore) quarantineLinesLocked(bad []badLine) error {
	drop := make(map[int]struct{}, len(bad))
	raws := make([][]byte, 0, len(bad))
	for _, b := range bad {
		drop[b.lineNo] = struct{}{}
		raws = append(raws, b.raw)
	}
	// Quarantine a torn tail first so the rewrite below never has to decide
	// what to do with a newline-less fragment.
	if err := fs.repairTornTailLocked(); err != nil {
		return err
	}
	fs.tailChecked = true
	if err := appendQuarantine(fs.path, raws); err != nil {
		return err
	}
	if _, err := rewriteWithout(fs.path, drop, false); err != nil {
		return err
	}
	if err := fs.reopenLocked(); err != nil {
		return err
	}
	log.Printf("[state] %s: quarantined %d malformed line(s) → %s",
		eventsLenientEnv, len(bad), QuarantineName(fs.path))
	return nil
}

// reopenLocked replaces the append handle after the log was rewritten under
// a new inode. Caller holds fs.mu (write).
func (fs *FileStore) reopenLocked() error {
	f, err := os.OpenFile(fs.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("reopen events log: %w", err)
	}
	_ = fs.file.Close()
	fs.file = f
	return nil
}

// rewriteWithout rewrites path omitting the given 1-based line numbers,
// atomically (temp file + fsync + rename). When keepBackup is set a
// "<path>.bak" copy of the original is left behind. Returns the number of
// lines written.
func rewriteWithout(path string, drop map[int]struct{}, keepBackup bool) (int, error) {
	src, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer src.Close()

	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return 0, fmt.Errorf("create temp log: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = tmp.Close(); _ = os.Remove(tmpPath) }

	w := bufio.NewWriterSize(tmp, scannerInitialBuf)
	scanner := bufio.NewScanner(src)
	scanner.Buffer(make([]byte, scannerInitialBuf), scannerMaxLine)
	kept, lineNo := 0, 0
	torn := hasTornTail(src)
	// Each line is written one iteration late so a torn final fragment (no
	// trailing newline) can be withheld instead of being "completed" into a
	// newline-terminated corrupt line.
	var held []byte
	for scanner.Scan() {
		lineNo++
		raw := scanner.Bytes()
		if held != nil {
			if _, err := w.Write(held); err != nil {
				cleanup()
				return 0, err
			}
			kept++
			held = nil
		}
		if _, skip := drop[lineNo]; skip || len(raw) == 0 {
			continue
		}
		held = append(append([]byte(nil), raw...), '\n')
	}
	if err := scanner.Err(); err != nil {
		cleanup()
		return 0, err
	}
	if held != nil && !torn {
		if _, err := w.Write(held); err != nil {
			cleanup()
			return 0, err
		}
		kept++
	}
	if err := w.Flush(); err != nil {
		cleanup()
		return 0, err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return 0, err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return 0, err
	}
	if keepBackup {
		if err := copyFile(path, path+".bak"); err != nil {
			_ = os.Remove(tmpPath)
			return 0, fmt.Errorf("write backup: %w", err)
		}
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return 0, fmt.Errorf("replace events log: %w", err)
	}
	return kept, nil
}

// copyFile copies src to dst (0600), overwriting dst.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
