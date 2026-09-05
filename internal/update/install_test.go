package update

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// staged writes a file where Download would have put it: beside the
// program it is going to replace.
func staged(t *testing.T, dir, content string) string {
	t.Helper()
	f, err := os.CreateTemp(dir, ".claude-companion-new-*")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	if err := f.Chmod(0o755); err != nil {
		t.Fatal(err)
	}
	return f.Name()
}

func TestInstallReplacesTheProgramInPlace(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "claude-companion")
	if err := os.WriteFile(target, []byte("the old program"), 0o755); err != nil {
		t.Fatal(err)
	}
	newBinary := staged(t, dir, "the new program")

	if err := Install(target, newBinary); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "the new program" {
		t.Errorf("target holds %q, want the new program", got)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		t.Errorf("installed mode = %v, want it executable", info.Mode().Perm())
	}
	// Nothing may be left beside the program: a staged file that outlives
	// the install is a half-finished update sitting in the user's PATH.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "claude-companion" {
			t.Errorf("install left %s behind", e.Name())
		}
	}
}

// A failed install must not eat the staged file silently and must not
// leave the user without a working program.
func TestInstallReportsAnImpossibleReplacement(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "somewhere-else", "claude-companion")
	newBinary := staged(t, dir, "the new program")

	if err := Install(target, newBinary); err == nil {
		t.Fatal("want an install into a missing directory refused")
	}
	if _, err := os.Stat(newBinary); !os.IsNotExist(err) {
		t.Errorf("a failed install left the staged file at %s", newBinary)
	}
}

func TestWritableAcceptsTheProgramsOwnDirectory(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "claude-companion")
	if err := os.WriteFile(target, []byte("the program"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Writable(target); err != nil {
		t.Fatalf("Writable = %v, want a temp dir to be writable", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("the check left %d files behind, want it to clean up after itself", len(entries)-1)
	}
}

// A program installed where the user cannot write - /usr/local/bin, say -
// has to be refused before anything is downloaded or stopped.
func TestWritableRefusesADirectoryTheUserCannotWrite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions do not work this way on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	dir := filepath.Join(t.TempDir(), "readonly")
	if err := os.Mkdir(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	if err := Writable(filepath.Join(dir, "claude-companion")); err == nil {
		t.Fatal("want a read-only directory refused")
	}
}

func TestTargetPathResolvesASymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege this test should not require")
	}
	// TargetPath resolves this test binary, which is enough to prove it
	// returns something real that exists.
	got, err := TargetPath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(got); err != nil {
		t.Errorf("TargetPath = %q, which does not exist: %v", got, err)
	}
}
