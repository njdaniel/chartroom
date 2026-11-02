package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestGeneratedFileIsCurrent fails when internal/protocol has changed and
// `make gen` has not been run.
func TestGeneratedFileIsCurrent(t *testing.T) {
	root := filepath.Join("..", "..")
	want, err := generate(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, OutFile))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s is stale: run `make gen`", OutFile)
	}
}
