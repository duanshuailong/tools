// Package builder wraps the existing template/pack.sh pipeline so the platform
// can invoke ISO builds programmatically. It does not reimplement packing — it
// resolves the large versioned inputs (base image, drivers) from the asset
// store, materializes them into the template workspace pack.sh expects, then
// shells out to pack.sh with the same environment knobs the CLI already honors.
package builder

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"isofactory/internal/assets"
	"isofactory/internal/progress"
	"isofactory/internal/toolpkgs"
)

// Options mirror the environment variables pack.sh reads.
type Options struct {
	// IncludeNVIDIA maps to INCLUDE_NVIDIA (default yes in pack.sh).
	IncludeNVIDIA bool
	// IncludeOFED maps to INCLUDE_OFED (default yes in pack.sh).
	IncludeOFED bool
}

// NVDownloader fetches an independently-selected NVIDIA driver + CUDA toolkit
// (apt debs) into the offline repo. *nvapt.Manager satisfies it; nil disables
// the apt-based NVIDIA path.
type NVDownloader interface {
	DownloadSync(ctx context.Context, sel NVSelection, logOut io.Writer) (string, error)
}

// ToolDownloader ensures the named tool-package groups are present in their
// version-keyed caches (downloading any missing ones) and returns the per-group
// cache directories to hardlink into the workspace. *toolpkgs.Manager satisfies
// it; nil disables build-time tool-package download.
type ToolDownloader interface {
	EnsureGroupsSync(ctx context.Context, codename string, names []string, logOut io.Writer) ([]string, error)
}

// DOCADownloader fetches the DOCA-OFED stack (current OFED via NVIDIA DOCA apt
// repo) into its cache dir and returns it. *doca.Manager satisfies it; nil
// disables the DOCA-OFED path.
type DOCADownloader interface {
	DownloadSync(ctx context.Context, logOut io.Writer) (string, error)
}

// NVSelection mirrors nvapt.Selection without importing it (avoids a cycle-free
// but tighter coupling). Driver is the cuda-drivers apt version; Toolkit is the
// cuda-toolkit package name.
type NVSelection struct {
	Driver  string
	Toolkit string
}

// Builder runs pack.sh inside a template workspace, sourcing large inputs from
// a versioned asset store.
type Builder struct {
	// TemplateDir holds pack.sh, debs/, user-data* (the build recipe). The base
	// ISO and driver files are materialized into it per build from the store.
	TemplateDir string
	// ScriptName is the packing script filename; defaults to "pack.sh".
	ScriptName string
	// Store provides versioned base images and drivers, local-first.
	Store *assets.Store
	// NV, if set, fetches independently-selected NVIDIA driver+CUDA apt debs.
	NV NVDownloader
	// Tools, if set, ensures selected tool-package groups are downloaded.
	Tools ToolDownloader
	// DOCA, if set, fetches the DOCA-OFED stack when OFEDSource=="doca".
	DOCA DOCADownloader
	// WorkRoot is the parent dir for per-job isolated build workspaces. Each
	// build gets its own subdir (recipe copied in, assets hardlinked) so
	// concurrent builds never share pack.sh's extract dir or output path. Empty
	// falls back to os.MkdirTemp's default (fine for tests / single-worker).
	WorkRoot string
	// DefaultOS / DefaultSystemVersion seed a Selection when the request omits
	// them (e.g. the auto-name preview or a bare build).
	DefaultOS            string
	DefaultSystemVersion string
}

// New returns a Builder for the given template dir and asset store, defaulting
// to Ubuntu 24.04.4 as the base OS/version.
func New(templateDir string, store *assets.Store) *Builder {
	return &Builder{
		TemplateDir:          templateDir,
		ScriptName:           "pack.sh",
		Store:                store,
		DefaultOS:            "ubuntu",
		DefaultSystemVersion: "24.04.4",
	}
}

// optionsFrom derives pack.sh env options from a selection.
func optionsFrom(sel assets.Selection) Options {
	return Options{IncludeNVIDIA: sel.IncludeNVIDIA, IncludeOFED: sel.IncludeOFED}
}

// Versions returns the component versions detected from the template dir. The
// system version falls back to the builder's default when no local ISO exists.
func (b *Builder) Versions() Versions {
	fallback := ""
	if b.DefaultOS == "ubuntu" && b.DefaultSystemVersion != "" {
		fallback = "ubuntu-" + b.DefaultSystemVersion + "-live-server-amd64.iso"
	}
	return DetectVersions(b.TemplateDir, fallback)
}

// DefaultISOName composes the default output name for a selection, filling in
// unspecified versions from detection where possible. Apt-based driver/CUDA
// selections (NVDriver/NVToolkit) take precedence over the .run versions.
func (b *Builder) DefaultISOName(sel assets.Selection) string {
	nv, cuda := sel.NVIDIAVersion, sel.CUDAVersion
	if sel.NVDriver != "" {
		nv = cleanAptDriver(sel.NVDriver) // 590.48.01-0ubuntu1 -> 590.48.01
	}
	if sel.NVToolkit != "" {
		cuda = toolkitLabel(sel.NVToolkit) // cuda-toolkit-13-1 -> 13.1
	}
	v := Versions{
		System: sel.SystemVersion,
		Kernel: detectKernel(filepath.Join(b.TemplateDir, "debs")),
		NVIDIA: nv,
		CUDA:   cuda,
		OFED:   sel.OFEDVersion,
	}
	if v.System == "" {
		v.System = b.DefaultSystemVersion
	}
	opts := optionsFrom(sel)
	if sel.NVDriver != "" || sel.NVToolkit != "" {
		opts.IncludeNVIDIA = true
	}
	return v.DefaultISOName(opts)
}

// cleanAptDriver strips the Debian revision suffix: 590.48.01-0ubuntu1 -> 590.48.01.
func cleanAptDriver(v string) string {
	if i := strings.IndexByte(v, '-'); i >= 0 {
		return v[:i]
	}
	return v
}

// toolkitLabel turns cuda-toolkit-13-1 into 13.1.
func toolkitLabel(pkg string) string {
	s := strings.TrimPrefix(pkg, "cuda-toolkit-")
	return strings.ReplaceAll(s, "-", ".")
}

// Preflight verifies the workspace is usable before a build starts: pack.sh
// present and xorriso on PATH. Asset availability is validated at build time by
// the store (which may download the base image).
func (b *Builder) Preflight() error {
	script := filepath.Join(b.TemplateDir, b.scriptName())
	if _, err := os.Stat(script); err != nil {
		return fmt.Errorf("packing script not found at %s: %w", script, err)
	}
	if _, err := exec.LookPath("xorriso"); err != nil {
		return fmt.Errorf("xorriso not found in PATH; install it (apt install xorriso): %w", err)
	}
	if b.Store == nil {
		return fmt.Errorf("no asset store configured")
	}
	return nil
}

// Resolve reports what the given selection would use and what is missing,
// without downloading anything. Used by the UI to preview readiness.
func (b *Builder) Resolve(sel assets.Selection) (assets.Resolved, error) {
	return b.Store.Resolve(b.applyDefaults(sel))
}

// Catalog returns the store's download catalog (base images, CUDA, OFED).
func (b *Builder) Catalog() assets.Catalog {
	if b.Store == nil {
		return assets.Catalog{}
	}
	return b.Store.Catalog()
}

// applyDefaults fills OS/SystemVersion from the builder defaults when blank.
func (b *Builder) applyDefaults(sel assets.Selection) assets.Selection {
	if sel.OS == "" {
		sel.OS = b.DefaultOS
	}
	if sel.SystemVersion == "" {
		sel.SystemVersion = b.DefaultSystemVersion
	}
	return sel
}

// Run builds an ISO in an ISOLATED per-job workspace so concurrent builds never
// share pack.sh's extract dir, debs tree, or output path. It: (1) ensures the
// base image + .run/.tgz in the shared asset store; (2) downloads apt debs into
// version-keyed shared caches; (3) assembles a fresh workspace (recipe copied,
// base ISO symlinked, drivers + cached debs hardlinked); (4) regenerates the
// offline index; (5) runs pack.sh there. Returns the produced ISO path and a
// cleanup func that removes the workspace (call after the ISO is relocated).
func (b *Builder) Run(ctx context.Context, sel assets.Selection, logOut io.Writer, rep progress.Reporter) (isoPath string, cleanup func(), err error) {
	if rep == nil {
		rep = progress.Nop
	}
	cleanup = func() {}
	if err := b.Preflight(); err != nil {
		return "", cleanup, err
	}
	sel = b.applyDefaults(sel)

	rep.Report(progress.Update{Phase: progress.PhaseResolving, Percent: -1, Message: "解析构建资产"})
	fmt.Fprintf(logOut, "resolving assets for %s %s (nvidia=%v ofed=%v)\n", sel.OS, sel.SystemVersion, sel.IncludeNVIDIA, sel.OFEDSource)

	resolved, err := b.Store.Ensure(sel, rep)
	if err != nil {
		return "", cleanup, err
	}

	// Collect cache dirs of apt-downloaded debs to hardlink into the workspace.
	// key -> cache dir, where key becomes the debs/<key> subdir in the workspace.
	debCacheDirs := map[string]string{}

	if (sel.NVDriver != "" || sel.NVToolkit != "") && b.NV != nil {
		rep.Report(progress.Update{Phase: progress.PhaseDownloading, Percent: -1, Message: "下载 NVIDIA 驱动 / CUDA (apt)"})
		fmt.Fprintf(logOut, "downloading NVIDIA apt packages: driver=%q toolkit=%q\n", sel.NVDriver, sel.NVToolkit)
		pw := progress.NewAptProgressWriter(logOut, rep, progress.PhaseDownloading, "下载 NVIDIA 驱动 / CUDA")
		dir, err := b.NV.DownloadSync(ctx, NVSelection{Driver: sel.NVDriver, Toolkit: sel.NVToolkit}, pw)
		if err != nil {
			return "", cleanup, fmt.Errorf("download NVIDIA apt packages: %w", err)
		}
		debCacheDirs["nvidia-apt"] = dir
	}

	if len(sel.ToolGroups) > 0 && b.Tools != nil {
		rep.Report(progress.Update{Phase: progress.PhaseDownloading, Percent: -1, Message: "下载常用工具包"})
		codename := toolpkgs.CodenameFor(sel.SystemVersion)
		fmt.Fprintf(logOut, "ensuring tool-package groups for %s (%s): %v\n", sel.SystemVersion, codename, sel.ToolGroups)
		pw := progress.NewAptProgressWriter(logOut, rep, progress.PhaseDownloading, "下载常用工具包")
		dirs, err := b.Tools.EnsureGroupsSync(ctx, codename, sel.ToolGroups, pw)
		if err != nil {
			return "", cleanup, fmt.Errorf("ensure tool packages: %w", err)
		}
		for _, d := range dirs {
			debCacheDirs[filepath.Base(d)] = d
		}
	}

	if sel.OFEDSource == "doca" && b.DOCA != nil {
		rep.Report(progress.Update{Phase: progress.PhaseDownloading, Percent: -1, Message: "下载 DOCA-OFED"})
		fmt.Fprintln(logOut, "downloading DOCA-OFED stack (apt)")
		pw := progress.NewAptProgressWriter(logOut, rep, progress.PhaseDownloading, "下载 DOCA-OFED")
		dir, err := b.DOCA.DownloadSync(ctx, pw)
		if err != nil {
			return "", cleanup, fmt.Errorf("download DOCA-OFED: %w", err)
		}
		debCacheDirs["doca-ofed"] = dir
	}

	// Assemble the isolated workspace.
	rep.Report(progress.Update{Phase: progress.PhaseBuilding, Percent: -1, Message: "组装工作区"})
	ws, err := os.MkdirTemp(b.WorkRoot, "build-")
	if err != nil {
		return "", cleanup, fmt.Errorf("create workspace: %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(ws) }
	if err := b.assembleWorkspace(ws, resolved, debCacheDirs, logOut); err != nil {
		return "", cleanup, fmt.Errorf("assemble workspace: %w", err)
	}

	// Run pack.sh in the workspace.
	rep.Report(progress.Update{Phase: progress.PhaseBuilding, Percent: -1, Message: "运行 pack.sh 打包镜像"})
	opts := optionsFrom(sel)
	cmd := exec.CommandContext(ctx, "bash", b.scriptName())
	cmd.Dir = ws
	cmd.Env = append(os.Environ(),
		"INCLUDE_NVIDIA="+yesNo(opts.IncludeNVIDIA),
		"INCLUDE_OFED="+yesNo(opts.IncludeOFED),
	)
	cmd.Stdout = logOut
	cmd.Stderr = logOut
	if err := cmd.Run(); err != nil {
		return "", cleanup, fmt.Errorf("pack.sh failed: %w", err)
	}

	out := filepath.Join(ws, "new-ubuntu.iso")
	if _, err := os.Stat(out); err != nil {
		return "", cleanup, fmt.Errorf("pack.sh reported success but output ISO missing at %s: %w", out, err)
	}
	return out, cleanup, nil
}

// assembleWorkspace populates an isolated build workspace: the recipe files are
// copied from the template, the base ISO is symlinked, .run/.tgz drivers and
// apt deb caches are hardlinked into debs/<subdir>, and the offline apt index is
// regenerated across debs/.
func (b *Builder) assembleWorkspace(ws string, r assets.Resolved, debCacheDirs map[string]string, logOut io.Writer) error {
	// Copy recipe files (pack.sh, user-data*, debs recipe scripts) — but NOT
	// downloaded debs, produced ISOs, drivers, or stale indexes.
	if err := copyRecipe(b.TemplateDir, ws); err != nil {
		return err
	}

	// Base ISO: symlink (pack.sh reads it via xorriso -indev, never copies it).
	if err := os.Symlink(r.BaseISO, filepath.Join(ws, filepath.Base(r.BaseISO))); err != nil {
		return fmt.Errorf("symlink base ISO: %w", err)
	}
	fmt.Fprintf(logOut, "base ISO: %s\n", filepath.Base(r.BaseISO))

	// .run/.tgz drivers → drivers/ (hardlink; pack.sh cp -r's this dir).
	driversDir := filepath.Join(ws, "drivers")
	if err := os.MkdirAll(driversDir, 0o755); err != nil {
		return err
	}
	for _, src := range []string{r.CUDARun, r.OFEDTgz} {
		if src == "" {
			continue
		}
		if err := linkOrCopy(src, filepath.Join(driversDir, filepath.Base(src))); err != nil {
			return fmt.Errorf("place driver %s: %w", filepath.Base(src), err)
		}
		fmt.Fprintf(logOut, "driver: %s\n", filepath.Base(src))
	}

	// apt deb caches → debs/<subdir>/ (hardlink each .deb).
	debsDir := filepath.Join(ws, "debs")
	if err := os.MkdirAll(debsDir, 0o755); err != nil {
		return err
	}
	for sub, cacheDir := range debCacheDirs {
		dst := filepath.Join(debsDir, sub)
		if err := hardlinkDebs(cacheDir, dst); err != nil {
			return fmt.Errorf("link debs %s: %w", sub, err)
		}
		fmt.Fprintf(logOut, "debs: %s\n", sub)
	}

	// Regenerate the offline apt index across debs/ (so it's a valid file:// repo).
	return regenIndex(debsDir, logOut)
}

// linkOrCopy hardlinks src to dst, falling back to a copy across filesystems.
func linkOrCopy(src, dst string) error {
	if err := os.Link(src, dst); err == nil {
		return nil
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

func (b *Builder) scriptName() string {
	if b.ScriptName == "" {
		return "pack.sh"
	}
	return b.ScriptName
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}
