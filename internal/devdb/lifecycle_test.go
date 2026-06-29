package devdb_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/devdb"
	"github.com/tzone85/nexus-dispatch/internal/devdb/null"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// fakeEventStore captures appended events for assertions.
type fakeEventStore struct {
	appended []state.Event
}

func (f *fakeEventStore) Append(evt state.Event) error {
	f.appended = append(f.appended, evt)
	return nil
}

func TestLifecycle_Provision_EmitsCreatedEvent(t *testing.T) {
	es := &fakeEventStore{}
	cfg := devdb.Config{
		Provider: "null",
		Template: "tpl",
	}
	lc := devdb.NewLifecycle(null.New(), es, cfg)
	worktree := t.TempDir()

	_, err := lc.Provision(context.Background(), "story-1", "myproj", worktree)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if len(es.appended) != 1 {
		t.Fatalf("appended events = %d, want 1", len(es.appended))
	}
	got := es.appended[0]
	if got.Type != state.EventStoryDBCreated {
		t.Errorf("event type = %v, want STORY_DB_CREATED", got.Type)
	}
	if got.StoryID != "story-1" {
		t.Errorf("story_id = %q", got.StoryID)
	}
	var payload map[string]any
	if err := json.Unmarshal(got.Payload, &payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if payload["provider"] != "null" {
		t.Errorf("payload.provider = %v, want null", payload["provider"])
	}
	hash, _ := payload["conn_string_hash"].(string)
	if hash == "" || hash[:7] != "sha256:" {
		t.Errorf("conn_string_hash = %q, want sha256:... prefix", hash)
	}
}

func TestLifecycle_Provision_WritesEnvFile(t *testing.T) {
	es := &fakeEventStore{}
	lc := devdb.NewLifecycle(null.New(), es, devdb.Config{Provider: "null"})
	worktree := t.TempDir()

	_, err := lc.Provision(context.Background(), "story-1", "myproj", worktree)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(worktree, ".nxd-db", "connect.env")
	if _, err := os.Stat(p); err != nil {
		t.Errorf("connect.env not created: %v", err)
	}
}

func TestLifecycle_Provision_HashesConnString(t *testing.T) {
	want := sha256.Sum256([]byte("postgres://null@localhost:0/nxd-myproj-story-1"))
	wantHash := "sha256:" + hex.EncodeToString(want[:])

	es := &fakeEventStore{}
	lc := devdb.NewLifecycle(null.New(), es, devdb.Config{Provider: "null"})
	_, err := lc.Provision(context.Background(), "story-1", "myproj", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	_ = json.Unmarshal(es.appended[0].Payload, &payload)
	if payload["conn_string_hash"] != wantHash {
		t.Errorf("hash = %v, want %v", payload["conn_string_hash"], wantHash)
	}
}

// recordingProvider wraps null.Provider and records Delete calls.
type recordingProvider struct {
	*null.Provider
	deleted []string
}

func (r *recordingProvider) Delete(ctx context.Context, dbID string) error {
	r.deleted = append(r.deleted, dbID)
	return nil
}

func TestLifecycle_Release_Success_Deletes(t *testing.T) {
	rp := &recordingProvider{Provider: null.New()}
	es := &fakeEventStore{}
	lc := devdb.NewLifecycle(rp, es, devdb.Config{Provider: "null"})

	db := devdb.DB{ID: "abc", Name: "nxd-myproj-story-1"}
	if err := lc.Release(context.Background(), db, devdb.OutcomeSuccess); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if len(rp.deleted) != 1 || rp.deleted[0] != "abc" {
		t.Errorf("expected one delete call for abc, got %v", rp.deleted)
	}
	if len(es.appended) != 1 {
		t.Fatalf("appended = %d, want 1", len(es.appended))
	}
	if es.appended[0].Type != state.EventStoryDBDeleted {
		t.Errorf("type = %v, want STORY_DB_DELETED", es.appended[0].Type)
	}
	var payload map[string]any
	_ = json.Unmarshal(es.appended[0].Payload, &payload)
	if payload["status"] != "deleted" {
		t.Errorf("status = %v, want deleted", payload["status"])
	}
}

func TestLifecycle_Release_FailedWithKeepDB_Retains(t *testing.T) {
	rp := &recordingProvider{Provider: null.New()}
	es := &fakeEventStore{}
	lc := devdb.NewLifecycle(rp, es, devdb.Config{Provider: "null", KeepDBOnFail: true})

	db := devdb.DB{ID: "abc"}
	if err := lc.Release(context.Background(), db, devdb.OutcomeFailed); err != nil {
		t.Fatal(err)
	}
	if len(rp.deleted) != 0 {
		t.Errorf("expected zero delete calls, got %v", rp.deleted)
	}
	var payload map[string]any
	_ = json.Unmarshal(es.appended[0].Payload, &payload)
	if payload["status"] != "retained" {
		t.Errorf("status = %v, want retained", payload["status"])
	}
}

func TestLifecycle_Release_FailedWithoutKeepDB_Deletes(t *testing.T) {
	rp := &recordingProvider{Provider: null.New()}
	es := &fakeEventStore{}
	lc := devdb.NewLifecycle(rp, es, devdb.Config{Provider: "null", KeepDBOnFail: false})

	if err := lc.Release(context.Background(), devdb.DB{ID: "abc"}, devdb.OutcomeFailed); err != nil {
		t.Fatal(err)
	}
	if len(rp.deleted) != 1 {
		t.Errorf("expected one delete call, got %v", rp.deleted)
	}
}

// fakeProjector records events the lifecycle drives into the read model.
type fakeProjector struct {
	projected []state.Event
}

func (f *fakeProjector) Project(evt state.Event) error {
	f.projected = append(f.projected, evt)
	return nil
}

func TestLifecycle_Provision_ProjectsCreatedEvent(t *testing.T) {
	es := &fakeEventStore{}
	pr := &fakeProjector{}
	lc := devdb.NewLifecycle(null.New(), es, devdb.Config{Provider: "null"}).WithProjector(pr)

	if _, err := lc.Provision(context.Background(), "story-1", "myproj", t.TempDir()); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if len(pr.projected) != 1 || pr.projected[0].Type != state.EventStoryDBCreated {
		t.Fatalf("STORY_DB_CREATED must be projected, got %+v", pr.projected)
	}
	if pr.projected[0].StoryID != "story-1" {
		t.Errorf("projected created story_id = %q, want story-1", pr.projected[0].StoryID)
	}
}

// Release must recover the story ID from the DB name and project the deletion;
// projectStoryDBDeleted keys its UPDATE on story_id, so an empty value would
// silently match zero rows (regression guard for the missing-StoryID bug).
func TestLifecycle_Release_ProjectsDeletedEventWithStoryID(t *testing.T) {
	es := &fakeEventStore{}
	pr := &fakeProjector{}
	lc := devdb.NewLifecycle(null.New(), es, devdb.Config{Provider: "null"}).WithProjector(pr)

	db := devdb.DB{ID: "null-nxd-myproj-story-1", Name: "nxd-myproj-story-1"}
	if err := lc.Release(context.Background(), db, devdb.OutcomeSuccess); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if len(pr.projected) != 1 || pr.projected[0].Type != state.EventStoryDBDeleted {
		t.Fatalf("STORY_DB_DELETED must be projected, got %+v", pr.projected)
	}
	if pr.projected[0].StoryID != "story-1" {
		t.Errorf("deleted event story_id = %q, want story-1 (recovered from db name)", pr.projected[0].StoryID)
	}
}

// End-to-end: provisioning then releasing must create and then transition a
// story_databases row in a real projection store. Before the wiring fix the
// table stayed empty in production.
func TestLifecycle_ProjectsRoundTripIntoSQLiteStore(t *testing.T) {
	es := &fakeEventStore{}
	store, err := state.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	lc := devdb.NewLifecycle(null.New(), es, devdb.Config{Provider: "null"}).WithProjector(store)

	db, err := lc.Provision(context.Background(), "story-1", "myproj", t.TempDir())
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	rows, err := store.ListStoryDatabases(state.StoryDBFilter{})
	if err != nil {
		t.Fatalf("ListStoryDatabases: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected a story_databases row after Provision, got %d", len(rows))
	}
	if rows[0].Status != "created" {
		t.Errorf("status after provision = %q, want created", rows[0].Status)
	}

	if err := lc.Release(context.Background(), db, devdb.OutcomeSuccess); err != nil {
		t.Fatalf("Release: %v", err)
	}
	rows, err = store.ListStoryDatabases(state.StoryDBFilter{})
	if err != nil {
		t.Fatalf("ListStoryDatabases after release: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row after Release, got %d", len(rows))
	}
	if rows[0].Status != "deleted" {
		t.Errorf("status after release = %q, want deleted (StoryID-keyed UPDATE must match)", rows[0].Status)
	}
}
