package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rec := Record{
		ID:            "20260920-120000-1",
		IncludeNVIDIA: true,
		IncludeOFED:   false,
		Status:        "done",
		ISOPath:       "/opt/isofactory/template/new-ubuntu.iso",
		Queued:        "2026-09-20 12:00:00",
		Finished:      "2026-09-20 12:30:00",
	}
	if err := s.SaveRecord(rec); err != nil {
		t.Fatalf("SaveRecord: %v", err)
	}

	got, err := s.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 record, got %d", len(got))
	}
	if got[0] != rec {
		t.Errorf("round trip mismatch:\n got %+v\nwant %+v", got[0], rec)
	}
}

func TestLoadAllNewestFirst(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(dir)
	// IDs are timestamp-prefixed; later ID must sort first.
	for _, id := range []string{"20260920-120000-1", "20260920-130000-2", "20260920-110000-0"} {
		if err := s.SaveRecord(Record{ID: id, Status: "done"}); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := s.LoadAll()
	if len(got) != 3 {
		t.Fatalf("want 3, got %d", len(got))
	}
	if got[0].ID != "20260920-130000-2" || got[2].ID != "20260920-110000-0" {
		t.Errorf("not sorted newest-first: %s .. %s", got[0].ID, got[2].ID)
	}
}

func TestStoreISOMovesToPerJobPath(t *testing.T) {
	base := t.TempDir()
	s, err := New(base)
	if err != nil {
		t.Fatal(err)
	}
	// A fake produced ISO at the fixed pack.sh output location.
	src := filepath.Join(base, "new-ubuntu.iso")
	if err := os.WriteFile(src, []byte("iso-A"), 0o644); err != nil {
		t.Fatal(err)
	}

	dest, err := s.StoreISO("job-A", "ubuntu24.04.4.iso", src)
	if err != nil {
		t.Fatalf("StoreISO: %v", err)
	}
	if dest != s.ISOPathFor("job-A", "ubuntu24.04.4.iso") {
		t.Errorf("dest = %q, want %q", dest, s.ISOPathFor("job-A", "ubuntu24.04.4.iso"))
	}
	// The stored file carries the meaningful output name, not the job id.
	if filepath.Base(dest) != "ubuntu24.04.4.iso" {
		t.Errorf("stored filename = %q, want ubuntu24.04.4.iso", filepath.Base(dest))
	}
	// Source is consumed, dest holds the content.
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("source ISO should be moved away")
	}
	got, _ := os.ReadFile(dest)
	if string(got) != "iso-A" {
		t.Errorf("dest content = %q", got)
	}

	// A second job choosing the SAME output name must not overwrite job-A
	// (per-job subdir keeps them separate).
	if err := os.WriteFile(src, []byte("iso-B"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StoreISO("job-B", "ubuntu24.04.4.iso", src); err != nil {
		t.Fatal(err)
	}
	a, _ := os.ReadFile(s.ISOPathFor("job-A", "ubuntu24.04.4.iso"))
	b, _ := os.ReadFile(s.ISOPathFor("job-B", "ubuntu24.04.4.iso"))
	if string(a) != "iso-A" || string(b) != "iso-B" {
		t.Errorf("per-job isolation broken: A=%q B=%q", a, b)
	}
}

func TestAppendReadLog(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(dir)
	id := "job-1"
	if err := s.AppendLog(id, "line1\n"); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendLog(id, "line2\n"); err != nil {
		t.Fatal(err)
	}
	if got, want := s.ReadLog(id), "line1\nline2\n"; got != want {
		t.Errorf("ReadLog = %q, want %q", got, want)
	}
	if got := s.ReadLog("nonexistent"); got != "" {
		t.Errorf("ReadLog(missing) = %q, want empty", got)
	}
}
