package fetcher

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func testServer(body []byte) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
}

func TestFetchVerifiesChecksum(t *testing.T) {
	body := []byte("fake iso content")
	srv := testServer(body)
	defer srv.Close()

	dir := t.TempDir()
	f := New()
	src := Source{Name: "test.iso", URL: srv.URL, SHA256: sha256Hex(body)}

	path, err := f.Fetch(src, dir, nil)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if path != filepath.Join(dir, "test.iso") {
		t.Errorf("path = %q", path)
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(body) {
		t.Errorf("content mismatch: %q", got)
	}
	// No leftover .part file.
	if _, err := os.Stat(path + ".part"); !os.IsNotExist(err) {
		t.Error(".part file should not remain")
	}
}

func TestFetchChecksumMismatch(t *testing.T) {
	srv := testServer([]byte("actual content"))
	defer srv.Close()

	dir := t.TempDir()
	src := Source{Name: "bad.iso", URL: srv.URL, SHA256: sha256Hex([]byte("expected different"))}

	_, err := New().Fetch(src, dir, nil)
	if err == nil {
		t.Fatal("want checksum mismatch error, got nil")
	}
	// Failed download must not leave a usable file.
	if _, err := os.Stat(filepath.Join(dir, "bad.iso")); !os.IsNotExist(err) {
		t.Error("mismatched download should not produce final file")
	}
}

func TestFetchReusesVerifiedFile(t *testing.T) {
	body := []byte("cached iso")
	dir := t.TempDir()
	// Pre-place a correct file.
	if err := os.WriteFile(filepath.Join(dir, "c.iso"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	// Server that would fail the test if hit.
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	src := Source{Name: "c.iso", URL: srv.URL, SHA256: sha256Hex(body)}
	if _, err := New().Fetch(src, dir, nil); err != nil {
		t.Fatalf("Fetch reuse: %v", err)
	}
	if hit {
		t.Error("server was contacted despite valid cached file")
	}
}

func TestFetchFallsBackToMirror(t *testing.T) {
	body := []byte("mirror content")
	// Primary always fails; mirror serves the real body.
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer primary.Close()
	mirror := testServer(body)
	defer mirror.Close()

	dir := t.TempDir()
	src := Source{
		Name:    "m.iso",
		URL:     primary.URL,
		Mirrors: []string{mirror.URL},
		SHA256:  sha256Hex(body),
	}
	path, err := New().Fetch(src, dir, nil)
	if err != nil {
		t.Fatalf("Fetch should succeed via mirror: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(body) {
		t.Errorf("content = %q, want %q", got, body)
	}
}

func TestFetchAllSourcesFail(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer down.Close()

	src := Source{Name: "x.iso", URL: down.URL, Mirrors: []string{down.URL}}
	_, err := New().Fetch(src, t.TempDir(), nil)
	if err == nil {
		t.Fatal("want error when all sources fail")
	}
}

func TestFetchProgressCallback(t *testing.T) {
	body := make([]byte, 4096)
	srv := testServer(body)
	defer srv.Close()

	dir := t.TempDir()
	var lastDownloaded int64
	src := Source{Name: "p.iso", URL: srv.URL} // no checksum
	_, err := New().Fetch(src, dir, func(p Progress) { lastDownloaded = p.Downloaded })
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	// Callback throttled to 500ms; on a tiny fast download it may fire 0 times,
	// so we only assert it never reported more than the total.
	if lastDownloaded > int64(len(body)) {
		t.Errorf("progress %d exceeds body %d", lastDownloaded, len(body))
	}
}
