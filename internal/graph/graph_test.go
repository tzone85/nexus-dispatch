package graph_test

import (
	"sort"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/graph"
)

func TestDAG_AddAndDependencies(t *testing.T) {
	g := graph.New()
	g.AddNode("a")
	g.AddNode("b")
	g.AddNode("c")
	g.AddEdge("b", "a") // b depends on a
	g.AddEdge("c", "b") // c depends on b

	deps := g.DependenciesOf("c")
	if len(deps) != 1 || deps[0] != "b" {
		t.Fatalf("expected [b], got %v", deps)
	}
}

func TestDAG_TopologicalSort_Linear(t *testing.T) {
	g := graph.New()
	g.AddNode("a")
	g.AddNode("b")
	g.AddNode("c")
	g.AddEdge("b", "a")
	g.AddEdge("c", "b")

	order, err := g.TopologicalSort()
	if err != nil {
		t.Fatalf("topo sort: %v", err)
	}
	if len(order) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(order))
	}

	pos := posMap(order)
	if pos["a"] > pos["b"] || pos["b"] > pos["c"] {
		t.Fatalf("expected a < b < c, got %v", order)
	}
}

func TestDAG_TopologicalSort_Diamond(t *testing.T) {
	g := graph.New()
	g.AddNode("a")
	g.AddNode("b")
	g.AddNode("c")
	g.AddNode("d")
	g.AddEdge("b", "a") // b depends on a
	g.AddEdge("c", "a") // c depends on a
	g.AddEdge("d", "b") // d depends on b
	g.AddEdge("d", "c") // d depends on c

	order, err := g.TopologicalSort()
	if err != nil {
		t.Fatalf("topo sort: %v", err)
	}
	if len(order) != 4 {
		t.Fatalf("expected 4 nodes, got %d", len(order))
	}

	pos := posMap(order)
	if pos["a"] > pos["b"] || pos["a"] > pos["c"] {
		t.Fatal("a must come before b and c")
	}
	if pos["b"] > pos["d"] || pos["c"] > pos["d"] {
		t.Fatal("b and c must come before d")
	}
}

func TestDAG_CycleDetection(t *testing.T) {
	g := graph.New()
	g.AddNode("a")
	g.AddNode("b")
	g.AddEdge("a", "b")
	g.AddEdge("b", "a")

	_, err := g.TopologicalSort()
	if err == nil {
		t.Fatal("expected cycle error")
	}
}

func TestDAG_CycleDetection_Three(t *testing.T) {
	g := graph.New()
	g.AddNode("a")
	g.AddNode("b")
	g.AddNode("c")
	g.AddEdge("a", "b")
	g.AddEdge("b", "c")
	g.AddEdge("c", "a")

	_, err := g.TopologicalSort()
	if err == nil {
		t.Fatal("expected cycle error")
	}
}

func TestDAG_Waves_Diamond(t *testing.T) {
	g := graph.New()
	g.AddNode("a")
	g.AddNode("b")
	g.AddNode("c")
	g.AddNode("d")
	g.AddEdge("b", "a")
	g.AddEdge("c", "a")
	g.AddEdge("d", "b")

	waves, err := g.Waves()
	if err != nil {
		t.Fatalf("waves: %v", err)
	}
	if len(waves) != 3 {
		t.Fatalf("expected 3 waves, got %d: %v", len(waves), waves)
	}

	// Wave 0: [a] (no deps)
	if len(waves[0]) != 1 || waves[0][0] != "a" {
		t.Fatalf("wave 0: expected [a], got %v", waves[0])
	}
	// Wave 1: [b, c] (depend on a only) — sort for determinism
	wave1 := make([]string, len(waves[1]))
	copy(wave1, waves[1])
	sort.Strings(wave1)
	if len(wave1) != 2 || wave1[0] != "b" || wave1[1] != "c" {
		t.Fatalf("wave 1: expected [b, c], got %v", wave1)
	}
	// Wave 2: [d] (depends on b)
	if len(waves[2]) != 1 || waves[2][0] != "d" {
		t.Fatalf("wave 2: expected [d], got %v", waves[2])
	}
}

func TestDAG_Waves_AllIndependent(t *testing.T) {
	g := graph.New()
	g.AddNode("a")
	g.AddNode("b")
	g.AddNode("c")

	waves, err := g.Waves()
	if err != nil {
		t.Fatalf("waves: %v", err)
	}
	if len(waves) != 1 {
		t.Fatalf("expected 1 wave for independent nodes, got %d", len(waves))
	}
	if len(waves[0]) != 3 {
		t.Fatalf("expected 3 nodes in wave 0, got %d", len(waves[0]))
	}
}

func TestDAG_ReadyNodes(t *testing.T) {
	g := graph.New()
	g.AddNode("a")
	g.AddNode("b")
	g.AddNode("c")
	g.AddEdge("b", "a")
	g.AddEdge("c", "b")

	ready := g.ReadyNodes(map[string]bool{})
	if len(ready) != 1 || ready[0] != "a" {
		t.Fatalf("expected [a], got %v", ready)
	}

	ready = g.ReadyNodes(map[string]bool{"a": true})
	if len(ready) != 1 || ready[0] != "b" {
		t.Fatalf("expected [b], got %v", ready)
	}

	ready = g.ReadyNodes(map[string]bool{"a": true, "b": true})
	if len(ready) != 1 || ready[0] != "c" {
		t.Fatalf("expected [c], got %v", ready)
	}
}

func TestDAG_ReadyNodes_SkipsCompleted(t *testing.T) {
	g := graph.New()
	g.AddNode("a")
	g.AddNode("b")

	ready := g.ReadyNodes(map[string]bool{"a": true})
	if len(ready) != 1 || ready[0] != "b" {
		t.Fatalf("expected [b], got %v", ready)
	}
}

func TestDAG_Empty(t *testing.T) {
	g := graph.New()

	order, err := g.TopologicalSort()
	if err != nil {
		t.Fatalf("topo sort on empty: %v", err)
	}
	if len(order) != 0 {
		t.Fatalf("expected empty order, got %v", order)
	}

	waves, err := g.Waves()
	if err != nil {
		t.Fatalf("waves on empty: %v", err)
	}
	if len(waves) != 0 {
		t.Fatalf("expected empty waves, got %v", waves)
	}
}

func TestDAG_NodeCount(t *testing.T) {
	g := graph.New()
	g.AddNode("a")
	g.AddNode("b")
	g.AddNode("c")

	if g.NodeCount() != 3 {
		t.Fatalf("expected 3 nodes, got %d", g.NodeCount())
	}
}

func posMap(order []string) map[string]int {
	pos := make(map[string]int)
	for i, n := range order {
		pos[n] = i
	}
	return pos
}
