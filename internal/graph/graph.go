package graph

import "sort"

// DAG represents a directed acyclic graph where edges encode dependencies.
// An edge from node A to node B means "A depends on B" (B must complete before A).
type DAG struct {
	nodes map[string]bool
	edges map[string][]string // node -> list of nodes it depends on
}

// New creates an empty DAG.
func New() *DAG {
	return &DAG{
		nodes: make(map[string]bool),
		edges: make(map[string][]string),
	}
}

// AddNode registers a node in the graph. Duplicate calls are safe (idempotent).
func (g *DAG) AddNode(id string) {
	g.nodes[id] = true
}

// AddEdge records that `from` depends on `to`.
// Both nodes should already exist via AddNode.
func (g *DAG) AddEdge(from, to string) {
	g.edges[from] = append(g.edges[from], to)
}

// DependenciesOf returns the direct dependencies of the given node.
// Returns nil if the node has no dependencies.
func (g *DAG) DependenciesOf(id string) []string {
	return g.edges[id]
}

// NodeCount returns the number of nodes in the graph.
func (g *DAG) NodeCount() int {
	return len(g.nodes)
}

// ReadyNodes returns all nodes whose dependencies are fully satisfied
// (present in the completed set) and that are not yet completed themselves.
// The returned slice is sorted for deterministic output.
func (g *DAG) ReadyNodes(completed map[string]bool) []string {
	var ready []string
	for node := range g.nodes {
		if completed[node] {
			continue
		}
		allDepsComplete := true
		for _, dep := range g.edges[node] {
			if !completed[dep] {
				allDepsComplete = false
				break
			}
		}
		if allDepsComplete {
			ready = append(ready, node)
		}
	}
	sort.Strings(ready)
	return ready
}
