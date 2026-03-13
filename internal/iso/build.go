// Package iso assembles the final ISO image from rootfs and bootloader staging.
package iso

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/talfaza/distrorun/internal/bootloader"
	"github.com/talfaza/distrorun/internal/ui"
)

// Build creates the final bootable ISO image.
// It creates a squashfs from the rootfs, then uses xorriso to produce the ISO.
func Build(rootfsPath, stagingDir, outputPath string) error {
	// Step 1: Create squashfs image from rootfs
	squashfsPath := filepath.Join(stagingDir, "rootfs.squashfs")
	ui.SubStep("Creating squashfs image (xz compression)...")

	cmd := exec.Command("mksquashfs", rootfsPath, squashfsPath,
		"-comp", "xz", "-no-xattrs", "-noappend")
	cmd.Stdout = nil
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("mksquashfs: %w", err)
	}

	// Print squashfs size
	if info, err := os.Stat(squashfsPath); err == nil {
		ui.SizeInfo("Squashfs", float64(info.Size())/1024/1024)
	}

	// Step 2: Build ISO with xorriso
	ui.SubStep("Assembling ISO image...")

	xorrisoArgs := []string{
		"-as", "mkisofs",
		"-o", outputPath,
		"-b", "isolinux/isolinux.bin",
		"-c", "isolinux/boot.cat",
		"-no-emul-boot",
		"-boot-load-size", "4",
		"-boot-info-table",
	}

	// Add isohybrid MBR if available (makes ISO bootable from USB too)
	isohdpfx := bootloader.IsohdpfxPath()
	if isohdpfx != "" {
		xorrisoArgs = append(xorrisoArgs, "-isohybrid-mbr", isohdpfx)
	}

	xorrisoArgs = append(xorrisoArgs, stagingDir)

	cmd = exec.Command("xorriso", xorrisoArgs...)
	cmd.Stdout = nil
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("xorriso: %w", err)
	}

	// Print ISO size
	if info, err := os.Stat(outputPath); err == nil {
		ui.SizeInfo("ISO", float64(info.Size())/1024/1024)
	}

	return nil
}

// CheckHostDeps verifies that all required host tools are installed.
func CheckHostDeps() error {
	tools := []string{"xorriso", "mksquashfs"}

	for _, tool := range tools {
		if _, err := exec.LookPath(tool); err != nil {
			return fmt.Errorf("required tool not found: %s (install with your package manager)", tool)
		}
	}

	// Check for syslinux files
	if bootloader.IsohdpfxPath() == "" {
		ui.Warn("isohdpfx.bin not found — ISO will not be USB bootable")
	}

	return nil
}
