// Package bootloader — GRUB2 BIOS bootloader setup for Fedora ISOs.
package bootloader

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// grub2MkimageCandidates — command name varies by host distro.
var grub2MkimageCandidates = []string{"grub2-mkimage", "grub-mkimage"}

// SetupGrub creates the GRUB2 BIOS bootloader staging directory.
// It copies the kernel and initramfs from the rootfs, generates the El Torito
// boot image with grub2-mkimage, and writes grub.cfg.
func SetupGrub(rootfsPath, stagingDir string, kernelFiles KernelFiles) error {
	grubDir := filepath.Join(stagingDir, "boot", "grub2", "i386-pc")
	bootDir := filepath.Join(stagingDir, "boot")

	if err := os.MkdirAll(grubDir, 0755); err != nil {
		return fmt.Errorf("creating grub dir: %w", err)
	}
	if err := os.MkdirAll(bootDir, 0755); err != nil {
		return fmt.Errorf("creating boot dir: %w", err)
	}

	// Copy kernel
	vmlinuzDst := filepath.Join(bootDir, "vmlinuz-"+kernelFiles.Version)
	if err := copyFile(kernelFiles.Vmlinuz, vmlinuzDst); err != nil {
		return fmt.Errorf("copying vmlinuz: %w", err)
	}

	// Copy initramfs
	initramfsDst := filepath.Join(bootDir, "initramfs-"+kernelFiles.Version+".img")
	if err := copyFile(kernelFiles.Initramfs, initramfsDst); err != nil {
		return fmt.Errorf("copying initramfs: %w", err)
	}

	// Generate El Torito boot image
	elToritoPath := filepath.Join(grubDir, "eltorito.img")
	if err := grub2Mkimage(elToritoPath); err != nil {
		return fmt.Errorf("grub2-mkimage: %w", err)
	}

	// Write grub.cfg
	cfg := grubCfg(kernelFiles.Version)
	if err := os.WriteFile(filepath.Join(stagingDir, "boot", "grub2", "grub.cfg"), []byte(cfg), 0644); err != nil {
		return fmt.Errorf("writing grub.cfg: %w", err)
	}

	return nil
}

// KernelFiles holds the kernel version and absolute paths for vmlinuz and initramfs.
type KernelFiles struct {
	Version   string
	Vmlinuz   string
	Initramfs string
}

// grub2Mkimage runs grub2-mkimage (or grub-mkimage) to produce the El Torito image.
func grub2Mkimage(outputPath string) error {
	bin := findGrub2Mkimage()
	if bin == "" {
		return fmt.Errorf("grub2-mkimage not found (install grub2-tools or grub-common)")
	}

	modules := []string{
		"biosdisk", "part_msdos", "part_gpt",
		"iso9660", "all_video",
		"linux", "normal", "echo", "search", "test",
	}

	args := []string{
		"-O", "i386-pc-eltorito",
		"-o", outputPath,
		"-p", "/boot/grub2",
	}
	args = append(args, modules...)

	cmd := exec.Command(bin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// grubCfg returns the grub.cfg content for live CD boot.
func grubCfg(kver string) string {
	return fmt.Sprintf(`set timeout=5
set default=0

menuentry "DistroRun Live" {
    linux  /boot/vmlinuz-%s quiet selinux=0
    initrd /boot/initramfs-%s.img
}
`, kver, kver)
}

// findGrub2Mkimage searches PATH for the grub2-mkimage binary.
func findGrub2Mkimage() string {
	for _, name := range grub2MkimageCandidates {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

// Grub2MkimageAvailable reports whether grub2-mkimage is available on the host.
func Grub2MkimageAvailable() bool {
	return findGrub2Mkimage() != ""
}
