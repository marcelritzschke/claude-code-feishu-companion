package update

import (
	"testing"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	s.dir = t.TempDir()
	return s
}

func TestStoreRoundTripsLatestAndNotified(t *testing.T) {
	s := testStore(t)

	if err := s.RecordLatest("1.4.0"); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordNotified("1.4.0"); err != nil {
		t.Fatal(err)
	}

	latest, notified, err := s.Cached()
	if err != nil {
		t.Fatal(err)
	}
	if latest != "1.4.0" || notified != "1.4.0" {
		t.Errorf("Cached() = (%q, %q), want (1.4.0, 1.4.0)", latest, notified)
	}
}

func TestStoreCachedOnMissingFileIsEmpty(t *testing.T) {
	s := testStore(t)

	latest, notified, err := s.Cached()
	if err != nil {
		t.Fatal(err)
	}
	if latest != "" || notified != "" {
		t.Errorf("Cached() = (%q, %q), want both empty", latest, notified)
	}
}

func TestStoreRecordLatestPreservesNotified(t *testing.T) {
	s := testStore(t)

	if err := s.RecordNotified("1.4.0"); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordLatest("1.5.0"); err != nil {
		t.Fatal(err)
	}

	latest, notified, err := s.Cached()
	if err != nil {
		t.Fatal(err)
	}
	if latest != "1.5.0" || notified != "1.4.0" {
		t.Errorf("Cached() = (%q, %q), want (1.5.0, 1.4.0)", latest, notified)
	}
}
