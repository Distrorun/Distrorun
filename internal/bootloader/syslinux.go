// Package bootloader handles syslinux/isolinux bootloader setup for ISO images.
package bootloader

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// syslinux file search paths (varies by distro)
var syslinuxSearchPaths = []string{
	"/usr/lib/syslinux",
	"/usr/lib/syslinux/modules/bios",
	"/usr/share/syslinux",
	"/usr/lib/ISOLINUX",
}

// required syslinux/isolinux files
var requiredFiles = []string{
	"isolinux.bin",
	"ldlinux.c32",
}

// optional syslinux modules (nice to have for menus, but not strictly required)
var optionalFiles = []string{
	"libcom32.c32",
	"libutil.c32",
	"menu.c32",
}

// isolinuxCfgTemplate is the boot configuration.
const isolinuxCfgTemplate = `DEFAULT linux
PROMPT 0
TIMEOUT 30

LABEL linux
    KERNEL /boot/vmlinuz-lts
    INITRD /boot/initramfs-lts
    APPEND quiet
`

// Setup creates the bootloader staging directory with all required files.
// It copies kernel, initramfs, isolinux binaries, and writes isolinux.cfg.
func Setup(rootfsPath, stagingDir string) error {
	isolinuxDir := filepath.Join(stagingDir, "isolinux")
	bootDir := filepath.Join(stagingDir, "boot")

	if err := os.MkdirAll(isolinuxDir, 0755); err != nil {
		return fmt.Errorf("creating isolinux dir: %w", err)
	}
	if err := os.MkdirAll(bootDir, 0755); err != nil {
		return fmt.Errorf("creating boot dir: %w", err)
	}

	// Copy required syslinux files
	for _, name := range requiredFiles {
		src := findFile(name)
		if src == "" {
			return fmt.Errorf("required syslinux file not found: %s (searched: %v)", name, syslinuxSearchPaths)
		}
		if err := copyFile(src, filepath.Join(isolinuxDir, name)); err != nil {
			return fmt.Errorf("copying %s: %w", name, err)
		}
	}

	// Copy optional syslinux files (non-fatal if missing)
	for _, name := range optionalFiles {
		src := findFile(name)
		if src != "" {
			copyFile(src, filepath.Join(isolinuxDir, name))
		}
	}

	// Copy kernel and initramfs from rootfs /boot/
	kernelFiles := map[string]string{
		"vmlinuz-lts":   "vmlinuz-lts",
		"initramfs-lts": "initramfs-lts",
	}

	rootfsBoot := filepath.Join(rootfsPath, "boot")
	for src, dst := range kernelFiles {
		srcPath := filepath.Join(rootfsBoot, src)
		dstPath := filepath.Join(bootDir, dst)

		// Try to find the actual file (might have a version suffix)
		if _, err := os.Stat(srcPath); os.IsNotExist(err) {
			// Look for files matching the pattern
			matches, _ := filepath.Glob(filepath.Join(rootfsBoot, src+"*"))
			if len(matches) == 0 {
				return fmt.Errorf("kernel file not found: %s in %s", src, rootfsBoot)
			}
			srcPath = matches[0]
		}

		if err := copyFile(srcPath, dstPath); err != nil {
			return fmt.Errorf("copying kernel file %s: %w", src, err)
		}
	}

	// Write isolinux.cfg
	cfgPath := filepath.Join(isolinuxDir, "isolinux.cfg")
	if err := os.WriteFile(cfgPath, []byte(isolinuxCfgTemplate), 0644); err != nil {
		return fmt.Errorf("writing isolinux.cfg: %w", err)
	}

	return nil
}

// IsohdpfxPath returns the path to isohdpfx.bin for isohybrid MBR.
func IsohdpfxPath() string {
	paths := []string{
		"/usr/lib/syslinux/isohdpfx.bin",
		"/usr/lib/syslinux/bios/isohdpfx.bin",
		"/usr/share/syslinux/isohdpfx.bin",
		"/usr/lib/ISOLINUX/isohdpfx.bin",
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// findFile searches for a syslinux file in known paths.
func findFile(name string) string {
	for _, dir := range syslinuxSearchPaths {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// copyFile copies src to dst.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
