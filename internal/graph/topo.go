package graph

import (
	"fmt"
	"sort"
)

// TopologicalSort returns nodes in dependency order using Kahn's algorithm.
// Nodes with no dependencies appear first. Returns an error if a cycle is detected.
func (g *DAG) TopologicalSort() ([]string, error) {
	inDegree := make(map[string]int)
	for node := range g.nodes {
		inDegree[node] = 0
	}

	// reverse maps a dependency to its dependents: if A depends on B,
	// reverse[B] contains A.
	reverse := make(map[string][]string)
	for node, deps := range g.edges {
		for _, dep := range deps {
			reverse[dep] = append(reverse[dep], node)
			inDegree[node]++
		}
	}

	// Seed queue with nodes that have zero in-degree (no dependencies).
	// Sort for deterministic ordering.
	var queue []string
	for node, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, node)
		}
	}
	sort.Strings(queue)

	var order []string
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		order = append(order, node)

		// For each dependent of this node, decrement in-degree.
		dependents := make([]string, len(reverse[node]))
		copy(dependents, reverse[node])
		sort.Strings(dependents)

		for _, dependent := range dependents {
			inDegree[dependent]--
			if inDegree[dependent] == 0 {
				queue = append(queue, dependent)
			}
		}
	}

	if len(order) != len(g.nodes) {
		return nil, fmt.Errorf("cycle detected: processed %d of %d nodes", len(order), len(g.nodes))
	}
	return order, nil
}

// Waves groups nodes into execution waves. Each wave contains nodes whose
// dependencies have all been satisfied by earlier waves. Nodes within a wave
// can execute in parallel. Returns an error if the graph contains a cycle.
func (g *DAG) Waves() ([][]string, error) {
	if _, err := g.TopologicalSort(); err != nil {
		return nil, err
	}

	var waves [][]string
	completed := make(map[string]bool)

	for len(completed) < len(g.nodes) {
		ready := g.ReadyNodes(completed)
		if len(ready) == 0 {
			break
		}
		waves = append(waves, ready)
		for _, node := range ready {
			completed[node] = true
		}
	}
	return waves, nil
}
