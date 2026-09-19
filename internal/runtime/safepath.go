package runtime

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// safePath resolves a relative path within the working directory and rejects
// any path traversal attempts. Symlinks are resolved to prevent escaping
// the work directory via symlink indirection.
//
// This is a check-then-use guard: the sinks (os.MkdirAll/os.WriteFile/
// os.ReadFile) follow whatever exists by the time they run. Kernel-enforced
// confinement via os.Root (os.OpenRoot(workDir) + root.MkdirAll/WriteFile/
// ReadFile) would close that window; it is a follow-up because it changes
// every sink and the read path together, and this fix targets the write
// escape. Rejections carry only the path relative to the work directory, and
// the read/write/edit sinks strip host paths from their I/O errors too — the
// agent reads them to self-correct and must not learn host paths.
func safePath(relPath, workDir string) (string, error) {
	cleaned := filepath.Clean(filepath.Join(workDir, relPath))
	cleanedWorkDir := filepath.Clean(workDir)

	// Ensure the cleaned path is within workDir before symlink resolution.
	if !strings.HasPrefix(cleaned, cleanedWorkDir+string(filepath.Separator)) &&
		cleaned != cleanedWorkDir {
		return "", &pathRejection{
			agentMsg: fmt.Sprintf("path traversal blocked: %s resolves outside work directory", relPath),
			Detail:   fmt.Sprintf("path traversal blocked: %q resolves to %q, outside %q", relPath, cleaned, cleanedWorkDir),
		}
	}

	// A workDir that cannot be resolved (missing, or a dangling symlink) keeps
	// its lexical path: every later comparison then runs against an unresolved
	// root, so a real path under it (macOS /var -> /private/var) looks like an
	// escape and is rejected. That is the safe direction, and the walk below
	// stops at workDir either way.
	realWorkDir, wdErr := filepath.EvalSymlinks(cleanedWorkDir)
	if wdErr != nil {
		realWorkDir = cleanedWorkDir
	}

	// The lexical check above is not enough: any component of the path may be
	// a symlink (e.g. checked out verbatim from an untrusted repo) and the
	// write sinks follow it. Walk from the target up to workDir. The first
	// component that exists (Lstat) must resolve, and resolve inside workDir.
	// A dangling symlink — as the final component or as a parent — a symlink
	// loop, or an unreadable ancestor fails closed: os.WriteFile opens with
	// O_CREATE and no O_NOFOLLOW, so a dangling final symlink would otherwise
	// create its target outside confinement. The walk is bounded at workDir,
	// so a nonexistent workDir is accepted lexically rather than walking up
	// to "/".
	for p := cleaned; ; p = filepath.Dir(p) {
		realPath, exists, err := resolveComponent(p, relPath, cleanedWorkDir, realWorkDir)
		if err != nil {
			return "", err
		}
		if exists {
			if p == cleaned {
				// Target exists: hand back the resolved path so the sinks
				// operate on the real file.
				return realPath, nil
			}
			break
		}
		if p == cleanedWorkDir {
			break
		}
	}

	return cleaned, nil
}

// resolveComponent checks one component of the walk. exists reports whether
// p is present (Lstat); when it is, realPath is its resolved location, already
// proven to be inside the work directory. Anything that cannot be verified is
// an error, and the error names only the work-directory-relative component.
func resolveComponent(p, relPath, cleanedWorkDir, realWorkDir string) (realPath string, exists bool, err error) {
	// Relative to the work directory ("." at the boundary) so a rejection
	// never carries the host path, even when workDir itself is the
	// component that cannot be verified.
	component, relErr := filepath.Rel(cleanedWorkDir, p)
	if relErr != nil {
		component = relPath
	}
	if _, lErr := os.Lstat(p); lErr != nil {
		if errors.Is(lErr, fs.ErrNotExist) {
			return "", false, nil
		}
		return "", false, &pathRejection{
			agentMsg: fmt.Sprintf("path rejected: %s: cannot verify component %q stays inside the work directory: %v", relPath, component, errReason(lErr)),
			Detail:   fmt.Sprintf("path rejected: %q: lstat %q: %q", relPath, p, lErr.Error()),
		}
	}
	realPath, rErr := filepath.EvalSymlinks(p)
	if rErr != nil {
		// Dangling link, loop, ENOTDIR, ENAMETOOLONG: not necessarily an
		// escape, but it cannot be proven safe — fail closed.
		return "", false, &pathRejection{
			agentMsg: fmt.Sprintf("path rejected: %s: cannot verify component %q stays inside the work directory: %v", relPath, component, errReason(rErr)),
			Detail:   fmt.Sprintf("path rejected: %q: resolve %q: %q", relPath, p, rErr.Error()),
		}
	}
	if realPath != realWorkDir && !strings.HasPrefix(realPath, realWorkDir+string(filepath.Separator)) {
		return "", false, &pathRejection{
			agentMsg: fmt.Sprintf("path traversal blocked: %s resolves outside work directory via symlink", relPath),
			Detail:   fmt.Sprintf("path traversal blocked: %q: %q resolves to %q, outside %q", relPath, p, realPath, realWorkDir),
		}
	}
	return realPath, true, nil
}

// pathRejection is a safePath refusal with two audiences: Error() is what
// the agent may read (work-directory-relative components, no host path);
// Detail is the operator's version — the real paths and the underlying
// error, which is what explains a blocked escape — and only the log gets it.
type pathRejection struct {
	agentMsg string
	Detail   string
}

func (r *pathRejection) Error() string { return r.agentMsg }

// rejectionDetail is what the sinks log for an error from safePath: the
// operator-side detail of a rejection, or the error itself otherwise. Every
// agent-controlled path and every wrapped error in it is %q-quoted: the agent
// chooses the path, and a newline in it must not become a second log line (a
// forged "[native-runtime] … all clear").
func rejectionDetail(err error) string {
	var rej *pathRejection
	if errors.As(err, &rej) {
		return rej.Detail
	}
	return fmt.Sprintf("%q", err.Error())
}

// errReason strips the operation and host path from an I/O error so the
// message handed back to the agent carries only the errno text (e.g. "no such
// file or directory"), never the absolute path it came from. *fs.PathError,
// *os.LinkError and *os.SyscallError are the shapes the sinks produce.
func errReason(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	var le *os.LinkError
	if errors.As(err, &le) {
		return le.Err
	}
	var se *os.SyscallError
	if errors.As(err, &se) {
		return se.Err
	}
	return err
}
