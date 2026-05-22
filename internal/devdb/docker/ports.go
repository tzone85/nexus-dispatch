// Package docker implements the devdb.Provider backed by a local Docker daemon
// and a long-lived Postgres host container.
package docker

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// ErrExhausted is returned by Allocator.Acquire when all ports are in use.
var ErrExhausted = errors.New("docker: host port range exhausted")

// Allocator hands out host ports from a range. Currently used for the single
// host container's published port; future per-DB containers will reuse it.
type Allocator struct {
	mu    sync.Mutex
	taken map[int]bool
	start int
	end   int
}

// NewAllocator parses "start-end" (inclusive) and returns a ready allocator.
func NewAllocator(rangeSpec string) (*Allocator, error) {
	parts := strings.SplitN(rangeSpec, "-", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("docker: invalid port range %q (want start-end)", rangeSpec)
	}
	start, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || start <= 0 {
		return nil, fmt.Errorf("docker: invalid start port in %q: %v", rangeSpec, err)
	}
	end, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || end < start {
		return nil, fmt.Errorf("docker: invalid end port in %q", rangeSpec)
	}
	return &Allocator{taken: map[int]bool{}, start: start, end: end}, nil
}

// Acquire returns the lowest free port in the range, or ErrExhausted if all ports are taken.
func (a *Allocator) Acquire() (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for p := a.start; p <= a.end; p++ {
		if !a.taken[p] {
			a.taken[p] = true
			return p, nil
		}
	}
	return 0, ErrExhausted
}

// Release marks a port free for re-acquisition.
func (a *Allocator) Release(port int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.taken, port)
}
