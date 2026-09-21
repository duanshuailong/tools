// Package generic builds a bootable or data-only ISO from an arbitrary source
// directory, using xorriso's mkisofs emulation. This is the general-purpose
// path (any directory → ISO), distinct from the Ubuntu-specific pack.sh flow in
// package builder.
package generic

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// BootMode selects the boot records written into the ISO.
type BootMode string

const (
	// BootNone produces a data-only ISO (mountable, not bootable).
	BootNone BootMode = "none"
	// BootBIOS writes an El Torito BIOS boot record from a boot image.
	BootBIOS BootMode = "bios"
	// BootUEFI writes an EFI boot record from an EFI image.
	BootUEFI BootMode = "uefi"
	// BootBoth writes both BIOS and UEFI records (hybrid).
	BootBoth BootMode = "both"
)

// Spec describes a generic ISO build.
type Spec struct {
	// SourceDir is the directory whose contents become the ISO root.
	SourceDir string
	// VolumeID is the ISO volume label (defaults to "ISOFACTORY").
	VolumeID string
	// Boot selects the boot mode; defaults to BootNone.
	Boot BootMode
	// BIOSBootImage is the path (relative to SourceDir) of the El Torito boot
	// image, required for BootBIOS/BootBoth (e.g. "isolinux/isolinux.bin").
	BIOSBootImage string
	// BIOSBootCatalog is the boot catalog path relative to SourceDir
	// (e.g. "isolinux/boot.cat"); defaults to "boot.catalog".
	BIOSBootCatalog string
	// EFIBootImage is the path (relative to SourceDir) of the EFI boot image,
	// required for BootUEFI/BootBoth (e.g. "EFI/boot/bootx64.efi" or an efiboot.img).
	EFIBootImage string
}

// Builder produces generic ISOs with xorriso.
type Builder struct{}

// New returns a generic Builder.
func New() *Builder { return &Builder{} }

// Validate checks the spec is internally consistent and its inputs exist.
func (b *Builder) Validate(s Spec) error {
	if s.SourceDir == "" {
		return fmt.Errorf("source directory is required")
	}
	info, err := os.Stat(s.SourceDir)
	if err != nil {
		return fmt.Errorf("source directory %s: %w", s.SourceDir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("source %s is not a directory", s.SourceDir)
	}
	if _, err := exec.LookPath("xorriso"); err != nil {
		return fmt.Errorf("xorriso not found in PATH: %w", err)
	}

	needBIOS := s.Boot == BootBIOS || s.Boot == BootBoth
	needUEFI := s.Boot == BootUEFI || s.Boot == BootBoth
	if needBIOS {
		if s.BIOSBootImage == "" {
			return fmt.Errorf("BIOS boot requires BIOSBootImage")
		}
		if err := mustExist(s.SourceDir, s.BIOSBootImage); err != nil {
			return err
		}
	}
	if needUEFI {
		if s.EFIBootImage == "" {
			return fmt.Errorf("UEFI boot requires EFIBootImage")
		}
		if err := mustExist(s.SourceDir, s.EFIBootImage); err != nil {
			return err
		}
	}
	return nil
}

// Args builds the xorriso argument list for the spec, writing to outPath. It is
// separated from Run so it can be unit-tested without invoking xorriso.
func (b *Builder) Args(s Spec, outPath string) []string {
	vol := s.VolumeID
	if vol == "" {
		vol = "ISOFACTORY"
	}
	args := []string{"-as", "mkisofs", "-o", outPath, "-V", vol, "-r", "-J"}

	if s.Boot == BootBIOS || s.Boot == BootBoth {
		cat := s.BIOSBootCatalog
		if cat == "" {
			cat = "boot.catalog"
		}
		args = append(args,
			"-b", s.BIOSBootImage,
			"-c", cat,
			"-no-emul-boot", "-boot-load-size", "4", "-boot-info-table",
		)
	}
	if s.Boot == BootUEFI || s.Boot == BootBoth {
		// -e names the EFI image; -no-emul-boot applies to this entry too.
		args = append(args, "-eltorito-alt-boot", "-e", s.EFIBootImage, "-no-emul-boot")
	}

	args = append(args, s.SourceDir)
	return args
}

// Run validates the spec and runs xorriso, streaming output to logOut. On
// success it returns outPath.
func (b *Builder) Run(ctx context.Context, s Spec, outPath string, logOut io.Writer) (string, error) {
	if err := b.Validate(s); err != nil {
		return "", err
	}
	// xorriso refuses to overwrite; start fresh.
	_ = os.Remove(outPath)

	cmd := exec.CommandContext(ctx, "xorriso", b.Args(s, outPath)...)
	cmd.Stdout = logOut
	cmd.Stderr = logOut
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("xorriso failed: %w", err)
	}
	if _, err := os.Stat(outPath); err != nil {
		return "", fmt.Errorf("xorriso reported success but output missing at %s: %w", outPath, err)
	}
	return outPath, nil
}

func mustExist(base, rel string) error {
	p := filepath.Join(base, rel)
	if _, err := os.Stat(p); err != nil {
		return fmt.Errorf("boot image %s not found: %w", rel, err)
	}
	return nil
}
