package builder

import (
	"path/filepath"
	"strings"
	"testing"

	"isofactory/internal/assets"
)

// newBuilder makes a Builder over a temp template dir with a fresh asset store.
func newBuilder(t *testing.T) *Builder {
	t.Helper()
	tmpl := t.TempDir()
	mustWrite(t, filepath.Join(tmpl, "pack.sh"), "#!/bin/bash\n")
	store, err := assets.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return New(tmpl, store)
}

func TestPreflightMissingScript(t *testing.T) {
	store, _ := assets.New(t.TempDir())
	b := New(t.TempDir(), store) // no pack.sh
	err := b.Preflight()
	if err == nil || !strings.Contains(err.Error(), "packing script not found") {
		t.Errorf("want missing-script error, got %v", err)
	}
}

func TestPreflightOK(t *testing.T) {
	if err := newBuilder(t).Preflight(); err != nil {
		t.Errorf("Preflight should pass with pack.sh + store, got %v", err)
	}
}

func TestResolveBaseDownloadable(t *testing.T) {
	// Default OS/version (ubuntu 24.04.4) has a catalog source; with an empty
	// store it resolves as a downloadable missing base image.
	r, err := newBuilder(t).Resolve(assets.Selection{})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Missing) != 1 || r.Missing[0].Kind != "base" || !r.Missing[0].Downloadable {
		t.Errorf("expected downloadable base missing, got %+v", r.Missing)
	}
}
