// Package server holds chartroomd's HTTP routes: the lobby API, map and
// token image assets, and the browser client. The WebSocket handler is added
// in #7.
package server

import (
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/njdaniel/chartroom/internal/eventlog"
	"github.com/njdaniel/chartroom/internal/table"
)

// Config is what the server needs.
type Config struct {
	Log       *eventlog.Log
	AssetsDir string // where uploaded images are stored, one file per sha256
	Client    fs.FS  // the built browser client, or nil if none is embedded
}

type server struct {
	Config
}

// New returns the HTTP handler for chartroomd.
func New(cfg Config) http.Handler {
	s := &server{Config: cfg}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealth)
	mux.HandleFunc("GET /api/sessions", s.handleListSessions)
	mux.HandleFunc("GET /api/", http.NotFound) // unknown API paths are not the client
	mux.HandleFunc("POST /api/assets", s.handleUpload)
	mux.HandleFunc("GET /assets/{hash}", s.handleAsset)
	mux.HandleFunc("GET /", s.handleClient)
	return mux
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// SessionSummary is one entry in the lobby. It deliberately carries only
// public fields: session.created also holds the secret dice seed.
type SessionSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Link string `json:"link"`
}

func (s *server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	firsts, err := s.Log.Firsts(r.Context())
	if err != nil {
		slog.Error("list sessions", "err", err)
		http.Error(w, "could not list sessions", http.StatusInternalServerError)
		return
	}
	out := []SessionSummary{}
	for _, e := range firsts {
		if e.Type != table.TypeSessionCreated {
			continue // unreachable while every session starts with session.created
		}
		var d table.SessionCreated
		if err := json.Unmarshal(e.Data, &d); err != nil {
			slog.Error("list sessions: bad session.created", "session", e.Session, "err", err)
			continue
		}
		out = append(out, SessionSummary{ID: e.Session, Name: d.Name, Link: "/s/" + e.Session})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleClient serves the browser client. Paths that are not a file in the
// build (such as /s/<id>) get index.html, so the client can route them.
func (s *server) handleClient(w http.ResponseWriter, r *http.Request) {
	if s.Client == nil {
		http.Error(w, "this chartroomd was built without the browser client: use `make build`, or `make dev` while developing", http.StatusNotFound)
		return
	}
	name := r.URL.Path[1:]
	if name == "" {
		name = "index.html"
	}
	if info, err := fs.Stat(s.Client, name); err != nil || info.IsDir() {
		name = "index.html"
	}
	if name == "index.html" {
		w.Header().Set("Cache-Control", "no-cache")
	} else {
		// Vite puts a content hash in every built file name under static/.
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.ServeFileFS(w, r, s.Client, name)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
