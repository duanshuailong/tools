package nvapt

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Selection is an independent driver + toolkit choice.
type Selection struct {
	Driver  string // cuda-drivers version, e.g. "580.65.06-1"; "" = skip driver
	Toolkit string // cuda-toolkit package, e.g. "cuda-toolkit-13-2"; "" = skip toolkit
}

// DownloadTo resolves the dependency closure for the selected driver + toolkit —
// PLUS the aligned datacenter driver stack (fabricmanager, nscq, DCGM,
// container-toolkit) — and downloads all debs into destDir. Downloading the full
// stack (not just the driver body) matters for multi-GPU/NVSwitch servers:
// fabricmanager & nscq must match the driver's three-part version or NVLink
// won't come up (see template/README §5). No index is written here — the builder
// regenerates the offline index once per workspace after assembly. EnsureRepo
// must have run first.
func DownloadTo(ctx context.Context, sel Selection, destDir string, log io.Writer) error {
	if !Available() {
		return fmt.Errorf("apt not available on this host")
	}
	if sel.Driver == "" && sel.Toolkit == "" {
		return fmt.Errorf("nothing selected: choose a driver and/or toolkit")
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}

	// Build the top-level package spec list. The `cuda-drivers-<branch>`
	// metapackage already pins the matching fabricmanager/nscq for that branch,
	// but we add the datacenter stack explicitly so it's guaranteed present.
	var top []string
	if sel.Driver != "" {
		top = append(top, "cuda-drivers="+sel.Driver)
		branch := driverBranch(sel.Driver) // 590.48.01-0ubuntu1 -> 590
		// Aligned stack packages for this branch, added only if they exist in the
		// repo (newer branches like 580+ may not ship fabricmanager/nscq yet).
		for _, p := range []string{
			"nvidia-fabricmanager-" + branch,
			"libnvidia-nscq-" + branch,
		} {
			if packageExists(ctx, p) {
				top = append(top, p)
			} else {
				fmt.Fprintf(log, "note: %s not in repo for this branch — skipped\n", p)
			}
		}
		// Version-independent datacenter tools.
		for _, p := range []string{"datacenter-gpu-manager-4-core", "nvidia-container-toolkit"} {
			if packageExists(ctx, p) {
				top = append(top, p)
			}
		}
	}
	if sel.Toolkit != "" {
		top = append(top, sel.Toolkit)
	}

	fmt.Fprintf(log, "resolving dependency closure for aligned driver stack: %s\n", strings.Join(top, ", "))
	closure, err := resolveClosure(ctx, top)
	if err != nil {
		return err
	}
	fmt.Fprintf(log, "closure has %d packages; downloading into %s\n", len(closure), destDir)

	cmd := exec.CommandContext(ctx, "apt-get", append([]string{"download"}, closure...)...)
	cmd.Dir = destDir
	cmd.Stdout = log
	cmd.Stderr = log
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(log, "warning: apt-get download reported errors (some packages may be unavailable): %v\n", err)
	}
	return nil
}

// driverBranch extracts the major branch from a driver version: 590.48.01-0ubuntu1 -> 590.
func driverBranch(v string) string {
	if i := strings.IndexByte(v, '.'); i >= 0 {
		return v[:i]
	}
	return v
}

// packageExists reports whether apt knows the named package (in any version).
func packageExists(ctx context.Context, pkg string) bool {
	out, err := exec.CommandContext(ctx, "apt-cache", "madison", pkg).Output()
	return err == nil && len(bytes.TrimSpace(out)) > 0
}

// resolveClosure returns the recursive dependency closure (package names) for
// the given top-level specs, dropping virtual packages apt-get download can't
// fetch. Strips any =version suffix for the recurse query.
func resolveClosure(ctx context.Context, specs []string) ([]string, error) {
	names := make([]string, len(specs))
	for i, s := range specs {
		if j := strings.IndexByte(s, '='); j >= 0 {
			names[i] = s[:j]
		} else {
			names[i] = s
		}
	}
	args := append([]string{"depends", "--recurse", "--no-recommends", "--no-suggests",
		"--no-conflicts", "--no-breaks", "--no-replaces", "--no-enhances", "--no-pre-depends"}, names...)
	out, err := exec.CommandContext(ctx, "apt-cache", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("apt-cache depends: %w", err)
	}
	set := map[string]struct{}{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == ' ' {
			continue
		}
		name := strings.TrimSpace(line)
		if strings.HasPrefix(name, "<") {
			continue // virtual package
		}
		if i := strings.IndexByte(name, ':'); i >= 0 {
			name = name[:i]
		}
		if name != "" {
			set[name] = struct{}{}
		}
	}
	out2 := make([]string, 0, len(set))
	for n := range set {
		out2 = append(out2, n)
	}
	sort.Strings(out2)
	return out2, nil
}

// generateIndex regenerates Packages/Packages.gz across debsRoot.
func generateIndex(ctx context.Context, debsRoot string, log io.Writer) error {
	fmt.Fprintln(log, "regenerating offline apt index")
	scan := exec.CommandContext(ctx, "dpkg-scanpackages", "-m", ".", "/dev/null")
	scan.Dir = debsRoot
	data, err := scan.Output()
	if err != nil {
		return fmt.Errorf("dpkg-scanpackages: %w", err)
	}
	if err := os.WriteFile(filepath.Join(debsRoot, "Packages"), data, 0o644); err != nil {
		return err
	}
	var gz bytes.Buffer
	g := exec.CommandContext(ctx, "gzip", "-9c")
	g.Stdin = bytes.NewReader(data)
	g.Stdout = &gz
	if err := g.Run(); err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	return os.WriteFile(filepath.Join(debsRoot, "Packages.gz"), gz.Bytes(), 0o644)
}
