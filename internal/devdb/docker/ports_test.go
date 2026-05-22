package docker_test

import (
	"errors"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/devdb/docker"
)

func TestAllocator_AcquireReleaseCycle(t *testing.T) {
	a, err := docker.NewAllocator("5500-5502")
	if err != nil {
		t.Fatal(err)
	}
	got := []int{}
	for i := 0; i < 3; i++ {
		p, err := a.Acquire()
		if err != nil {
			t.Fatalf("Acquire %d: %v", i, err)
		}
		got = append(got, p)
	}
	if len(got) != 3 || got[0] != 5500 || got[1] != 5501 || got[2] != 5502 {
		t.Errorf("acquired = %v, want [5500 5501 5502]", got)
	}
	if _, err := a.Acquire(); !errors.Is(err, docker.ErrExhausted) {
		t.Errorf("Acquire on exhausted = %v, want ErrExhausted", err)
	}
	a.Release(5501)
	p, err := a.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	if p != 5501 {
		t.Errorf("recycled port = %d, want 5501", p)
	}
}

func TestNewAllocator_InvalidRange(t *testing.T) {
	cases := []string{"abc", "5500-", "-5500", "5600-5500", "0-0"}
	for _, c := range cases {
		if _, err := docker.NewAllocator(c); err == nil {
			t.Errorf("NewAllocator(%q) should fail", c)
		}
	}
}
