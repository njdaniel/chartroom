// Package server holds chartroomd's HTTP routes. The WebSocket handler,
// lobby, assets, and embedded client are added here in later M0 issues.
package server

import (
	"encoding/json"
	"net/http"
)

// New returns the HTTP handler for chartroomd.
func New() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealth)
	return mux
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
