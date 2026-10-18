package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteSecretReplacesAtomicallyWithReadableMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")

	if err := writeSecret(path, "stk_first"); err != nil {
		t.Fatal(err)
	}
	if err := writeSecret(path, "stk_second"); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil || string(got) != "stk_second\n" {
		t.Fatalf("file = %q, %v; want the second secret", got, err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want 0644", info.Mode().Perm())
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("%d files left in the directory, want only the token", len(entries))
	}
}
