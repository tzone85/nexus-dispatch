package graph

import (
	"testing"
)

func TestExport_Linear(t *testing.T) {
	g := New()
	g.AddNode("a")
	g.AddNode("b")
	g.AddNode("c")
	g.AddEdge("b", "a") // b depends on a
	g.AddEdge("c", "b") // c depends on b

	export := g.Export()

	if len(export.Nodes) != 3 {
		t.Fatalf("nodes = %d, want 3", len(export.Nodes))
	}
	if len(export.Edges) != 2 {
		t.Fatalf("edges = %d, want 2", len(export.Edges))
	}
	if len(export.Waves) != 3 {
		t.Fatalf("waves = %d, want 3", len(export.Waves))
	}

	// Verify wave assignments.
	waveOf := make(map[string]int)
	for _, n := range export.Nodes {
		waveOf[n.ID] = n.Wave
	}
	if waveOf["a"] != 0 {
		t.Errorf("a wave = %d, want 0", waveOf["a"])
	}
	if waveOf["b"] != 1 {
		t.Errorf("b wave = %d, want 1", waveOf["b"])
	}
	if waveOf["c"] != 2 {
		t.Errorf("c wave = %d, want 2", waveOf["c"])
	}
}

func TestExport_Diamond(t *testing.T) {
	g := New()
	g.AddNode("root")
	g.AddNode("left")
	g.AddNode("right")
	g.AddNode("merge")
	g.AddEdge("left", "root")
	g.AddEdge("right", "root")
	g.AddEdge("merge", "left")
	g.AddEdge("merge", "right")

	export := g.Export()

	if len(export.Waves) != 3 {
		t.Fatalf("waves = %d, want 3", len(export.Waves))
	}
	// Wave 0: root, Wave 1: left+right, Wave 2: merge.
	if len(export.Waves[0]) != 1 || export.Waves[0][0] != "root" {
		t.Errorf("wave 0 = %v, want [root]", export.Waves[0])
	}
	if len(export.Waves[1]) != 2 {
		t.Errorf("wave 1 = %v, want 2 nodes", export.Waves[1])
	}
}

func TestExport_NoDeps(t *testing.T) {
	g := New()
	g.AddNode("a")
	g.AddNode("b")
	g.AddNode("c")

	export := g.Export()

	if len(export.Waves) != 1 {
		t.Fatalf("waves = %d, want 1 (all parallel)", len(export.Waves))
	}
	if len(export.Waves[0]) != 3 {
		t.Errorf("wave 0 = %d nodes, want 3", len(export.Waves[0]))
	}
}
