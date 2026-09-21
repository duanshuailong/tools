package toolpkgs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultGroupsNonEmpty(t *testing.T) {
	groups := DefaultGroups()
	if len(groups) == 0 {
		t.Fatal("no default groups")
	}
	seen := map[string]bool{}
	for _, g := range groups {
		if g.Name == "" || len(g.Packages) == 0 {
			t.Errorf("group %+v missing name or packages", g)
		}
		if seen[g.Name] {
			t.Errorf("duplicate group name %q", g.Name)
		}
		seen[g.Name] = true
	}
}

func TestGroupByName(t *testing.T) {
	if _, ok := GroupByName("base"); !ok {
		t.Error("expected 'base' group to exist")
	}
	if _, ok := GroupByName("nonexistent"); ok {
		t.Error("expected unknown group to not exist")
	}
}

func TestCountDebs(t *testing.T) {
	dir := t.TempDir()
	grpDir := filepath.Join(dir, "base")
	if err := os.MkdirAll(grpDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"a.deb", "b.deb", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(grpDir, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := countDebs(dir, "base"); got != 2 {
		t.Errorf("countDebs = %d, want 2", got)
	}
	if got := countDebs(dir, "missing"); got != 0 {
		t.Errorf("countDebs(missing) = %d, want 0", got)
	}
}

func TestManagerStatusesIncludesAllGroups(t *testing.T) {
	m := NewManager(t.TempDir())
	sts := m.Statuses()
	if len(sts) != len(DefaultGroups()) {
		t.Errorf("statuses = %d, want %d", len(sts), len(DefaultGroups()))
	}
	for _, s := range sts {
		if s.State != StateIdle {
			t.Errorf("group %s should start idle, got %s", s.Group, s.State)
		}
	}
}

func TestManagerUnknownGroup(t *testing.T) {
	m := NewManager(t.TempDir())
	if err := m.Start("nope"); err == nil {
		t.Error("expected error starting unknown group")
	}
}
