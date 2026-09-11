package buildid

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The whole point is that replacing the file at a path changes the answer.
// A daemon that has outlived its own binary is otherwise indistinguishable
// from one that has not: same path, same version string, same everything
// it could report about itself.
func TestStampChangesWhenTheFileIsReplaced(t *testing.T) {
	p := filepath.Join(t.TempDir(), "claude-companion")
	write(t, p, "one")
	first := StampOf(p)
	if first == "" {
		t.Fatal("StampOf returned nothing for a file that exists")
	}
	if again := StampOf(p); again != first {
		t.Errorf("a file nobody touched stamped %q then %q", first, again)
	}

	// An install replaces the file rather than editing it, and the
	// replacement carries its own build time.
	write(t, p, "two")
	if err := os.Chtimes(p, time.Now().Add(time.Hour), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if second := StampOf(p); second == first {
		t.Errorf("a replaced file stamped the same: %q", second)
	}
}

// A stamp nobody could read has to be empty, because every caller reads an
// empty stamp as "unknown" and acts on nothing. Anything else here would
// have a daemon retire itself over a failed stat.
func TestUnreadableStampsAreEmpty(t *testing.T) {
	if got := StampOf(""); got != "" {
		t.Errorf(`StampOf("") = %q, want ""`, got)
	}
	if got := StampOf(filepath.Join(t.TempDir(), "not-here")); got != "" {
		t.Errorf("StampOf(missing) = %q, want \"\"", got)
	}
}

// This process's own image is the one thing that cannot have been replaced
// while the test runs, so Replaced must say no and Stamp must say
// something.
func TestThisProcessHasNotBeenReplaced(t *testing.T) {
	if Stamp() == "" {
		t.Fatal("this process could not identify its own image")
	}
	if Path() == "" {
		t.Fatal("this process does not know where it was started from")
	}
	if Replaced() {
		t.Error("Replaced reported a replacement of the running test binary")
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}
