package null_test

import (
	"context"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/devdb"
	"github.com/tzone85/nexus-dispatch/internal/devdb/null"
)

func TestNullProvider_Name(t *testing.T) {
	p := null.New()
	if p.Name() != "null" {
		t.Errorf("Name = %q, want null", p.Name())
	}
}

func TestNullProvider_Create_Deterministic(t *testing.T) {
	p := null.New()
	ctx := context.Background()
	db, err := p.Create(ctx, devdb.CreateOpts{Name: "nxd-test-story-1"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if db.Name != "nxd-test-story-1" {
		t.Errorf("Name = %q, want nxd-test-story-1", db.Name)
	}
	if db.Provider != "null" {
		t.Errorf("Provider = %q, want null", db.Provider)
	}
	if db.ConnectionString == "" {
		t.Error("ConnectionString empty")
	}
}

func TestNullProvider_Fork_BehavesLikeCreate(t *testing.T) {
	p := null.New()
	db, err := p.Fork(context.Background(), "src", devdb.CreateOpts{Name: "nxd-fork-1"})
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	if db.Name != "nxd-fork-1" {
		t.Errorf("Fork.Name = %q", db.Name)
	}
}

func TestNullProvider_DeleteListPingAllSucceed(t *testing.T) {
	p := null.New()
	ctx := context.Background()
	if err := p.Delete(ctx, "anything"); err != nil {
		t.Errorf("Delete: %v", err)
	}
	got, err := p.List(ctx)
	if err != nil {
		t.Errorf("List: %v", err)
	}
	if got == nil {
		t.Error("List should return non-nil slice")
	}
	if err := p.Ping(ctx); err != nil {
		t.Errorf("Ping: %v", err)
	}
}

func TestNullProvider_Schema_Empty(t *testing.T) {
	p := null.New()
	s, err := p.Schema(context.Background(), "x")
	if err != nil {
		t.Errorf("Schema: %v", err)
	}
	if s != "" {
		t.Errorf("Schema = %q, want empty", s)
	}
}

func TestNullProvider_SatisfiesInterface(t *testing.T) {
	var _ devdb.Provider = null.New()
}
