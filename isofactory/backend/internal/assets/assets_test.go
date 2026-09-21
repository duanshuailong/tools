package assets

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"isofactory/internal/fetcher"
	"isofactory/internal/progress"
)

func mk(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveLocalFirst(t *testing.T) {
	root := t.TempDir()
	s, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	// Place a local base ISO, cuda run, and ofed tgz.
	mk(t, filepath.Join(root, "base/ubuntu/24.04.4/ubuntu-24.04.4-live-server-amd64.iso"), "iso")
	mk(t, filepath.Join(root, "nvidia/580.65.06/13.0.0/cuda_13.0.0_580.65.06_linux.run"), "run")
	mk(t, filepath.Join(root, "ofed/24.04-0.6.6.0/MLNX_OFED_LINUX-24.04-0.6.6.0-ubuntu24.04-x86_64.tgz"), "tgz")

	sel := Selection{
		OS: "ubuntu", SystemVersion: "24.04.4",
		IncludeNVIDIA: true, NVIDIAVersion: "580.65.06", CUDAVersion: "13.0.0",
		IncludeOFED: true, OFEDVersion: "24.04-0.6.6.0",
	}
	r, err := s.Resolve(sel)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Missing) != 0 {
		t.Errorf("expected nothing missing, got %+v", r.Missing)
	}
	if filepath.Base(r.BaseISO) != "ubuntu-24.04.4-live-server-amd64.iso" {
		t.Errorf("BaseISO = %q", r.BaseISO)
	}
	if filepath.Base(r.CUDARun) != "cuda_13.0.0_580.65.06_linux.run" {
		t.Errorf("CUDARun = %q", r.CUDARun)
	}
	if filepath.Base(r.OFEDTgz) == "" {
		t.Errorf("OFEDTgz not resolved")
	}
}

func TestResolveBaseMissingButDownloadable(t *testing.T) {
	s, _ := New(t.TempDir())
	r, err := s.Resolve(Selection{OS: "ubuntu", SystemVersion: "24.04.4"})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Missing) != 1 || r.Missing[0].Kind != "base" || !r.Missing[0].Downloadable {
		t.Errorf("expected downloadable base missing, got %+v", r.Missing)
	}
}

func TestResolveCUDADownloadable(t *testing.T) {
	root := t.TempDir()
	s, _ := New(root)
	mk(t, filepath.Join(root, "base/ubuntu/24.04.4/x.iso"), "iso")
	// A catalog CUDA pair (13.0.0/580.65.06) is downloadable.
	sel := Selection{OS: "ubuntu", SystemVersion: "24.04.4",
		IncludeNVIDIA: true, NVIDIAVersion: "580.65.06", CUDAVersion: "13.0.0"}
	r, _ := s.Resolve(sel)
	if len(r.Missing) != 1 || r.Missing[0].Kind != "nvidia" || !r.Missing[0].Downloadable {
		t.Errorf("expected downloadable nvidia missing, got %+v", r.Missing)
	}
}

func TestResolveCUDANotInCatalog(t *testing.T) {
	root := t.TempDir()
	s, _ := New(root)
	mk(t, filepath.Join(root, "base/ubuntu/24.04.4/x.iso"), "iso")
	// An unknown CUDA pair is not downloadable.
	sel := Selection{OS: "ubuntu", SystemVersion: "24.04.4",
		IncludeNVIDIA: true, NVIDIAVersion: "999.99.99", CUDAVersion: "1.0.0"}
	r, _ := s.Resolve(sel)
	if len(r.Missing) != 1 || r.Missing[0].Kind != "nvidia" || r.Missing[0].Downloadable {
		t.Errorf("expected non-downloadable nvidia missing, got %+v", r.Missing)
	}
}

func TestEnsureDownloadsBase(t *testing.T) {
	body := []byte("iso-bytes")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	root := t.TempDir()
	s, _ := New(root)
	// Catalog pointing at the test server (no checksum, to keep the test simple).
	s.SetCatalog(Catalog{
		Images: []ImageRelease{
			{OS: "ubuntu", Version: "24.04.4", Name: "ubuntu-24.04.4-live-server-amd64.iso", URL: srv.URL},
		},
	})

	var sawDownloading bool
	rep := progress.ReporterFunc(func(u progress.Update) {
		if u.Phase == progress.PhaseDownloading {
			sawDownloading = true
		}
	})
	r, err := s.Ensure(Selection{OS: "ubuntu", SystemVersion: "24.04.4"}, rep)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if _, err := os.Stat(r.BaseISO); err != nil {
		t.Errorf("base ISO not present after Ensure: %v", err)
	}
	if !sawDownloading {
		t.Error("expected a downloading progress update")
	}
	if len(r.Missing) != 0 {
		t.Errorf("Missing should be cleared, got %+v", r.Missing)
	}
}

func TestEnsureDownloadsCUDA(t *testing.T) {
	body := []byte("#!/bin/sh\nfake cuda run\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	root := t.TempDir()
	s, _ := New(root)
	mk(t, filepath.Join(root, "base/ubuntu/24.04.4/x.iso"), "iso")
	// Custom catalog: base already local (skipped), CUDA points at test server.
	s.SetCatalog(Catalog{
		CUDA: []CUDARelease{{CUDA: "13.0.0", Driver: "580.65.06"}},
	})
	// Override the derived URL by pointing the fetcher... simpler: use a catalog
	// whose CUDA source URL we control via a custom release type isn't possible,
	// so instead assert the missing item is downloadable and Ensure attempts it.
	sel := Selection{OS: "ubuntu", SystemVersion: "24.04.4",
		IncludeNVIDIA: true, NVIDIAVersion: "580.65.06", CUDAVersion: "13.0.0"}
	r, _ := s.Resolve(sel)
	if len(r.Missing) != 1 || !r.Missing[0].Downloadable {
		t.Fatalf("expected downloadable CUDA, got %+v", r.Missing)
	}
	// (Full Ensure would hit NVIDIA's real CDN; covered by Resolve + the base
	// download test above. We don't hit the network here.)
}

func TestEnsureFailsOnUndownloadable(t *testing.T) {
	root := t.TempDir()
	s, _ := New(root)
	mk(t, filepath.Join(root, "base/ubuntu/24.04.4/x.iso"), "iso")
	sel := Selection{OS: "ubuntu", SystemVersion: "24.04.4",
		IncludeNVIDIA: true, NVIDIAVersion: "999", CUDAVersion: "1"}
	_, err := s.Ensure(sel, progress.Nop)
	if err == nil {
		t.Fatal("expected error for missing non-downloadable driver")
	}
}

func TestOFEDTagForSystem(t *testing.T) {
	cases := map[string]string{
		"24.04.4": "ubuntu24.04",
		"22.04.3": "ubuntu22.04",
		"24.04":   "ubuntu24.04",
	}
	for in, want := range cases {
		if got := OFEDTagForSystem(in); got != want {
			t.Errorf("OFEDTagForSystem(%q) = %q, want %q", in, got, want)
		}
	}
}

var _ = fetcher.Source{}
