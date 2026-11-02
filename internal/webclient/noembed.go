//go:build !chartroomweb

// Package webclient holds the browser client built into chartroomd. This
// build has none: see embed.go for the one built with -tags chartroomweb.
package webclient

import "io/fs"

// Files reports that no client is embedded.
func Files() (fs.FS, bool) { return nil, false }
