package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"isofactory/internal/assets"
	"isofactory/internal/builder"
	"isofactory/internal/progress"
	"isofactory/internal/queue"
	"isofactory/internal/storage"
)

// fakeRunner produces a small fake ISO file so download paths can be exercised
// without invoking pack.sh/xorriso.
type fakeRunner struct {
	dir string // where to write the fake produced ISO
	err error
}

func (f *fakeRunner) Run(_ context.Context, _ assets.Selection, logOut io.Writer, rep progress.Reporter) (string, func(), error) {
	noop := func() {}
	if f.err != nil {
		return "", noop, f.err
	}
	_, _ = io.WriteString(logOut, "fake build\n")
	rep.Report(progress.Update{Phase: progress.PhaseBuilding, Percent: 50})
	out := filepath.Join(f.dir, "new-ubuntu.iso")
	if err := os.WriteFile(out, []byte("ISO-BYTES"), 0o644); err != nil {
		return "", noop, err
	}
	return out, noop, nil
}

// newTestServer wires a Server with a real queue (fake runner) + store and a
// builder pointed at a temp template dir. The template has pack.sh; the asset
// store has a local base ISO so the default selection resolves cleanly.
func newTestServer(t *testing.T) (*httptest.Server, *storage.Store) {
	t.Helper()
	tmpl := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpl, "pack.sh"), []byte("#!/bin/bash\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	store, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	q := queue.New(&fakeRunner{dir: tmpl}, store, 1)

	assetRoot := t.TempDir()
	baseDir := filepath.Join(assetRoot, "base", "ubuntu", "24.04.4")
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(baseDir, "ubuntu-24.04.4-live-server-amd64.iso"), []byte("iso"), 0o644); err != nil {
		t.Fatal(err)
	}
	astore, err := assets.New(assetRoot)
	if err != nil {
		t.Fatal(err)
	}

	b := builder.New(tmpl, astore)
	srv := &Server{Q: q, B: b}
	return httptest.NewServer(srv.Routes()), store
}

func getJSON(t *testing.T, url string, into any) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if into != nil {
		_ = json.NewDecoder(resp.Body).Decode(into)
	}
	return resp.StatusCode
}

func TestHealth(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()
	var body map[string]string
	if code := getJSON(t, ts.URL+"/api/health", &body); code != 200 || body["status"] != "ok" {
		t.Errorf("health: code=%d body=%v", code, body)
	}
}

func TestPreflightReady(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()
	var body map[string]any
	getJSON(t, ts.URL+"/api/preflight", &body)
	if body["ready"] != true {
		t.Errorf("preflight not ready: %v", body)
	}
}

func TestDefaultNameEndpoint(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()
	var body map[string]any
	getJSON(t, ts.URL+"/api/default-name?include_nvidia=false&include_ofed=false", &body)
	// System version resolves from the local ISO name.
	if got, _ := body["default_name"].(string); !strings.HasPrefix(got, "ubuntu24.04.4") {
		t.Errorf("default_name = %v", body["default_name"])
	}
}

func TestBuildAutoNameThenDownload(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()

	// Submit with no name → auto.
	resp, err := http.Post(ts.URL+"/api/build", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var job map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&job)
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("build status %d", resp.StatusCode)
	}
	id, _ := job["id"].(string)
	if id == "" {
		t.Fatal("no job id returned")
	}
	if name, _ := job["output_name"].(string); !strings.HasPrefix(name, "ubuntu24.04.4") {
		t.Errorf("auto output_name = %v", job["output_name"])
	}

	// Poll until done.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var j map[string]any
		getJSON(t, ts.URL+"/api/jobs/"+id, &j)
		if j["status"] == "done" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Download should serve the fake ISO under the auto name.
	dl, err := http.Get(ts.URL + "/api/jobs/" + id + "/download")
	if err != nil {
		t.Fatal(err)
	}
	defer dl.Body.Close()
	if dl.StatusCode != 200 {
		t.Fatalf("download status %d", dl.StatusCode)
	}
	cd := dl.Header.Get("Content-Disposition")
	if !strings.Contains(cd, "ubuntu24.04.4") {
		t.Errorf("Content-Disposition = %q", cd)
	}
	data, _ := io.ReadAll(dl.Body)
	if string(data) != "ISO-BYTES" {
		t.Errorf("download body = %q", data)
	}
}

func TestBuildCustomName(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()
	resp, err := http.Post(ts.URL+"/api/build", "application/json", strings.NewReader(`{"name":"../my build"}`))
	if err != nil {
		t.Fatal(err)
	}
	var job map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&job)
	resp.Body.Close()
	// Sanitized: no path parts, .iso appended.
	if got, _ := job["output_name"].(string); got != "my build.iso" {
		t.Errorf("custom output_name = %v, want 'my build.iso'", job["output_name"])
	}
	// Wait for the async build to finish so it stops writing to the temp dir
	// before t.TempDir() cleanup runs.
	waitJobDone(t, ts.URL, job["id"].(string))
}

// waitJobDone polls until the job reaches a terminal state.
func waitJobDone(t *testing.T, base, id string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var j map[string]any
		getJSON(t, base+"/api/jobs/"+id, &j)
		if s := j["status"]; s == "done" || s == "failed" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("job did not finish before deadline")
}

func TestDownloadNotReady(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()
	// Nonexistent job → 404.
	if code := getJSON(t, ts.URL+"/api/jobs/nope/download", nil); code != http.StatusNotFound {
		t.Errorf("download missing job: code=%d, want 404", code)
	}
}

func TestJobNotFound(t *testing.T) {
	ts, _ := newTestServer(t)
	defer ts.Close()
	if code := getJSON(t, ts.URL+"/api/jobs/nope", nil); code != http.StatusNotFound {
		t.Errorf("job lookup: code=%d, want 404", code)
	}
}
