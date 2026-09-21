package builder

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Inventory describes what would go into the ISO: the source image, offline
// deb groups, and driver files. It reflects the current contents of the
// template directory — the same inputs README.md documents.
type Inventory struct {
	SourceISO string      `json:"source_iso"` // filename of the ubuntu*.iso, or "" if absent
	DebGroups []DebGroup  `json:"deb_groups"` // one per debs/<subdir>
	Drivers   []FileEntry `json:"drivers"`    // files in drivers/
	HasIndex  bool        `json:"has_index"`  // debs/Packages(.gz) present
}

// DebGroup is one debs/<subdir>: its name, the top-level package list (from the
// optional `package` file), and how many .deb files it holds.
type DebGroup struct {
	Name     string `json:"name"`
	Packages string `json:"packages,omitempty"` // contents of the `package` file, if any
	DebCount int    `json:"deb_count"`
}

// FileEntry is a file name plus human-readable size.
type FileEntry struct {
	Name string `json:"name"`
	Size string `json:"size"`
}

// Inventory scans the template directory and reports its build inputs. Missing
// subdirectories are simply reported as empty rather than as errors.
func (b *Builder) Inventory() Inventory {
	inv := Inventory{}

	if matches, _ := filepath.Glob(filepath.Join(b.TemplateDir, "ubuntu*.iso")); len(matches) > 0 {
		inv.SourceISO = filepath.Base(matches[0])
	}

	debsDir := filepath.Join(b.TemplateDir, "debs")
	if entries, err := os.ReadDir(debsDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				inv.DebGroups = append(inv.DebGroups, readDebGroup(filepath.Join(debsDir, e.Name()), e.Name()))
			}
			if e.Name() == "Packages" || e.Name() == "Packages.gz" {
				inv.HasIndex = true
			}
		}
		sort.Slice(inv.DebGroups, func(i, j int) bool { return inv.DebGroups[i].Name < inv.DebGroups[j].Name })
	}

	driversDir := filepath.Join(b.TemplateDir, "drivers")
	if entries, err := os.ReadDir(driversDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue // pack.sh's md5sum breaks on subdirs; surface only files
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			inv.Drivers = append(inv.Drivers, FileEntry{Name: e.Name(), Size: humanSize(info.Size())})
		}
	}

	return inv
}

func readDebGroup(dir, name string) DebGroup {
	g := DebGroup{Name: name}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return g
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".deb") {
			g.DebCount++
		}
	}
	if data, err := os.ReadFile(filepath.Join(dir, "package")); err == nil {
		g.Packages = strings.TrimSpace(string(data))
	}
	return g
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return itoa64(n) + " B"
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	// one decimal place
	whole := n / div
	frac := (n % div) * 10 / div
	return itoa64(whole) + "." + itoa64(frac) + " " + string("KMGTPE"[exp]) + "iB"
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [24]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
