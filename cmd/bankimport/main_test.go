package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestImportCommandRequiresExplicitSourceAndDestination(t *testing.T) {
	for _, args := range [][]string{nil, {"--source-dir", "somewhere"}, {"--source-dir", "somewhere", "--commit", "123"}, {"--source-dir", "somewhere", "--commit", "123", "--bank", "somewhere", "unexpected"}} {
		if err := run(args); err == nil {
			t.Fatalf("accepted incomplete command %v", args)
		}
	}
}

func TestDryRunDoesNotCreateRequestedBank(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "solutions", "Linux", "Create")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(`{"experiment":"Linux","subject":"Create","kind":"code","path":"case1/code.sh","file":"answer.sh"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "answer.sh"), []byte("touch file\n"), 0600); err != nil {
		t.Fatal(err)
	}
	bank := filepath.Join(t.TempDir(), "must-not-exist.sqlite")
	if err := run([]string{"--source-dir", root, "--commit", "1234567890abcdef1234567890abcdef12345678", "--dry-run", "--bank", bank}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(bank); !os.IsNotExist(err) {
		t.Fatal("dry-run created requested persistent bank")
	}
}
