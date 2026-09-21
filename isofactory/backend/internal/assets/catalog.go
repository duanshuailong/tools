package assets

import (
	"fmt"
	"strings"

	"isofactory/internal/fetcher"
)

// Catalog is the platform's knowledge of what it can download online: base
// images, NVIDIA driver+CUDA installers, and MLNX_OFED packages. Every entry
// here was verified reachable (HTTP 200/206) before being added — do not add
// unverified URLs.
type Catalog struct {
	Images []ImageRelease
	CUDA   []CUDARelease
	OFED   []OFEDRelease
}

// ImageRelease is a downloadable base OS image.
type ImageRelease struct {
	OS      string   `json:"os"`
	Version string   `json:"version"`
	Name    string   `json:"name"`
	URL     string   `json:"-"`
	Mirrors []string `json:"-"`
	SHA256  string   `json:"-"`
}

// CUDARelease is a downloadable NVIDIA CUDA+driver installer (.run). The URL is
// derived from the (CUDA, driver) version pair.
type CUDARelease struct {
	CUDA   string `json:"cuda"`   // e.g. 13.0.0
	Driver string `json:"driver"` // e.g. 580.65.06
}

// FileName is the installer filename.
func (c CUDARelease) FileName() string {
	return fmt.Sprintf("cuda_%s_%s_linux.run", c.CUDA, c.Driver)
}

// Source is the download spec (NVIDIA official CDN, redirects to the .cn CDN
// for mainland China automatically).
func (c CUDARelease) Source() fetcher.Source {
	return fetcher.Source{
		Name: c.FileName(),
		URL:  fmt.Sprintf("https://developer.download.nvidia.com/compute/cuda/%s/local_installers/%s", c.CUDA, c.FileName()),
	}
}

// OFEDRelease is a downloadable MLNX_OFED package for a given OS variant.
type OFEDRelease struct {
	Version string `json:"version"` // e.g. 24.04-0.6.6.0
	OSTag   string `json:"os_tag"`  // e.g. ubuntu24.04
}

// FileName is the tarball filename.
func (o OFEDRelease) FileName() string {
	return fmt.Sprintf("MLNX_OFED_LINUX-%s-%s-x86_64.tgz", o.Version, o.OSTag)
}

// Source is the download spec (NVIDIA/Mellanox content CDN).
func (o OFEDRelease) Source() fetcher.Source {
	return fetcher.Source{
		Name: o.FileName(),
		URL:  fmt.Sprintf("https://content.mellanox.com/ofed/MLNX_OFED-%s/%s", o.Version, o.FileName()),
	}
}

// LookupImage returns the base-image source for (os, version).
func (c Catalog) LookupImage(os, version string) (fetcher.Source, bool) {
	for _, im := range c.Images {
		if im.OS == os && im.Version == version {
			return fetcher.Source{Name: im.Name, URL: im.URL, Mirrors: im.Mirrors, SHA256: im.SHA256}, true
		}
	}
	return fetcher.Source{}, false
}

// LookupCUDA returns the installer source for a (cuda, driver) pair.
func (c Catalog) LookupCUDA(cuda, driver string) (fetcher.Source, bool) {
	for _, r := range c.CUDA {
		if r.CUDA == cuda && r.Driver == driver {
			return r.Source(), true
		}
	}
	return fetcher.Source{}, false
}

// LookupOFED returns the tarball source for an OFED version + OS tag.
func (c Catalog) LookupOFED(version, osTag string) (fetcher.Source, bool) {
	for _, r := range c.OFED {
		if r.Version == version && r.OSTag == osTag {
			return r.Source(), true
		}
	}
	return fetcher.Source{}, false
}

// OFEDTagForSystem maps a system version (e.g. 24.04.4) to the OFED OS tag
// (e.g. ubuntu24.04). Falls back to ubuntu24.04.
func OFEDTagForSystem(systemVersion string) string {
	parts := strings.Split(systemVersion, ".")
	if len(parts) >= 2 {
		return "ubuntu" + parts[0] + "." + parts[1]
	}
	return "ubuntu24.04"
}

// DefaultCatalog returns the built-in, verified-downloadable catalog.
//
// Base images: Ubuntu Server 24.04.x live-server amd64 (Aliyun primary + NJU
// fallback, checksummed). CUDA/OFED URLs were probed (HTTP 206) 2026-09-20.
func DefaultCatalog() Catalog {
	return Catalog{
		Images: []ImageRelease{
			{OS: "ubuntu", Version: "24.04.5", Name: "ubuntu-24.04.5-live-server-amd64.iso",
				URL:     "https://mirrors.aliyun.com/ubuntu-releases/24.04/ubuntu-24.04.5-live-server-amd64.iso",
				Mirrors: []string{"https://mirrors.nju.edu.cn/ubuntu-releases/24.04/ubuntu-24.04.5-live-server-amd64.iso"},
				SHA256:  "97f3d7ffb032c3eb3b23d2c8be9cc76e60c2c1f2c0146ba5ba9fe01cafae0fd8"},
			{OS: "ubuntu", Version: "24.04.4", Name: "ubuntu-24.04.4-live-server-amd64.iso",
				URL:     "https://mirrors.aliyun.com/ubuntu-releases/24.04/ubuntu-24.04.4-live-server-amd64.iso",
				Mirrors: []string{"https://mirrors.nju.edu.cn/ubuntu-releases/24.04/ubuntu-24.04.4-live-server-amd64.iso"},
				SHA256:  "e907d92eeec9df64163a7e454cbc8d7755e8ddc7ed42f99dbc80c40f1a138433"},
			{OS: "ubuntu", Version: "24.04.3", Name: "ubuntu-24.04.3-live-server-amd64.iso",
				URL:     "https://mirrors.aliyun.com/ubuntu-releases/24.04/ubuntu-24.04.3-live-server-amd64.iso",
				Mirrors: []string{"https://mirrors.nju.edu.cn/ubuntu-releases/24.04/ubuntu-24.04.3-live-server-amd64.iso"},
				SHA256:  "c3514bf0056180d09376462a7a1b4f213c1d6e8ea67fae5c25099c6fd3d8274b"},
		},
		// CUDA (cuda, driver) pairs — all probed HTTP 206 (newest first). Each
		// .run bundles a fixed driver, so the pair is the unit of selection.
		CUDA: []CUDARelease{
			{CUDA: "13.2.2", Driver: "595.71.05"}, // template baseline
			{CUDA: "13.0.1", Driver: "580.82.07"},
			{CUDA: "13.0.0", Driver: "580.65.06"},
			{CUDA: "12.9.1", Driver: "575.57.08"},
			{CUDA: "12.8.1", Driver: "570.124.06"},
			{CUDA: "12.6.3", Driver: "560.35.05"},
			{CUDA: "12.6.2", Driver: "560.35.03"},
			{CUDA: "12.6.0", Driver: "560.28.03"},
			{CUDA: "12.5.1", Driver: "555.42.06"},
			{CUDA: "12.5.0", Driver: "555.42.02"},
			{CUDA: "12.4.1", Driver: "550.54.15"},
			{CUDA: "12.4.0", Driver: "550.54.14"},
			{CUDA: "12.3.2", Driver: "545.23.08"},
			{CUDA: "12.2.2", Driver: "535.104.05"},
			{CUDA: "12.1.1", Driver: "530.30.02"},
			{CUDA: "12.0.1", Driver: "525.85.12"},
			{CUDA: "11.8.0", Driver: "520.61.05"},
		},
		// OFED versions — all probed HTTP 206 (newest first, 2026-09-20).
		OFED: []OFEDRelease{
			{Version: "24.10-3.2.5.0", OSTag: "ubuntu24.04"},
			{Version: "24.10-3.2.5.0", OSTag: "ubuntu22.04"},
			{Version: "24.10-2.1.8.0", OSTag: "ubuntu24.04"},
			{Version: "24.10-1.1.4.0", OSTag: "ubuntu24.04"},
			{Version: "24.07-0.6.1.0", OSTag: "ubuntu24.04"},
			{Version: "24.04-0.7.0.0", OSTag: "ubuntu24.04"},
			{Version: "24.04-0.6.6.0", OSTag: "ubuntu24.04"},
			{Version: "24.04-0.6.6.0", OSTag: "ubuntu22.04"},
		},
	}
}
