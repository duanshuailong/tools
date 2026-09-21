// Package assets manages a versioned, local-first store of the large build
// inputs: base OS images, NVIDIA driver+CUDA installers, and OFED packages.
//
// Layout under the store root:
//
//	<root>/base/<os>/<version>/<iso>            e.g. base/ubuntu/24.04.4/ubuntu-24.04.4-live-server-amd64.iso
//	<root>/nvidia/<driver>/<cuda>/<cuda.run>    e.g. nvidia/580.65.06/13.0.0/cuda_13.0.0_580.65.06_linux.run
//	<root>/ofed/<version>/<tgz>                 e.g. ofed/24.04-0.6.6.0/MLNX_OFED_LINUX-24.04-0.6.6.0-ubuntu24.04-x86_64.tgz
//
// Resolution is local-first: a requested asset present under the root is used
// as-is; a missing asset triggers an online download if the catalog knows a
// source for it (base images, CUDA, and OFED all have verified sources).
package assets

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"isofactory/internal/fetcher"
	"isofactory/internal/progress"
)

// Selection describes the versioned inputs a build wants.
type Selection struct {
	OS            string // e.g. "ubuntu"
	SystemVersion string // e.g. "24.04.4"

	IncludeNVIDIA bool
	NVIDIAVersion string // driver version, e.g. "580.65.06"
	CUDAVersion   string // e.g. "13.0.0"

	// Independent apt-based NVIDIA selection (driver and CUDA chosen freely from
	// the NVIDIA apt repo). When NVDriver/NVToolkit are set, the build fetches
	// these debs into the offline repo instead of the bundled .run installer.
	NVDriver  string // cuda-drivers apt version, e.g. "590.48.01-0ubuntu1"
	NVToolkit string // cuda-toolkit package, e.g. "cuda-toolkit-13-1"

	IncludeOFED bool
	OFEDVersion string // e.g. "24.04-0.6.6.0" (only for OFEDSource=="mlnx")
	// OFEDSource selects the OFED provider: "mlnx" = legacy MLNX_OFED .tgz
	// (≤24.10, frozen), "doca" = DOCA-OFED apt (current, e.g. OFED 26.07).
	// Empty means "mlnx" for backward compatibility.
	OFEDSource string

	// ToolGroups are tool-package group names to bundle into the ISO (downloaded
	// into the offline apt repo at build time if missing), e.g. ["base","net-tools"].
	ToolGroups []string
}

// Resolved is the outcome of resolving a Selection against the store.
type Resolved struct {
	BaseISO string // absolute path to the base ISO (may need fetching first)
	CUDARun string // absolute path to the cuda .run, or "" if NVIDIA not included
	OFEDTgz string // absolute path to the OFED tgz, or "" if OFED not included
	Missing []Missing

	baseSpec *fetcher.Source // download spec for the base ISO (nil if none)
	cudaSpec *fetcher.Source
	ofedSpec *fetcher.Source
	ofedTag  string
}

// Missing is an asset that is not present locally.
type Missing struct {
	Kind         string `json:"kind"` // "base" | "nvidia" | "ofed"
	Path         string `json:"path"` // expected local path
	Downloadable bool   `json:"downloadable"`
	Note         string `json:"note"`
}

// Store manages assets under a root directory.
type Store struct {
	root    string
	fetch   *fetcher.Fetcher
	mu      sync.RWMutex // guards catalog (mutated by live auto-refresh)
	catalog Catalog
}

// New creates a Store rooted at root (created if needed) with the default
// catalog of downloadable assets.
func New(root string) (*Store, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &Store{root: root, fetch: fetcher.New(), catalog: DefaultCatalog()}, nil
}

// Root returns the store root directory.
func (s *Store) Root() string { return s.root }

// Catalog returns the store's download catalog.
func (s *Store) Catalog() Catalog {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.catalog
}

// SetCatalog overrides the download catalog (used in tests).
func (s *Store) SetCatalog(c Catalog) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.catalog = c
}

// SetFetcher overrides the downloader (used in tests).
func (s *Store) SetFetcher(f *fetcher.Fetcher) { s.fetch = f }

func (s *Store) baseDir(os, ver string) string     { return filepath.Join(s.root, "base", os, ver) }
func (s *Store) nvidiaDir(drv, cuda string) string { return filepath.Join(s.root, "nvidia", drv, cuda) }
func (s *Store) ofedDir(ver string) string         { return filepath.Join(s.root, "ofed", ver) }

// Resolve locates the selected assets under the store, recording local hits and
// missing items (with their catalog download spec attached for Ensure).
func (s *Store) Resolve(sel Selection) (Resolved, error) {
	if sel.OS == "" || sel.SystemVersion == "" {
		return Resolved{}, fmt.Errorf("selection requires OS and system version")
	}
	var r Resolved

	// Snapshot the catalog once under the lock (live auto-refresh may mutate it).
	s.mu.RLock()
	cat := s.catalog
	s.mu.RUnlock()

	// Base image.
	baseDir := s.baseDir(sel.OS, sel.SystemVersion)
	if local := firstFile(baseDir, "*.iso"); local != "" {
		r.BaseISO = local
	} else if spec, ok := cat.LookupImage(sel.OS, sel.SystemVersion); ok {
		sp := spec
		r.baseSpec = &sp
		r.BaseISO = filepath.Join(baseDir, spec.Name)
		r.Missing = append(r.Missing, Missing{Kind: "base", Path: r.BaseISO, Downloadable: true,
			Note: fmt.Sprintf("将在线下载基础镜像 %s", spec.Name)})
	} else {
		r.Missing = append(r.Missing, Missing{Kind: "base", Path: baseDir, Downloadable: false,
			Note: fmt.Sprintf("目录无 %s %s 基础镜像，且不在可下载清单中", sel.OS, sel.SystemVersion)})
	}

	// NVIDIA + CUDA.
	if sel.IncludeNVIDIA {
		if sel.NVIDIAVersion == "" || sel.CUDAVersion == "" {
			return Resolved{}, fmt.Errorf("NVIDIA included but driver/cuda version not specified")
		}
		dir := s.nvidiaDir(sel.NVIDIAVersion, sel.CUDAVersion)
		if local := firstFile(dir, "cuda_*.run"); local != "" {
			r.CUDARun = local
		} else if spec, ok := cat.LookupCUDA(sel.CUDAVersion, sel.NVIDIAVersion); ok {
			sp := spec
			r.cudaSpec = &sp
			r.CUDARun = filepath.Join(dir, spec.Name)
			r.Missing = append(r.Missing, Missing{Kind: "nvidia", Path: r.CUDARun, Downloadable: true,
				Note: fmt.Sprintf("将在线下载 NVIDIA %s / CUDA %s（%s）", sel.NVIDIAVersion, sel.CUDAVersion, spec.Name)})
		} else {
			r.Missing = append(r.Missing, Missing{Kind: "nvidia", Path: dir, Downloadable: false,
				Note: fmt.Sprintf("NVIDIA %s / CUDA %s 不在可下载清单中，请放入 %s", sel.NVIDIAVersion, sel.CUDAVersion, dir)})
		}
	}

	// OFED.
	if sel.IncludeOFED {
		if sel.OFEDVersion == "" {
			return Resolved{}, fmt.Errorf("OFED included but version not specified")
		}
		dir := s.ofedDir(sel.OFEDVersion)
		if local := firstFile(dir, "MLNX_OFED_LINUX-*.tgz"); local != "" {
			r.OFEDTgz = local
		} else {
			tag := OFEDTagForSystem(sel.SystemVersion)
			r.ofedTag = tag
			if spec, ok := cat.LookupOFED(sel.OFEDVersion, tag); ok {
				sp := spec
				r.ofedSpec = &sp
				r.OFEDTgz = filepath.Join(dir, spec.Name)
				r.Missing = append(r.Missing, Missing{Kind: "ofed", Path: r.OFEDTgz, Downloadable: true,
					Note: fmt.Sprintf("将在线下载 OFED %s（%s）", sel.OFEDVersion, spec.Name)})
			} else {
				r.Missing = append(r.Missing, Missing{Kind: "ofed", Path: dir, Downloadable: false,
					Note: fmt.Sprintf("OFED %s（%s）不在可下载清单中，请放入 %s", sel.OFEDVersion, tag, dir)})
			}
		}
	}

	return r, nil
}

// Ensure makes the resolved assets present locally, downloading any missing
// ones that have a catalog source (base image, CUDA, OFED) with progress. A
// missing asset with no download source is a hard error.
func (s *Store) Ensure(sel Selection, rep progress.Reporter) (Resolved, error) {
	r, err := s.Resolve(sel)
	if err != nil {
		return r, err
	}

	// Fail early on undownloadable missing assets.
	var undownloadable []Missing
	for _, m := range r.Missing {
		if !m.Downloadable {
			undownloadable = append(undownloadable, m)
		}
	}
	if len(undownloadable) > 0 {
		return r, fmt.Errorf("缺少无法在线下载的资产：%s", joinMissing(undownloadable))
	}

	// Base image.
	if r.baseSpec != nil {
		path, err := s.download(*r.baseSpec, s.baseDir(sel.OS, sel.SystemVersion), "基础镜像", rep)
		if err != nil {
			return r, err
		}
		r.BaseISO = path
	}
	// CUDA.
	if r.cudaSpec != nil {
		path, err := s.download(*r.cudaSpec, s.nvidiaDir(sel.NVIDIAVersion, sel.CUDAVersion), "NVIDIA 驱动 / CUDA", rep)
		if err != nil {
			return r, err
		}
		r.CUDARun = path
	}
	// OFED.
	if r.ofedSpec != nil {
		path, err := s.download(*r.ofedSpec, s.ofedDir(sel.OFEDVersion), "OFED", rep)
		if err != nil {
			return r, err
		}
		r.OFEDTgz = path
	}

	r.Missing = nil
	return r, nil
}

// download fetches src into destDir, reporting progress with a label.
func (s *Store) download(src fetcher.Source, destDir, label string, rep progress.Reporter) (string, error) {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", err
	}
	rep.Report(progress.Update{Phase: progress.PhaseDownloading, Percent: -1,
		Message: fmt.Sprintf("下载%s %s", label, src.Name)})
	path, err := s.fetch.Fetch(src, destDir, func(p fetcher.Progress) {
		pct := -1.0
		if p.Total > 0 {
			pct = 100 * float64(p.Downloaded) / float64(p.Total)
		}
		rep.Report(progress.Update{Phase: progress.PhaseDownloading, Percent: pct,
			Message: fmt.Sprintf("下载%s %s", label, src.Name)})
	})
	if err != nil {
		return "", fmt.Errorf("下载%s失败: %w", label, err)
	}
	return path, nil
}

// firstFile returns the first path under dir matching pattern, or "".
func firstFile(dir, pattern string) string {
	matches, _ := filepath.Glob(filepath.Join(dir, pattern))
	if len(matches) > 0 {
		return matches[0]
	}
	return ""
}

func joinMissing(ms []Missing) string {
	out := ""
	for i, m := range ms {
		if i > 0 {
			out += "；"
		}
		out += m.Note
	}
	return out
}
