package builder

import (
	"path/filepath"
	"testing"
)

func setupTemplate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// source ISO
	mustWrite(t, filepath.Join(dir, "ubuntu-24.04.4-live-server-amd64.iso"), "iso")
	// drivers
	mustMkdir(t, filepath.Join(dir, "drivers"))
	mustWrite(t, filepath.Join(dir, "drivers", "cuda_13.2.2_595.71.05_linux.run"), "run")
	mustWrite(t, filepath.Join(dir, "drivers", "MLNX_OFED_LINUX-24.04-0.6.6.0-ubuntu24.04-x86_64.tgz"), "tgz")
	// kernel deb under debs/linux-tools
	mustMkdir(t, filepath.Join(dir, "debs", "linux-tools"))
	mustWrite(t, filepath.Join(dir, "debs", "linux-tools", "linux-tools-6.8.0-111-generic_6.8.0-111.111_amd64.deb"), "deb")
	return dir
}

func TestDetectVersions(t *testing.T) {
	v := DetectVersions(setupTemplate(t), "")
	if v.System != "24.04.4" {
		t.Errorf("System = %q, want 24.04.4", v.System)
	}
	if v.Kernel != "6.8.0-111" {
		t.Errorf("Kernel = %q, want 6.8.0-111", v.Kernel)
	}
	if v.NVIDIA != "595.71.05" {
		t.Errorf("NVIDIA = %q, want 595.71.05", v.NVIDIA)
	}
	if v.CUDA != "13.2.2" {
		t.Errorf("CUDA = %q, want 13.2.2", v.CUDA)
	}
	if v.OFED != "24.04-0.6.6.0" {
		t.Errorf("OFED = %q, want 24.04-0.6.6.0", v.OFED)
	}
}

func TestDetectVersionsFallbackISOName(t *testing.T) {
	dir := t.TempDir() // no local ISO
	v := DetectVersions(dir, "ubuntu-24.04.4-live-server-amd64.iso")
	if v.System != "24.04.4" {
		t.Errorf("System via fallback = %q, want 24.04.4", v.System)
	}
}

func TestDefaultISONameFull(t *testing.T) {
	v := DetectVersions(setupTemplate(t), "")
	got := v.DefaultISOName(Options{IncludeNVIDIA: true, IncludeOFED: true})
	want := "ubuntu24.04.4-6.8.0-111-nvidia595.71.05-cuda13.2.2-ofed24.04-0.6.6.0.iso"
	if got != want {
		t.Errorf("DefaultISOName =\n %q\nwant\n %q", got, want)
	}
}

func TestDefaultISONameExcludesDrivers(t *testing.T) {
	v := DetectVersions(setupTemplate(t), "")
	got := v.DefaultISOName(Options{IncludeNVIDIA: false, IncludeOFED: false})
	want := "ubuntu24.04.4-6.8.0-111.iso"
	if got != want {
		t.Errorf("DefaultISOName (no drivers) = %q, want %q", got, want)
	}
}

func TestDefaultISONameEmpty(t *testing.T) {
	got := DetectVersions(t.TempDir(), "").DefaultISOName(Options{})
	if got != "new-ubuntu.iso" {
		t.Errorf("empty template name = %q, want new-ubuntu.iso", got)
	}
}

func TestSanitizeISOName(t *testing.T) {
	cases := map[string]string{
		"my-build":         "my-build.iso",
		"my-build.iso":     "my-build.iso",
		"MY.ISO":           "MY.ISO",
		"../../etc/passwd": "passwd.iso",
		"a/b/c.iso":        "c.iso",
		"   ":              "",
		"":                 "",
	}
	for in, want := range cases {
		if got := SanitizeISOName(in); got != want {
			t.Errorf("SanitizeISOName(%q) = %q, want %q", in, got, want)
		}
	}
}
