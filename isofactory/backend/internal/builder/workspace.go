package builder

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// copyRecipe copies the build-recipe files from the template into a fresh
// workspace: pack.sh, user-data*, check-packages.sh, and the debs recipe
// scripts/manifests (gen_packages.sh + per-group `package` files). It skips
// large/regenerable content — produced ISOs, the extract dir, materialized
// drivers, downloaded .deb files, and stale apt indexes — so each workspace
// starts clean and only gets the exact assets this build selected.
func copyRecipe(templateDir, ws string) error {
	return filepath.WalkDir(templateDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(templateDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if skipRecipe(rel, d) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		dst := filepath.Join(ws, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		// Regular recipe file — copy contents (small text files).
		return copyFileContents(path, dst)
	})
}

// skipRecipe decides whether a template entry should be excluded from the
// workspace recipe copy.
func skipRecipe(rel string, d os.DirEntry) bool {
	base := filepath.Base(rel)
	// Top-level directories we never copy as recipe.
	if d.IsDir() {
		switch rel {
		case "drivers", "ubuntu-files":
			return true
		}
		if strings.HasPrefix(rel, "ubuntu-files") { // ubuntu-files.old.* etc.
			return true
		}
		return false
	}
	// Files: skip produced ISOs, base-image symlinks, downloaded debs, indexes.
	if strings.HasSuffix(base, ".iso") || strings.HasSuffix(base, ".iso.part") {
		return true
	}
	if strings.HasSuffix(base, ".deb") {
		return true
	}
	switch base {
	case "Packages", "Packages.gz", "md5sum.txt":
		return true
	}
	return false
}

func copyFileContents(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	return out.Close()
}

// hardlinkDebs hardlinks every .deb from cacheDir into dst (falling back to copy
// across filesystems). dst is created if needed.
func hardlinkDebs(cacheDir, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	debs, _ := filepath.Glob(filepath.Join(cacheDir, "*.deb"))
	for _, src := range debs {
		if err := linkOrCopy(src, filepath.Join(dst, filepath.Base(src))); err != nil {
			return err
		}
	}
	return nil
}

// regenIndex regenerates Packages/Packages.gz across debsDir so it works as an
// offline apt source (deb [trusted=yes] file:///cdrom/debs ./).
func regenIndex(debsDir string, log io.Writer) error {
	if _, err := exec.LookPath("dpkg-scanpackages"); err != nil {
		// No apt tooling (e.g. macOS dev host) — skip; the offline index only
		// matters on the Linux build server, which has dpkg-dev.
		fmt.Fprintln(log, "dpkg-scanpackages not found — skipping offline index generation")
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	scan := exec.CommandContext(ctx, "dpkg-scanpackages", "-m", ".", "/dev/null")
	scan.Dir = debsDir
	data, err := scan.Output()
	if err != nil {
		return fmt.Errorf("dpkg-scanpackages: %w", err)
	}
	if err := os.WriteFile(filepath.Join(debsDir, "Packages"), data, 0o644); err != nil {
		return err
	}
	var gz bytes.Buffer
	g := exec.CommandContext(ctx, "gzip", "-9c")
	g.Stdin = bytes.NewReader(data)
	g.Stdout = &gz
	if err := g.Run(); err != nil {
		return fmt.Errorf("gzip Packages: %w", err)
	}
	fmt.Fprintf(log, "offline apt index regenerated (%d bytes)\n", len(data))
	return os.WriteFile(filepath.Join(debsDir, "Packages.gz"), gz.Bytes(), 0o644)
}
