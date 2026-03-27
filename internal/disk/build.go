// Package disk builds bootable qcow2 disk images from a rootfs directory.
package disk

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/talfaza/distrorun/internal/ui"
)

// grubInstallCandidates handles naming differences across distros.
var grubInstallCandidates = []string{"grub2-install", "grub-install"}
var grubMkconfigCandidates = []string{"grub2-mkconfig", "grub-mkconfig"}

// CheckDiskDeps verifies all host tools required for disk image builds.
func CheckDiskDeps() error {
	tools := []string{"qemu-img", "sfdisk", "losetup", "mkfs.ext4"}
	for _, t := range tools {
		if _, err := exec.LookPath(t); err != nil {
			return fmt.Errorf("required tool not found: %s (install with your package manager)", t)
		}
	}
	if resolvebin(grubInstallCandidates) == "" {
		return fmt.Errorf("grub2-install not found (install grub2-tools or grub-pc)")
	}
	if resolvebin(grubMkconfigCandidates) == "" {
		return fmt.Errorf("grub2-mkconfig not found (install grub2-tools or grub-pc)")
	}
	return nil
}

// Build creates a bootable qcow2 disk image from rootfsPath.
// diskSize is passed directly to qemu-img (e.g. "4G", "8G").
// outputPath should end in .qcow2.
func Build(rootfsPath, outputPath, diskSize string) error {
	workDir := filepath.Dir(rootfsPath) // e.g. /tmp/distrorun-<name>
	rawImg := filepath.Join(workDir, "disk.img")
	mntDir := filepath.Join(workDir, "mnt")

	loopDev := ""
	mntActive := false

	// Cleanup on any failure
	defer func() {
		if mntActive {
			unmountAll(mntDir)
		}
		if loopDev != "" {
			exec.Command("losetup", "-d", loopDev).Run()
		}
		os.Remove(rawImg)
	}()

	// 1. Create raw disk image
	ui.SubStep(fmt.Sprintf("Creating raw disk image (%s)...", diskSize))
	if err := run("qemu-img", "create", "-f", "raw", rawImg, diskSize); err != nil {
		return fmt.Errorf("qemu-img create: %w", err)
	}

	// 2. Partition: single ext4 partition, 1 MB BIOS boot gap for GRUB
	ui.SubStep("Partitioning disk...")
	sfdiskInput := "label: dos\n\nstart=2048, type=83, bootable\n"
	cmd := exec.Command("sfdisk", rawImg)
	cmd.Stdin = strings.NewReader(sfdiskInput)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sfdisk: %w", err)
	}

	// 3. Attach loop device (with partition scan)
	ui.SubStep("Attaching loop device...")
	out, err := exec.Command("losetup", "-fP", "--show", rawImg).Output()
	if err != nil {
		return fmt.Errorf("losetup: %w", err)
	}
	loopDev = strings.TrimSpace(string(out))
	partition := loopDev + "p1"

	// 4. Format partition
	ui.SubStep("Formatting ext4 partition...")
	if err := run("mkfs.ext4", "-L", "DISTRORUN", partition); err != nil {
		return fmt.Errorf("mkfs.ext4: %w", err)
	}

	// 5. Get UUID for fstab
	uuidOut, err := exec.Command("blkid", "-s", "UUID", "-o", "value", partition).Output()
	if err != nil {
		return fmt.Errorf("blkid: %w", err)
	}
	uuid := strings.TrimSpace(string(uuidOut))

	// 6. Mount partition
	if err := os.MkdirAll(mntDir, 0755); err != nil {
		return fmt.Errorf("creating mount dir: %w", err)
	}
	if err := run("mount", partition, mntDir); err != nil {
		return fmt.Errorf("mounting partition: %w", err)
	}
	mntActive = true

	// 7. Copy rootfs
	ui.SubStep("Copying rootfs to disk (this may take a while)...")
	cmd = exec.Command("cp", "-a", rootfsPath+"/.", mntDir+"/")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("copying rootfs: %w", err)
	}

	// 8. Write /etc/fstab
	fstab := fmt.Sprintf("UUID=%s  /  ext4  defaults,errors=remount-ro  0  1\n", uuid)
	if err := os.WriteFile(filepath.Join(mntDir, "etc", "fstab"), []byte(fstab), 0644); err != nil {
		return fmt.Errorf("writing fstab: %w", err)
	}

	// 9. Bind-mount pseudo-filesystems so grub2-install and grub2-mkconfig work
	ui.SubStep("Mounting pseudo-filesystems for GRUB install...")
	for _, m := range []struct{ src, dst, fstype string }{
		{"/proc", filepath.Join(mntDir, "proc"), ""},
		{"/sys", filepath.Join(mntDir, "sys"), ""},
		{"/dev", filepath.Join(mntDir, "dev"), ""},
	} {
		os.MkdirAll(m.dst, 0755)
		if err := run("mount", "--bind", m.src, m.dst); err != nil {
			return fmt.Errorf("bind-mounting %s: %w", m.src, err)
		}
	}
	runDir := filepath.Join(mntDir, "run")
	os.MkdirAll(runDir, 0755)
	if err := run("mount", "-t", "tmpfs", "tmpfs", runDir); err != nil {
		return fmt.Errorf("mounting /run: %w", err)
	}

	// 10. Install GRUB to MBR
	ui.SubStep("Installing GRUB2 to MBR...")
	grubInstall := resolvebin(grubInstallCandidates)
	cmd = exec.Command("chroot", mntDir, grubInstall, "--target=i386-pc", loopDev)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("grub2-install: %w", err)
	}

	// 11. Generate grub.cfg
	ui.SubStep("Generating grub.cfg...")
	grubMkconfig := resolvebin(grubMkconfigCandidates)
	cmd = exec.Command("chroot", mntDir, grubMkconfig, "-o", "/boot/grub2/grub.cfg")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("grub2-mkconfig: %w", err)
	}

	// 12. Unmount everything
	ui.SubStep("Unmounting...")
	unmountAll(mntDir)
	mntActive = false

	// 13. Detach loop device
	if err := run("losetup", "-d", loopDev); err != nil {
		return fmt.Errorf("losetup -d: %w", err)
	}
	loopDev = ""

	// 14. Convert raw → qcow2
	ui.SubStep("Converting to qcow2...")
	if err := run("qemu-img", "convert", "-f", "raw", "-O", "qcow2", rawImg, outputPath); err != nil {
		return fmt.Errorf("qemu-img convert: %w", err)
	}

	os.Remove(rawImg)

	// Make the image readable by the invoking user (build runs as root)
	os.Chmod(outputPath, 0644)

	if info, err := os.Stat(outputPath); err == nil {
		ui.SizeInfo("qcow2", float64(info.Size())/1024/1024)
	}

	return nil
}

// run executes a command, printing stderr to os.Stderr.
func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// resolvebin returns the first binary from candidates found in PATH.
func resolvebin(candidates []string) string {
	for _, c := range candidates {
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
	}
	return ""
}

// unmountAll unmounts everything under dir in reverse (deepest first) order.
func unmountAll(dir string) {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return
	}
	var mps []string
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		mp := fields[1]
		if strings.HasPrefix(mp, dir+"/") || mp == dir {
			mps = append(mps, mp)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(mps)))
	for _, mp := range mps {
		exec.Command("umount", mp).Run()
	}
}
