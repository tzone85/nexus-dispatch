package graph

// DAGExport is a JSON-serializable representation of the DAG for frontend
// rendering. Includes nodes with wave assignments and edges.
type DAGExport struct {
	Nodes []NodeExport `json:"nodes"`
	Edges []EdgeExport `json:"edges"`
	Waves [][]string   `json:"waves"`
}

// NodeExport represents a single node in the exported DAG.
type NodeExport struct {
	ID   string `json:"id"`
	Wave int    `json:"wave"`
}

// EdgeExport represents a dependency edge: From depends on To.
type EdgeExport struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Export produces a JSON-serializable DAGExport with wave assignments and
// edges. Returns an empty export if the DAG contains a cycle.
func (g *DAG) Export() DAGExport {
	waves, err := g.Waves()
	if err != nil {
		return DAGExport{}
	}

	// Build wave index: node -> wave number (0-based).
	waveIndex := make(map[string]int)
	for i, wave := range waves {
		for _, node := range wave {
			waveIndex[node] = i
		}
	}

	nodes := make([]NodeExport, 0, len(g.nodes))
	for node := range g.nodes {
		nodes = append(nodes, NodeExport{
			ID:   node,
			Wave: waveIndex[node],
		})
	}

	var edges []EdgeExport
	for from, deps := range g.edges {
		for _, to := range deps {
			edges = append(edges, EdgeExport{From: from, To: to})
		}
	}

	wavesCopy := make([][]string, len(waves))
	for i, w := range waves {
		wavesCopy[i] = make([]string, len(w))
		copy(wavesCopy[i], w)
	}

	return DAGExport{
		Nodes: nodes,
		Edges: edges,
		Waves: wavesCopy,
	}
}
