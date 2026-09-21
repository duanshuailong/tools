package builder

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Versions holds the component versions detected from a template directory.
// Any field may be empty when its source file is not present.
type Versions struct {
	System string `json:"system"` // e.g. 24.04.4 (from ubuntu-*.iso)
	Kernel string `json:"kernel"` // e.g. 6.8.0-111 (from linux-* debs)
	NVIDIA string `json:"nvidia"` // e.g. 595.71.05 (from cuda_*.run)
	CUDA   string `json:"cuda"`   // e.g. 13.2.2   (from cuda_*.run)
	OFED   string `json:"ofed"`   // e.g. 24.04-0.6.6.0 (from MLNX_OFED_LINUX-*.tgz)
}

var (
	// ubuntu-24.04.4-live-server-amd64.iso -> 24.04.4
	reISO = regexp.MustCompile(`ubuntu-(\d+\.\d+(?:\.\d+)?)-`)
	// cuda_13.2.2_595.71.05_linux.run -> cuda=13.2.2 driver=595.71.05
	reCUDA = regexp.MustCompile(`cuda_(\d[\d.]*)_(\d[\d.]*)_linux\.run`)
	// MLNX_OFED_LINUX-24.04-0.6.6.0-ubuntu24.04-x86_64.tgz -> 24.04-0.6.6.0
	reOFED = regexp.MustCompile(`MLNX_OFED_LINUX-(\d+\.\d+-[\d.]+)`)
	// linux-tools-6.8.0-111-generic_... -> 6.8.0-111
	reKernel = regexp.MustCompile(`linux-(?:image|headers|tools|modules)-(\d+\.\d+\.\d+-\d+)-generic`)
)

// DetectVersions scans the template directory for component versions. It reads
// filenames only (no ISO/deb extraction), so it is cheap and side-effect free.
// fallbackISOName, if given, supplies the system version when no local ISO is
// present (e.g. the configured auto-fetch source name).
func DetectVersions(templateDir, fallbackISOName string) Versions {
	var v Versions

	// System version from the local source ISO, else the fallback name.
	if matches, _ := filepath.Glob(filepath.Join(templateDir, "ubuntu*.iso")); len(matches) > 0 {
		if m := reISO.FindStringSubmatch(filepath.Base(matches[0])); m != nil {
			v.System = m[1]
		}
	}
	if v.System == "" && fallbackISOName != "" {
		if m := reISO.FindStringSubmatch(fallbackISOName); m != nil {
			v.System = m[1]
		}
	}

	// Driver versions from drivers/.
	driversDir := filepath.Join(templateDir, "drivers")
	if entries, err := os.ReadDir(driversDir); err == nil {
		for _, e := range entries {
			name := e.Name()
			if m := reCUDA.FindStringSubmatch(name); m != nil {
				v.CUDA, v.NVIDIA = m[1], m[2]
			}
			if m := reOFED.FindStringSubmatch(name); m != nil {
				v.OFED = m[1]
			}
		}
	}

	// Kernel version from any linux-* deb under debs/.
	v.Kernel = detectKernel(filepath.Join(templateDir, "debs"))

	return v
}

// detectKernel walks debs/ one level deep looking for a linux-* deb encoding a
// kernel version. Returns the first match, or "".
func detectKernel(debsDir string) string {
	groups, err := os.ReadDir(debsDir)
	if err != nil {
		return ""
	}
	for _, g := range groups {
		if !g.IsDir() {
			if m := reKernel.FindStringSubmatch(g.Name()); m != nil {
				return m[1]
			}
			continue
		}
		files, err := os.ReadDir(filepath.Join(debsDir, g.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if m := reKernel.FindStringSubmatch(f.Name()); m != nil {
				return m[1]
			}
		}
	}
	return ""
}

// DefaultISOName composes the default output filename from detected versions,
// honoring the include flags: the nvidia/cuda parts are dropped when NVIDIA is
// excluded, and the ofed part when OFED is excluded. Unknown versions are
// omitted. The result always ends in ".iso".
//
// Example: ubuntu24.04.4-6.8.0-111-nvidia595.71.05-cuda13.2.2-ofed24.04-0.6.6.0.iso
func (v Versions) DefaultISOName(opts Options) string {
	var parts []string
	if v.System != "" {
		parts = append(parts, "ubuntu"+v.System)
	}
	if v.Kernel != "" {
		parts = append(parts, v.Kernel)
	}
	if opts.IncludeNVIDIA {
		if v.NVIDIA != "" {
			parts = append(parts, "nvidia"+v.NVIDIA)
		}
		if v.CUDA != "" {
			parts = append(parts, "cuda"+v.CUDA)
		}
	}
	if opts.IncludeOFED && v.OFED != "" {
		parts = append(parts, "ofed"+v.OFED)
	}
	if len(parts) == 0 {
		return "new-ubuntu.iso"
	}
	return strings.Join(parts, "-") + ".iso"
}

// SanitizeISOName turns a user-provided name into a safe bare filename ending in
// ".iso": path separators are stripped, and ".iso" is appended if missing.
// Returns "" if the input has no usable characters.
func SanitizeISOName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	// Keep only the base name; drop any directory components.
	name = filepath.Base(filepath.FromSlash(name))
	name = strings.ReplaceAll(name, string(filepath.Separator), "")
	// Guard against traversal remnants.
	name = strings.Trim(name, ". ")
	if name == "" {
		return ""
	}
	if !strings.HasSuffix(strings.ToLower(name), ".iso") {
		name += ".iso"
	}
	return name
}
