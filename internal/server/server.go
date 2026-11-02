// Package server holds chartroomd's HTTP routes: the lobby API, map and
// token image assets, the browser client, and the WebSocket at /ws.
package server

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/njdaniel/chartroom/internal/eventlog"
	"github.com/njdaniel/chartroom/internal/session"
	"github.com/njdaniel/chartroom/internal/table"
	"github.com/njdaniel/chartroom/internal/visibility"
)

// Config is what the server needs.
type Config struct {
	Log       *eventlog.Log
	Sessions  *session.Manager
	AssetsDir string // where uploaded images are stored, one file per sha256
	Client    fs.FS  // the built browser client, or nil if none is embedded

	// Filter is what each seat may see. Nil means visibility.M0. Tests swap
	// it to check that nothing reaches a client without passing through it.
	Filter visibility.Filter
	// PingInterval is how often /ws pings; a client that misses two pings
	// is dropped. Zero means DefaultPingInterval.
	PingInterval time.Duration
	// SendQueue is how many frames may wait for a client before it is
	// dropped as stuck. Zero means DefaultSendQueue.
	SendQueue int
}

type server struct {
	Config
	filter       visibility.Filter
	pingInterval time.Duration
	sendQueue    int
}

// New returns the HTTP handler for chartroomd.
func New(cfg Config) http.Handler {
	s := &server{Config: cfg, filter: cfg.Filter, pingInterval: cfg.PingInterval, sendQueue: cfg.SendQueue}
	if s.filter == nil {
		s.filter = visibility.M0{}
	}
	if s.pingInterval == 0 {
		s.pingInterval = DefaultPingInterval
	}
	if s.sendQueue == 0 {
		s.sendQueue = DefaultSendQueue
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealth)
	mux.HandleFunc("GET /ws", s.handleWS)
	mux.HandleFunc("GET /api/sessions", s.handleListSessions)
	mux.HandleFunc("POST /api/sessions", s.handleCreateSession)
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

// handleCreateSession starts a session from {"name": "…"} and returns its
// summary, with the link to share.
func (s *server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		http.Error(w, `send {"name": "…"}`, http.StatusBadRequest)
		return
	}
	id, err := s.Sessions.Create(r.Context(), req.Name)
	if errors.Is(err, session.ErrBadName) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err != nil {
		slog.Error("create session", "err", err)
		http.Error(w, "could not create the session", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, SessionSummary{ID: id, Name: strings.TrimSpace(req.Name), Link: "/s/" + id})
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
