package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	s.dir = t.TempDir()
	return s
}

func TestMutatePersists(t *testing.T) {
	s := testStore(t)
	now := time.Now()
	err := s.Mutate(func(entries map[string]Entry) {
		entries["sess-1"] = Entry{PromptID: "p-1", MessageID: "om_1", UpdatedAt: now}
	})
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(s.dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var entries map[string]Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatal(err)
	}
	if entries["sess-1"].MessageID != "om_1" {
		t.Errorf("entries = %+v", entries)
	}
}

func TestMutateSeesPriorState(t *testing.T) {
	s := testStore(t)
	if err := s.Mutate(func(entries map[string]Entry) {
		entries["sess-1"] = Entry{PromptID: "p-1", MessageID: "om_1", UpdatedAt: time.Now()}
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Mutate(func(entries map[string]Entry) {
		if entries["sess-1"].MessageID != "om_1" {
			t.Errorf("prior entry missing: %+v", entries)
		}
		delete(entries, "sess-1")
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Mutate(func(entries map[string]Entry) {
		if _, ok := entries["sess-1"]; ok {
			t.Error("deleted entry resurrected")
		}
	}); err != nil {
		t.Fatal(err)
	}
}

func TestMutatePrunesStaleEntries(t *testing.T) {
	s := testStore(t)
	old := time.Now().Add(-2 * maxAge)
	if err := s.Mutate(func(entries map[string]Entry) {
		entries["stale"] = Entry{MessageID: "om_old", UpdatedAt: old}
		entries["live"] = Entry{MessageID: "om_new", UpdatedAt: time.Now()}
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Mutate(func(entries map[string]Entry) {
		if _, ok := entries["stale"]; ok {
			t.Error("stale entry not pruned")
		}
		if _, ok := entries["live"]; !ok {
			t.Error("live entry pruned")
		}
	}); err != nil {
		t.Fatal(err)
	}
}

func TestMutateSurvivesCorruptStateFile(t *testing.T) {
	s := testStore(t)
	if err := os.WriteFile(filepath.Join(s.dir, "state.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Mutate(func(entries map[string]Entry) {
		if len(entries) != 0 {
			t.Errorf("corrupt file should start fresh, got %+v", entries)
		}
	}); err != nil {
		t.Fatal(err)
	}
}

func TestMutateSerializesConcurrentWriters(t *testing.T) {
	s := testStore(t)
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			done <- s.Mutate(func(entries map[string]Entry) {
				entries["sess"] = Entry{MessageID: "om", UpdatedAt: time.Now()}
			})
		}()
	}
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}
