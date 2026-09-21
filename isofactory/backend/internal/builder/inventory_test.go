package builder

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInventoryScansTemplate(t *testing.T) {
	dir := t.TempDir()

	// source ISO
	mustWrite(t, filepath.Join(dir, "ubuntu-24.04.4-live-server-amd64.iso"), "iso")
	// debs/base with two .deb and a package file
	mustMkdir(t, filepath.Join(dir, "debs", "base"))
	mustWrite(t, filepath.Join(dir, "debs", "base", "a.deb"), "x")
	mustWrite(t, filepath.Join(dir, "debs", "base", "b.deb"), "x")
	mustWrite(t, filepath.Join(dir, "debs", "base", "package"), "net-tools dnsutils")
	// debs/doca with no package file
	mustMkdir(t, filepath.Join(dir, "debs", "doca"))
	mustWrite(t, filepath.Join(dir, "debs", "doca", "doca.deb"), "x")
	// offline index present
	mustWrite(t, filepath.Join(dir, "debs", "Packages"), "")
	// drivers dir with one file and one subdir (subdir must be ignored)
	mustMkdir(t, filepath.Join(dir, "drivers"))
	mustWrite(t, filepath.Join(dir, "drivers", "cuda_13.2.2_595.71.05_linux.run"), "run")
	mustMkdir(t, filepath.Join(dir, "drivers", "nvidia"))

	inv := (&Builder{TemplateDir: dir}).Inventory()

	if inv.SourceISO != "ubuntu-24.04.4-live-server-amd64.iso" {
		t.Errorf("SourceISO = %q", inv.SourceISO)
	}
	if !inv.HasIndex {
		t.Error("HasIndex = false, want true (Packages present)")
	}
	if len(inv.DebGroups) != 2 {
		t.Fatalf("want 2 deb groups, got %d", len(inv.DebGroups))
	}
	// sorted: base, doca
	base := inv.DebGroups[0]
	if base.Name != "base" || base.DebCount != 2 || base.Packages != "net-tools dnsutils" {
		t.Errorf("base group wrong: %+v", base)
	}
	if inv.DebGroups[1].Name != "doca" || inv.DebGroups[1].DebCount != 1 {
		t.Errorf("doca group wrong: %+v", inv.DebGroups[1])
	}
	if len(inv.Drivers) != 1 || inv.Drivers[0].Name != "cuda_13.2.2_595.71.05_linux.run" {
		t.Errorf("drivers wrong (subdir should be ignored): %+v", inv.Drivers)
	}
}

func TestInventoryEmptyTemplate(t *testing.T) {
	inv := (&Builder{TemplateDir: t.TempDir()}).Inventory()
	if inv.SourceISO != "" || len(inv.DebGroups) != 0 || len(inv.Drivers) != 0 || inv.HasIndex {
		t.Errorf("empty template should yield empty inventory, got %+v", inv)
	}
}

func TestHumanSize(t *testing.T) {
	cases := map[int64]string{
		0:          "0 B",
		512:        "512 B",
		1024:       "1.0 KiB",
		1536:       "1.5 KiB",
		1073741824: "1.0 GiB",
	}
	for in, want := range cases {
		if got := humanSize(in); got != want {
			t.Errorf("humanSize(%d) = %q, want %q", in, got, want)
		}
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}
