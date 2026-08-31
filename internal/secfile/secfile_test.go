package secfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAtomicCreatesParentsWithPerm(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nested", "dir", "config.toml")
	if err := WriteAtomic(p, []byte("secret = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "secret = 1\n" {
		t.Errorf("content = %q", data)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("file perm = %v, want 0600", info.Mode().Perm())
	}
	dir, err := os.Stat(filepath.Dir(p))
	if err != nil {
		t.Fatal(err)
	}
	if dir.Mode().Perm() != 0o700 {
		t.Errorf("dir perm = %v, want 0700", dir.Mode().Perm())
	}
}

func TestWriteAtomicOverwritesAndLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "token.json")
	for _, content := range []string{"first", "second-and-longer"} {
		if err := WriteAtomic(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	data, _ := os.ReadFile(p)
	if string(data) != "second-and-longer" {
		t.Errorf("content = %q, want the latest write", data)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "token.json" {
		t.Errorf("directory holds leftovers: %v", entries)
	}
}
