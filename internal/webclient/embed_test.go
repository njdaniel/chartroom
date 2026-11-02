//go:build chartroomweb

package webclient

import (
	"io/fs"
	"testing"
)

func TestEmbeddedClientHasIndex(t *testing.T) {
	files, ok := Files()
	if !ok {
		t.Fatal("Files() reports no client in a chartroomweb build")
	}
	if _, err := fs.Stat(files, "index.html"); err != nil {
		t.Fatalf("embedded client has no index.html: %v", err)
	}
}
