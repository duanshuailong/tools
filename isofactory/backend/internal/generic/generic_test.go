package generic

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateRequiresSource(t *testing.T) {
	if err := New().Validate(Spec{}); err == nil {
		t.Error("want error for empty source dir")
	}
}

func TestValidateBIOSNeedsImage(t *testing.T) {
	dir := t.TempDir()
	err := New().Validate(Spec{SourceDir: dir, Boot: BootBIOS})
	if err == nil || !strings.Contains(err.Error(), "BIOSBootImage") {
		t.Errorf("want BIOS image error, got %v", err)
	}
}

func TestArgsDataOnly(t *testing.T) {
	args := New().Args(Spec{SourceDir: "/src", VolumeID: "MYVOL"}, "/out.iso")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-as mkisofs") || !strings.Contains(joined, "-V MYVOL") || !strings.HasSuffix(joined, "/src") {
		t.Errorf("unexpected args: %v", args)
	}
	if strings.Contains(joined, "-b ") || strings.Contains(joined, "-e ") {
		t.Errorf("data-only args should have no boot flags: %v", args)
	}
}

func TestArgsBIOS(t *testing.T) {
	args := New().Args(Spec{SourceDir: "/src", Boot: BootBIOS, BIOSBootImage: "isolinux/isolinux.bin"}, "/out.iso")
	joined := strings.Join(args, " ")
	for _, want := range []string{"-b isolinux/isolinux.bin", "-c boot.catalog", "-no-emul-boot", "-boot-load-size 4", "-boot-info-table"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args missing %q: %v", want, args)
		}
	}
}

// TestRunProducesRealISO builds an actual data ISO and verifies it, exercising
// the real xorriso path. Skipped if xorriso is unavailable.
func TestRunProducesRealISO(t *testing.T) {
	if _, err := exec.LookPath("xorriso"); err != nil {
		t.Skip("xorriso not installed")
	}
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "hello.txt"), []byte("hi from isofactory"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "nested.txt"), []byte("nested"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "test.iso")
	var logBuf bytes.Buffer
	got, err := New().Run(context.Background(), Spec{SourceDir: src, VolumeID: "TESTVOL"}, out, &logBuf)
	if err != nil {
		t.Fatalf("Run: %v\nlog:\n%s", err, logBuf.String())
	}
	if got != out {
		t.Errorf("returned path %q, want %q", got, out)
	}

	info, err := os.Stat(out)
	if err != nil {
		t.Fatalf("output ISO missing: %v", err)
	}
	if info.Size() == 0 {
		t.Error("output ISO is empty")
	}

	// Verify the ISO lists our files back via xorriso.
	lsCmd := exec.Command("xorriso", "-indev", out, "-find", "/")
	lsOut, err := lsCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("xorriso -find: %v\n%s", err, lsOut)
	}
	listing := string(lsOut)
	if !strings.Contains(listing, "hello.txt") || !strings.Contains(listing, "nested.txt") {
		t.Errorf("ISO listing missing expected files:\n%s", listing)
	}
}
