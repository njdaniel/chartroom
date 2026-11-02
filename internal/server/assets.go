package server

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
)

// MaxAssetBytes is the largest image upload accepted (ADR-003).
const MaxAssetBytes = 20 << 20

// assetTypes are the image types accepted, as sniffed from the bytes. SVG
// is left out on purpose: it can carry script.
var assetTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/webp": true}

var assetHash = regexp.MustCompile(`^[0-9a-f]{64}$`)

// handleUpload stores the request body as an image named by its sha256. The
// type is checked from the bytes, never from a name or header. Uploading the
// same image again returns the same hash and keeps one file.
func (s *server) handleUpload(w http.ResponseWriter, r *http.Request) {
	body := http.MaxBytesReader(w, r.Body, MaxAssetBytes)
	tmp, err := os.CreateTemp(s.AssetsDir, ".upload-*")
	if err != nil {
		slog.Error("asset upload: temp file", "err", err)
		http.Error(w, "could not store the upload", http.StatusInternalServerError)
		return
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name()) // no-op once renamed into place
	}()

	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), body)
	if err != nil {
		if errors.As(err, new(*http.MaxBytesError)) {
			http.Error(w, "images can be at most 20 MB", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "could not read the upload", http.StatusBadRequest)
		return
	}
	if n == 0 {
		http.Error(w, "empty upload", http.StatusBadRequest)
		return
	}

	head := make([]byte, 512)
	m, _ := tmp.ReadAt(head, 0)
	ctype := http.DetectContentType(head[:m])
	if !assetTypes[ctype] {
		http.Error(w, "images must be PNG, JPEG, or WebP", http.StatusUnsupportedMediaType)
		return
	}

	hash := hex.EncodeToString(h.Sum(nil))
	final := filepath.Join(s.AssetsDir, hash)
	if err := tmp.Close(); err != nil {
		slog.Error("asset upload: close", "err", err)
		http.Error(w, "could not store the upload", http.StatusInternalServerError)
		return
	}
	if _, err := os.Stat(final); errors.Is(err, os.ErrNotExist) {
		if err := os.Rename(tmp.Name(), final); err != nil {
			slog.Error("asset upload: rename", "err", err)
			http.Error(w, "could not store the upload", http.StatusInternalServerError)
			return
		}
	}
	writeJSON(w, http.StatusCreated, map[string]string{"asset": hash, "type": ctype})
}

// handleAsset serves an uploaded image. A hash names exactly one content, so
// the response can be cached forever.
func (s *server) handleAsset(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash")
	if !assetHash.MatchString(hash) {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(filepath.Join(s.AssetsDir, hash))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}

	head := make([]byte, 512)
	m, _ := f.ReadAt(head, 0)
	w.Header().Set("Content-Type", http.DetectContentType(head[:m]))
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "", info.ModTime(), f)
}
