package state

import (
	"path/filepath"
	"testing"
)

func setupSQLiteForDB(t *testing.T) *SQLiteStore {
	t.Helper()
	dir := t.TempDir()
	store, err := NewSQLiteStore(filepath.Join(dir, "nxd.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestSQLiteStore_ListStoryDatabases_Empty(t *testing.T) {
	store := setupSQLiteForDB(t)
	rows, err := store.ListStoryDatabases(StoryDBFilter{})
	if err != nil {
		t.Fatalf("ListStoryDatabases: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 rows, got %d", len(rows))
	}
}

func TestSQLiteStore_ListStoryDatabases_AfterCreatedEvent(t *testing.T) {
	store := setupSQLiteForDB(t)

	evt := NewEvent(EventStoryDBCreated, "lifecycle", "S1", map[string]any{
		"db_id":            "nxd-S1-abc",
		"db_name":          "nxd-S1-abc",
		"provider":         "docker",
		"template":         "base",
		"conn_string_hash": "sha256:xyz",
	})
	if err := store.Project(evt); err != nil {
		t.Fatalf("Project: %v", err)
	}

	rows, err := store.ListStoryDatabases(StoryDBFilter{})
	if err != nil {
		t.Fatalf("ListStoryDatabases: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	got := rows[0]
	if got.StoryID != "S1" || got.DBName != "nxd-S1-abc" {
		t.Errorf("unexpected row: %+v", got)
	}
	if got.Status != "created" {
		t.Errorf("Status = %q, want created", got.Status)
	}
	if got.Provider != "docker" {
		t.Errorf("Provider = %q, want docker", got.Provider)
	}
}

func TestSQLiteStore_ListStoryDatabases_FilterByStoryID(t *testing.T) {
	store := setupSQLiteForDB(t)

	for _, sid := range []string{"S1", "S2", "S3"} {
		evt := NewEvent(EventStoryDBCreated, "lifecycle", sid, map[string]any{
			"db_id": "db-" + sid, "db_name": "nxd-" + sid, "provider": "docker",
		})
		if err := store.Project(evt); err != nil {
			t.Fatalf("Project: %v", err)
		}
	}

	rows, err := store.ListStoryDatabases(StoryDBFilter{StoryID: "S2"})
	if err != nil {
		t.Fatalf("ListStoryDatabases: %v", err)
	}
	if len(rows) != 1 || rows[0].StoryID != "S2" {
		t.Errorf("expected only S2, got: %+v", rows)
	}
}

func TestSQLiteStore_ListStoryDatabases_FilterByStatus(t *testing.T) {
	store := setupSQLiteForDB(t)

	store.Project(NewEvent(EventStoryDBCreated, "lifecycle", "S1", map[string]any{
		"db_id": "d1", "db_name": "n1", "provider": "docker",
	}))
	store.Project(NewEvent(EventStoryDBFailed, "lifecycle", "S2", map[string]any{
		"db_id": "d2", "db_name": "n2", "provider": "docker", "error": "timeout",
	}))

	created, err := store.ListStoryDatabases(StoryDBFilter{Status: "created"})
	if err != nil {
		t.Fatalf("filter created: %v", err)
	}
	if len(created) != 1 || created[0].StoryID != "S1" {
		t.Errorf("expected one created row for S1, got: %+v", created)
	}

	failed, err := store.ListStoryDatabases(StoryDBFilter{Status: "failed"})
	if err != nil {
		t.Fatalf("filter failed: %v", err)
	}
	if len(failed) != 1 || failed[0].Error != "timeout" {
		t.Errorf("expected one failed row with error, got: %+v", failed)
	}
}

func TestSQLiteStore_ListStoryDatabases_AfterDeletedEvent(t *testing.T) {
	store := setupSQLiteForDB(t)

	store.Project(NewEvent(EventStoryDBCreated, "lifecycle", "S1", map[string]any{
		"db_id": "d1", "db_name": "n1", "provider": "docker",
	}))
	store.Project(NewEvent(EventStoryDBDeleted, "lifecycle", "S1", map[string]any{
		"db_id":            "d1",
		"status":           "deleted",
		"duration_seconds": 12.5,
		"bytes_used":       1024 * 1024,
	}))

	rows, err := store.ListStoryDatabases(StoryDBFilter{StoryID: "S1"})
	if err != nil {
		t.Fatalf("ListStoryDatabases: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Status != "deleted" {
		t.Errorf("Status = %q, want deleted", rows[0].Status)
	}
	if rows[0].DurationSeconds != 12.5 {
		t.Errorf("DurationSeconds = %v, want 12.5", rows[0].DurationSeconds)
	}
	if rows[0].BytesUsed != 1024*1024 {
		t.Errorf("BytesUsed = %d, want %d", rows[0].BytesUsed, 1024*1024)
	}
}
