package dashboard

import (
	"fmt"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func TestNew(t *testing.T) {
	// New should create a model without panicking
	es := &mockEventStore{}
	m := New(es, nil, "v0.1.0", state.ReqFilter{})
	if m.version != "v0.1.0" {
		t.Errorf("expected version v0.1.0, got %q", m.version)
	}
}

func TestInit(t *testing.T) {
	es := &mockEventStore{}
	m := New(es, nil, "test", state.ReqFilter{})
	cmd := m.Init()
	if cmd == nil {
		t.Error("Init should return a non-nil command batch")
	}
}

func TestUpdate_TickMsg(t *testing.T) {
	es := &mockEventStore{}
	m := New(es, nil, "test", state.ReqFilter{})
	m.width = 80
	m.height = 24

	updated, cmd := m.Update(tickMsg(time.Now()))
	if cmd == nil {
		t.Error("tick should return a command for next tick + fetch")
	}
	model := updated.(Model)
	if model.version != "test" {
		t.Error("model version should be preserved after tick")
	}
}

func TestUpdate_WindowSizeMsg(t *testing.T) {
	m := Model{version: "test"}
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model := updated.(Model)
	if model.width != 120 {
		t.Errorf("expected width 120, got %d", model.width)
	}
	if model.height != 40 {
		t.Errorf("expected height 40, got %d", model.height)
	}
}

func TestUpdate_DataMsg(t *testing.T) {
	m := Model{version: "test", width: 80, height: 24}
	msg := dataMsg{
		requirements: []state.Requirement{{ID: "r-001", Title: "Test"}},
		stories:      []state.Story{{ID: "s-001", Title: "Story"}},
		events:       []state.Event{{Type: "TEST"}},
	}
	updated, _ := m.Update(msg)
	model := updated.(Model)
	if len(model.requirements) != 1 {
		t.Errorf("expected 1 requirement, got %d", len(model.requirements))
	}
	if len(model.stories) != 1 {
		t.Errorf("expected 1 story, got %d", len(model.stories))
	}
}

func TestUpdate_DataMsg_Error(t *testing.T) {
	m := Model{version: "test", width: 80, height: 24}
	msg := dataMsg{err: errMock}
	updated, _ := m.Update(msg)
	model := updated.(Model)
	if model.err == nil {
		t.Error("expected error to be set")
	}
}

func TestTickCmd(t *testing.T) {
	cmd := tickCmd()
	if cmd == nil {
		t.Error("tickCmd should return a non-nil command")
	}
}

func TestFetchData_NilStore(t *testing.T) {
	m := Model{version: "test"}
	cmd := m.fetchData()
	if cmd == nil {
		t.Error("fetchData should return a command even with nil stores")
	}
}

// mockEventStore is a minimal EventStore for dashboard tests.
type mockEventStore struct{}

var errMock = fmt.Errorf("test error")

func (m *mockEventStore) Append(evt state.Event) error                     { return nil }
func (m *mockEventStore) List(filter state.EventFilter) ([]state.Event, error) { return nil, nil }
func (m *mockEventStore) Count(filter state.EventFilter) (int, error)      { return 0, nil }
func (m *mockEventStore) Close() error                                     { return nil }
