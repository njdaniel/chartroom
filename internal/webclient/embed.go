//go:build chartroomweb

// Package webclient holds the browser client built into chartroomd. It is
// there only when chartroomd is built with -tags chartroomweb (make build),
// which needs internal/webclient/dist to hold a client build. Without the tag
// the package embeds nothing, so go build and go test need neither Node nor a
// client build.
package webclient

import (
	"embed"
	"io/fs"
)

// With the tag set, an empty or missing dist fails the compile, as intended.
//
//go:embed all:dist
var dist embed.FS

// Files returns the embedded client, rooted at index.html, and true.
func Files() (fs.FS, bool) {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil, false // unreachable: "dist" is valid and embedded
	}
	return sub, true
}
