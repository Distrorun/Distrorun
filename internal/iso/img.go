package iso

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/talfaza/distrorun/internal/bootloader"
	"github.com/talfaza/distrorun/internal/ui"
)

// BuildImg creates a raw disk image with two partitions:
//   - Partition 1 (ext2, labeled DISTRORUN_BOOT): squashfs + kernel + initramfs + GRUB
//   - Partition 2 (ext4, labeled DISTRORUN_PERS): empty persistence storage
//
// The image can be written to USB with dd or booted directly in QEMU with -hda.
// title is the human-readable name shown in the GRUB menu.
func BuildImg(rootfsPath, stagingDir, outputPath, persistSize, title string) error {
	workDir := filepath.Dir(stagingDir)
	rawImg := filepath.Join(workDir, "disk.img")
	mntBoot := filepath.Join(workDir, "mnt-boot")

	loopDev := ""
	bootMounted := false

	defer func() {
		if bootMounted {
			imgUnmountAll(mntBoot)
		}
		if loopDev != "" {
			exec.Command("losetup", "-d", loopDev).Run()
		}
		os.Remove(rawImg)
	}()

	// ── Squashfs ─────────────────────────────────────────────────────────
	squashfsPath := filepath.Join(stagingDir, "rootfs.squashfs")
	ui.SubStep("Creating squashfs image (xz compression)...")
	cmd := exec.Command("mksquashfs", rootfsPath, squashfsPath,
		"-comp", "xz", "-no-xattrs", "-noappend")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("mksquashfs: %w", err)
	}
	if info, err := os.Stat(squashfsPath); err == nil {
		ui.SizeInfo("Squashfs", float64(info.Size())/1024/1024)
	}

	// ── Size calculation ─────────────────────────────────────────────────
	// Boot partition = squashfs + kernel + initramfs + 512 MB overhead for GRUB
	bootSizeBytes := int64(512 * 1024 * 1024)
	for _, p := range []string{squashfsPath, filepath.Join(stagingDir, "boot")} {
		s, _ := dirSize(p)
		bootSizeBytes += s
	}
	// Round up to next 100 MB
	bootSizeMB := (bootSizeBytes/(1024*1024)/100 + 1) * 100

	persistBytes, err := parseSize(persistSize)
	if err != nil {
		return fmt.Errorf("invalid persist_size %q: %w", persistSize, err)
	}
	persistMB := persistBytes / (1024 * 1024)

	totalMB := bootSizeMB + persistMB + 10 // 10 MB for MBR/alignment

	// ── Create raw image ─────────────────────────────────────────────────
	ui.SubStep(fmt.Sprintf("Creating raw image (%d MB boot + %d MB persistence)...", bootSizeMB, persistMB))
	if err := imgRun("qemu-img", "create", "-f", "raw", rawImg, fmt.Sprintf("%dM", totalMB)); err != nil {
		return fmt.Errorf("qemu-img create: %w", err)
	}

	// ── Partition ────────────────────────────────────────────────────────
	// Partition 1: bootable, bootSizeMB sectors (1 sector = 512 bytes but we work in MiB)
	// Partition 2: rest for persistence
	sfdiskScript := fmt.Sprintf(
		"label: dos\n\nsize=%dMiB, type=83, bootable\ntype=83\n",
		bootSizeMB,
	)
	partCmd := exec.Command("sfdisk", rawImg)
	partCmd.Stdin = strings.NewReader(sfdiskScript)
	partCmd.Stderr = os.Stderr
	if err := partCmd.Run(); err != nil {
		return fmt.Errorf("sfdisk: %w", err)
	}

	// ── Loop device ──────────────────────────────────────────────────────
	out, err := exec.Command("losetup", "-fP", "--show", rawImg).Output()
	if err != nil {
		return fmt.Errorf("losetup: %w", err)
	}
	loopDev = strings.TrimSpace(string(out))
	bootPart := loopDev + "p1"
	persPart := loopDev + "p2"

	// ── Format partitions ────────────────────────────────────────────────
	ui.SubStep("Formatting partitions...")
	if err := imgRun("mkfs.ext2", "-L", "DISTRORUN_BOOT", bootPart); err != nil {
		return fmt.Errorf("mkfs.ext2: %w", err)
	}
	if err := imgRun("mkfs.ext4", "-L", "DISTRORUN_PERS", persPart); err != nil {
		return fmt.Errorf("mkfs.ext4: %w", err)
	}

	// ── Mount boot partition ─────────────────────────────────────────────
	os.MkdirAll(mntBoot, 0755)
	if err := imgRun("mount", bootPart, mntBoot); err != nil {
		return fmt.Errorf("mounting boot partition: %w", err)
	}
	bootMounted = true

	// ── Copy boot files ──────────────────────────────────────────────────
	ui.SubStep("Copying boot files...")
	// squashfs
	if err := imgCopyFile(squashfsPath, filepath.Join(mntBoot, "rootfs.squashfs")); err != nil {
		return fmt.Errorf("copying squashfs: %w", err)
	}
	// kernel + initramfs from staging/boot/
	bootSrcDir := filepath.Join(stagingDir, "boot")
	entries, err := os.ReadDir(bootSrcDir)
	if err != nil {
		return fmt.Errorf("reading staging boot dir: %w", err)
	}
	os.MkdirAll(filepath.Join(mntBoot, "boot"), 0755)
	for _, e := range entries {
		src := filepath.Join(bootSrcDir, e.Name())
		dst := filepath.Join(mntBoot, "boot", e.Name())
		if e.IsDir() {
			if err := imgCopyDir(src, dst); err != nil {
				return fmt.Errorf("copying %s: %w", e.Name(), err)
			}
		} else {
			if err := imgCopyFile(src, dst); err != nil {
				return fmt.Errorf("copying %s: %w", e.Name(), err)
			}
		}
	}

	// ── Install GRUB ─────────────────────────────────────────────────────
	ui.SubStep("Installing GRUB to MBR...")

	// Bind-mount /dev so grub2-install can probe devices
	os.MkdirAll(filepath.Join(mntBoot, "dev"), 0755)
	os.MkdirAll(filepath.Join(mntBoot, "proc"), 0755)
	os.MkdirAll(filepath.Join(mntBoot, "sys"), 0755)
	for _, bind := range []string{"/dev", "/proc", "/sys"} {
		imgRun("mount", "--bind", bind, filepath.Join(mntBoot, strings.TrimPrefix(bind, "/")))
	}

	grubDir := filepath.Join(mntBoot, "boot", "grub2")
	os.MkdirAll(grubDir, 0755)

	grubInstall := imgResolve([]string{"grub2-install", "grub-install"})
	cmd = exec.Command(grubInstall,
		"--target=i386-pc",
		"--boot-directory="+filepath.Join(mntBoot, "boot"),
		loopDev,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("grub2-install: %w", err)
	}

	// ── Write grub.cfg ───────────────────────────────────────────────────
	// Find kernel version from staged boot files
	kver := imgFindKver(bootSrcDir)
	grubCfgContent := bootloader.ImgGrubCfg(kver, title)
	if err := os.WriteFile(filepath.Join(grubDir, "grub.cfg"), []byte(grubCfgContent), 0644); err != nil {
		return fmt.Errorf("writing grub.cfg: %w", err)
	}

	// ── Unmount ──────────────────────────────────────────────────────────
	imgUnmountAll(mntBoot)
	bootMounted = false

	if err := imgRun("losetup", "-d", loopDev); err != nil {
		return fmt.Errorf("losetup -d: %w", err)
	}
	loopDev = ""

	// ── Move raw image to output path ────────────────────────────────────
	if err := os.Rename(rawImg, outputPath); err != nil {
		// Rename may fail across filesystems — fall back to copy
		if err2 := imgCopyFile(rawImg, outputPath); err2 != nil {
			return fmt.Errorf("writing output image: %w", err2)
		}
		os.Remove(rawImg)
	}

	os.Chmod(outputPath, 0644)

	if info, err := os.Stat(outputPath); err == nil {
		ui.SizeInfo("IMG", float64(info.Size())/1024/1024)
	}

	return nil
}

// CheckImgDeps verifies tools required for img builds.
func CheckImgDeps() error {
	tools := []string{"qemu-img", "sfdisk", "losetup", "mkfs.ext2", "mkfs.ext4", "mksquashfs"}
	for _, t := range tools {
		if _, err := exec.LookPath(t); err != nil {
			return fmt.Errorf("required tool not found: %s", t)
		}
	}
	if imgResolve([]string{"grub2-install", "grub-install"}) == "" {
		return fmt.Errorf("grub2-install not found (install grub2-tools)")
	}
	return nil
}

// ── helpers ──────────────────────────────────────────────────────────────────

func imgRun(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func imgResolve(candidates []string) string {
	for _, c := range candidates {
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
	}
	return ""
}

func imgUnmountAll(dir string) {
	data, _ := os.ReadFile("/proc/mounts")
	var mps []string
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		if strings.HasPrefix(f[1], dir+"/") || f[1] == dir {
			mps = append(mps, f[1])
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(mps)))
	for _, mp := range mps {
		exec.Command("umount", mp).Run()
	}
}

func imgCopyFile(src, dst string) error {
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
	buf := make([]byte, 4*1024*1024)
	_, err = copyBuf(in, out, buf)
	return err
}

func copyBuf(r, w interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
}, buf []byte) (int64, error) {
	var total int64
	for {
		n, err := r.Read(buf)
		if n > 0 {
			wn, werr := w.Write(buf[:n])
			total += int64(wn)
			if werr != nil {
				return total, werr
			}
		}
		if err != nil {
			if err.Error() == "EOF" {
				return total, nil
			}
			return total, err
		}
	}
}

func imgCopyDir(src, dst string) error {
	cmd := exec.Command("cp", "-a", src, dst)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func dirSize(path string) (int64, error) {
	var total int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if len(s) == 0 {
		return 2 * 1024 * 1024 * 1024, nil
	}
	suffix := strings.ToUpper(s[len(s)-1:])
	numStr := s[:len(s)-1]
	var n int64
	if _, err := fmt.Sscanf(numStr, "%d", &n); err != nil {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	switch suffix {
	case "G":
		return n * 1024 * 1024 * 1024, nil
	case "M":
		return n * 1024 * 1024, nil
	default:
		return 0, fmt.Errorf("unsupported size suffix %q (use G or M)", suffix)
	}
}

func imgFindKver(bootDir string) string {
	entries, _ := os.ReadDir(bootDir)
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "vmlinuz-") {
			return strings.TrimPrefix(name, "vmlinuz-")
		}
	}
	return ""
}
