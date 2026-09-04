package rag

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestIndexQuery(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"),
		"package main\nfunc CalculateTotal(items []int) int {\n  sum := 0\n  for _, i := range items { sum += i }\n  return sum\n}\n")
	writeFile(t, filepath.Join(dir, "b.py"),
		"def greet(name):\n    return 'hello ' + name\n")

	idx := NewIndex(dir)
	if idx.Root() != dir {
		t.Fatalf("root %q", idx.Root())
	}

	snips := idx.Query("CalculateTotal", 3)
	if len(snips) == 0 {
		t.Fatal("expected hit for CalculateTotal")
	}
	found := false
	for _, s := range snips {
		if s.File == "a.go" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a.go in snippets, got %+v", snips)
	}

	if got := idx.Query("nonexistentthingxyz", 3); len(got) != 0 {
		t.Fatalf("expected no match, got %d", len(got))
	}
}

func TestIndexIgnoresVendoredAndBinary(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "node_modules", "x", "x.js"), "const secretToken = 1\n")
	writeFile(t, filepath.Join(dir, "main.go"), "package main\nfunc SecretToken() int { return 1 }\n")
	idx := NewIndex(dir)
	if got := idx.Query("SecretToken", 3); len(got) == 0 {
		t.Fatal("expected main.go hit")
	}
	for _, s := range idx.Query("x", 3) {
		if s.File == "node_modules/x/x.js" {
			t.Fatal("should have ignored node_modules")
		}
	}
}
