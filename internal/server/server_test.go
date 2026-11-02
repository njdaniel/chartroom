package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/njdaniel/chartroom/internal/eventlog"
	"github.com/njdaniel/chartroom/internal/store"
	"github.com/njdaniel/chartroom/internal/table"
)

type fixture struct {
	url    string
	log    *eventlog.Log
	assets string
}

func newFixture(t *testing.T, client fs.FS) fixture {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "chartroom.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	assets := filepath.Join(dir, "assets")
	if err := os.Mkdir(assets, 0o700); err != nil {
		t.Fatal(err)
	}

	reg := eventlog.NewRegistry()
	table.Register(reg)
	log := eventlog.New(db, reg)
	srv := httptest.NewServer(New(Config{Log: log, AssetsDir: assets, Client: client}))
	t.Cleanup(srv.Close)
	return fixture{url: srv.URL, log: log, assets: assets}
}

func get(t *testing.T, url string) (*http.Response, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func upload(t *testing.T, url string, body io.Reader) (*http.Response, string) {
	t.Helper()
	resp, err := http.Post(url+"/api/assets", "application/octet-stream", body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

// Minimal byte prefixes that content sniffing recognises.
var (
	pngBytes  = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{1}, 64)...)
	jpegBytes = append([]byte("\xff\xd8\xff\xe0"), bytes.Repeat([]byte{2}, 64)...)
	webpBytes = append([]byte("RIFF\x00\x00\x00\x00WEBPVP8 "), bytes.Repeat([]byte{3}, 64)...)
)

func TestHealth(t *testing.T) {
	f := newFixture(t, nil)
	resp, body := get(t, f.url+"/healthz")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"ok"`) {
		t.Fatalf("GET /healthz = %d %s", resp.StatusCode, body)
	}
	post, err := http.Post(f.url+"/healthz", "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	post.Body.Close()
	if post.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST /healthz = %d, want 405", post.StatusCode)
	}
}

func TestUploadAndServeImages(t *testing.T) {
	f := newFixture(t, nil)
	for name, img := range map[string][]byte{"png": pngBytes, "jpeg": jpegBytes, "webp": webpBytes} {
		t.Run(name, func(t *testing.T) {
			resp, body := upload(t, f.url, bytes.NewReader(img))
			if resp.StatusCode != http.StatusCreated {
				t.Fatalf("upload = %d %s", resp.StatusCode, body)
			}
			var got struct{ Asset, Type string }
			if err := json.Unmarshal([]byte(body), &got); err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(img)
			if got.Asset != hex.EncodeToString(sum[:]) || got.Type != "image/"+name {
				t.Fatalf("upload returned %+v, want the sha256 and image/%s", got, name)
			}

			resp, served := get(t, f.url+"/assets/"+got.Asset)
			if resp.StatusCode != http.StatusOK || served != string(img) {
				t.Fatalf("GET asset = %d, %d bytes", resp.StatusCode, len(served))
			}
			if ct := resp.Header.Get("Content-Type"); ct != "image/"+name {
				t.Errorf("Content-Type = %q", ct)
			}
			if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
				t.Errorf("Cache-Control = %q, want immutable", cc)
			}
		})
	}
}

func TestUploadRejectsOtherTypes(t *testing.T) {
	f := newFixture(t, nil)
	for name, body := range map[string]string{
		"gif":         "GIF89a" + strings.Repeat("x", 64),
		"svg":         `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`,
		"html":        "<!doctype html><html><script>alert(1)</script></html>",
		"text":        "just some text",
		"png renamed": "this is not a png but the client says it is",
	} {
		t.Run(name, func(t *testing.T) {
			resp, _ := upload(t, f.url, strings.NewReader(body))
			if resp.StatusCode != http.StatusUnsupportedMediaType {
				t.Fatalf("upload = %d, want 415", resp.StatusCode)
			}
		})
	}
	if resp, _ := upload(t, f.url, strings.NewReader("")); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("empty upload = %d, want 400", resp.StatusCode)
	}
	assertFiles(t, f.assets, 0)
}

func TestUploadTooLarge(t *testing.T) {
	f := newFixture(t, nil)
	big := io.MultiReader(bytes.NewReader(pngBytes), bytes.NewReader(make([]byte, MaxAssetBytes)))
	resp, _ := upload(t, f.url, big)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("upload = %d, want 413", resp.StatusCode)
	}
	assertFiles(t, f.assets, 0)

	// Exactly the limit is fine.
	limit := append(append([]byte{}, pngBytes...), make([]byte, MaxAssetBytes-len(pngBytes))...)
	if resp, body := upload(t, f.url, bytes.NewReader(limit)); resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload at the limit = %d %s", resp.StatusCode, body)
	}
}

func TestUploadSameImageTwiceKeepsOneFile(t *testing.T) {
	f := newFixture(t, nil)
	_, first := upload(t, f.url, bytes.NewReader(pngBytes))
	_, second := upload(t, f.url, bytes.NewReader(pngBytes))
	if first != second {
		t.Fatalf("uploads returned %s and %s, want the same", first, second)
	}
	assertFiles(t, f.assets, 1)
}

func TestAssetNotFound(t *testing.T) {
	f := newFixture(t, nil)
	for _, path := range []string{
		"/assets/" + strings.Repeat("0", 64), // well-formed, never uploaded
		"/assets/not-a-hash",
		"/assets/" + strings.Repeat("A", 64), // uppercase is not how hashes are named
		"/assets/..%2fchartroom.db",
	} {
		if resp, _ := get(t, f.url+path); resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, resp.StatusCode)
		}
	}
}

func TestListSessionsHidesTheSeed(t *testing.T) {
	f := newFixture(t, nil)
	ctx := context.Background()
	for _, name := range []string{"Friday game", "Little Wars"} {
		id := "s_" + strings.ReplaceAll(strings.ToLower(name), " ", "_")
		if _, err := f.log.Append(ctx, id, "", "", eventlog.Draft{
			Type: table.TypeSessionCreated, Data: table.SessionCreated{Name: name, DiceSeed: strings.Repeat("ab", 32)},
		}); err != nil {
			t.Fatal(err)
		}
	}

	resp, body := get(t, f.url+"/api/sessions")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/sessions = %d", resp.StatusCode)
	}
	if strings.Contains(body, "abab") || strings.Contains(body, "seed") {
		t.Fatalf("session list leaks the dice seed: %s", body)
	}
	var got []SessionSummary
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "Friday game" || got[0].Link != "/s/s_friday_game" {
		t.Fatalf("sessions = %+v", got)
	}
}

func TestListSessionsEmpty(t *testing.T) {
	f := newFixture(t, nil)
	if _, body := get(t, f.url+"/api/sessions"); strings.TrimSpace(body) != "[]" {
		t.Fatalf("empty list = %s, want []", body)
	}
}

func TestClient(t *testing.T) {
	client := fstest.MapFS{
		"index.html":       {Data: []byte("<!doctype html>index")},
		"static/app-1.js":  {Data: []byte("console.log(1)")},
		"static/nested/ok": {Data: []byte("nested")},
	}
	f := newFixture(t, client)

	for path, want := range map[string]string{
		"/":                 "<!doctype html>index",
		"/s/s_123":          "<!doctype html>index", // client-side route
		"/static/app-1.js":  "console.log(1)",
		"/static/":          "<!doctype html>index", // directories are not listed
		"/static/nested/ok": "nested",
		"/no/such/file.js":  "<!doctype html>index",
	} {
		resp, body := get(t, f.url+path)
		if resp.StatusCode != http.StatusOK || body != want {
			t.Errorf("GET %s = %d %q, want %q", path, resp.StatusCode, body, want)
		}
	}

	resp, _ := get(t, f.url+"/")
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("index Cache-Control = %q, want no-cache", cc)
	}
	resp, _ = get(t, f.url+"/static/app-1.js")
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("static Cache-Control = %q, want immutable", cc)
	}

	// Unknown API paths are 404s, not the client.
	if resp, _ := get(t, f.url+"/api/nope"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /api/nope = %d, want 404", resp.StatusCode)
	}
}

func TestNoClient(t *testing.T) {
	f := newFixture(t, nil)
	resp, body := get(t, f.url+"/")
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(body, "make build") {
		t.Fatalf("GET / without a client = %d %q", resp.StatusCode, body)
	}
}

func assertFiles(t *testing.T, dir string, want int) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != want {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Fatalf("assets dir holds %v, want %d files", names, want)
	}
}
