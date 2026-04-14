package codegraph

import (
	"database/sql"
	"fmt"
	"os"

	_ "github.com/mattn/go-sqlite3"
)

// GraphDB reads the code-review-graph SQLite database directly from Go.
// This avoids subprocess overhead for simple queries like stats and callers.
type GraphDB struct {
	db *sql.DB
}

// Open opens the graph database at the given repo path.
// Returns an error if the database file does not exist.
func Open(repoPath string) (*GraphDB, error) {
	dbPath := GraphDBPath(repoPath)
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("graph database not found: %s", dbPath)
	}
	db, err := sql.Open("sqlite3", dbPath+"?mode=ro&_journal_mode=WAL")
	if err != nil {
		return nil, fmt.Errorf("open graph db: %w", err)
	}
	return &GraphDB{db: db}, nil
}

// Close closes the database connection.
func (g *GraphDB) Close() error {
	if g.db == nil {
		return nil
	}
	return g.db.Close()
}

// Stats returns node, edge, and file counts from the graph.
func (g *GraphDB) Stats() (GraphInfo, error) {
	var info GraphInfo

	row := g.db.QueryRow("SELECT COUNT(*) FROM nodes")
	if err := row.Scan(&info.NodeCount); err != nil {
		return info, fmt.Errorf("count nodes: %w", err)
	}

	row = g.db.QueryRow("SELECT COUNT(*) FROM edges")
	if err := row.Scan(&info.EdgeCount); err != nil {
		return info, fmt.Errorf("count edges: %w", err)
	}

	row = g.db.QueryRow("SELECT COUNT(DISTINCT file_path) FROM nodes")
	if err := row.Scan(&info.FileCount); err != nil {
		return info, fmt.Errorf("count files: %w", err)
	}

	rows, err := g.db.Query("SELECT DISTINCT language FROM nodes WHERE language IS NOT NULL AND language != ''")
	if err != nil {
		return info, fmt.Errorf("query languages: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var lang string
		if err := rows.Scan(&lang); err == nil {
			info.Languages = append(info.Languages, lang)
		}
	}

	return info, nil
}

// CallersOf returns functions that call the given qualified name.
func (g *GraphDB) CallersOf(qualifiedName string) ([]ChangedNode, error) {
	query := `
		SELECT n.name, n.file_path, n.kind, n.line_start, n.line_end, n.is_test
		FROM edges e
		JOIN nodes n ON n.qualified_name = e.source_qualified
		WHERE e.target_qualified = ? AND e.kind = 'CALLS'
		ORDER BY n.file_path, n.line_start
	`
	rows, err := g.db.Query(query, qualifiedName)
	if err != nil {
		return nil, fmt.Errorf("query callers: %w", err)
	}
	defer rows.Close()

	var nodes []ChangedNode
	for rows.Next() {
		var n ChangedNode
		var isTest int
		if err := rows.Scan(&n.Name, &n.FilePath, &n.Kind, &n.LineStart, &n.LineEnd, &isTest); err != nil {
			continue
		}
		n.IsTest = isTest != 0
		nodes = append(nodes, n)
	}
	return nodes, nil
}

// TestsFor returns test nodes that are in the same file or reference the given file path.
func (g *GraphDB) TestsFor(filePath string) ([]ChangedNode, error) {
	query := `
		SELECT n.name, n.file_path, n.kind, n.line_start, n.line_end
		FROM nodes n
		WHERE n.is_test = 1
		AND (n.file_path = ? OR n.file_path LIKE ?)
		ORDER BY n.file_path, n.line_start
	`
	// Match both the exact file and its _test.go companion
	testPattern := filePath[:len(filePath)-3] + "_test.go"
	rows, err := g.db.Query(query, filePath, testPattern)
	if err != nil {
		return nil, fmt.Errorf("query tests: %w", err)
	}
	defer rows.Close()

	var nodes []ChangedNode
	for rows.Next() {
		var n ChangedNode
		if err := rows.Scan(&n.Name, &n.FilePath, &n.Kind, &n.LineStart, &n.LineEnd); err != nil {
			continue
		}
		n.IsTest = true
		nodes = append(nodes, n)
	}
	return nodes, nil
}
