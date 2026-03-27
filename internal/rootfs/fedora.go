package rootfs

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/talfaza/distrorun/internal/ui"
)

// fedoraServerPackages are the minimum packages for a headless/server Fedora live ISO.
var fedoraServerPackages = []string{
	// Base system
	"fedora-release",
	"systemd",
	"systemd-udev",
	"dracut",
	"kernel",
	"grub2-pc",
	"passwd",
	"shadow-utils",
	"bash",
	"busybox",
	"sudo",
	// Networking
	"NetworkManager",
	"NetworkManager-tui",
	"iproute",   // ip, ss
	"iputils",   // ping
	"net-tools", // netstat, ifconfig
	"bind-utils", // dig, nslookup
	"openssh-server",
	"openssh-clients",
	// Filesystem & storage
	"e2fsprogs",
	"util-linux", // lsblk, fdisk, mount
	// Common tools
	"procps-ng", // ps, top, kill
	"less",
	"tar",
	"gzip",
	"which",
	"dnf",
}

// fedoraWorkstationPackages extend the server base with a graphical desktop.
var fedoraWorkstationPackages = []string{
	"fedora-release",
	"systemd",
	"systemd-udev",
	"dracut",
	"kernel",
	"grub2-pc",
	"passwd",
	"shadow-utils",
	"bash",
	"busybox",
	"NetworkManager",
	"NetworkManager-wifi",
	"e2fsprogs",
	"util-linux",
	"xorg-x11-server-Xorg",
	"gdm",
	"gnome-shell",
	"gnome-terminal",
	"firefox",
	"dnf",
}

// BootstrapFedora creates a new Fedora rootfs using dnf --installroot.
// distroType is "server" (default) or "workstation".
func BootstrapFedora(name, distroType string) (*Rootfs, error) {
	workDir := filepath.Join(os.TempDir(), fmt.Sprintf("distrorun-%s", name))
	rootfsPath := filepath.Join(workDir, "rootfs")

	// Clean up any stale workdir from a previous (possibly failed) build.
	// Unmount first so we don't remove live bind mounts or a partial rootfs
	// that dnf would then consider "already installed".
	if _, err := os.Stat(workDir); err == nil {
		stale := &Rootfs{Path: rootfsPath, WorkDir: workDir}
		stale.Unmount()
		if err := os.RemoveAll(workDir); err != nil {
			return nil, fmt.Errorf("removing stale workdir: %w", err)
		}
	}

	if err := os.MkdirAll(rootfsPath, 0755); err != nil {
		return nil, fmt.Errorf("creating rootfs directory: %w", err)
	}

	r := &Rootfs{
		Path:    rootfsPath,
		WorkDir: workDir,
		arch:    "x86_64",
		distro:  "fedora",
	}

	// Step 1: Mount /proc /dev /sys before dnf --installroot so that RPM
	// %post/%posttrans scriptlets (kernel-core dracut, systemd-udev sysusers,
	// grub2-probe) find the pseudo-filesystems they expect.
	if err := r.setupChrootMounts(); err != nil {
		return nil, err
	}

	// Step 2: Bootstrap rootfs via dnf --installroot
	if err := r.installFedoraBaseSystem(distroType); err != nil {
		return nil, err
	}

	// Step 3: Copy DNS resolution config
	if err := r.copyResolv(); err != nil {
		return nil, err
	}

	// Step 4: Configure networking
	if err := r.configureFedoraNetwork(name); err != nil {
		return nil, err
	}

	// Step 5: Write custom /etc/os-release (reuse Alpine helper)
	r.configureOSRelease(name)

	// Step 6: Generate initramfs via dracut (gzip forced for our patcher)
	if err := r.generateFedoraInitramfs(); err != nil {
		return nil, err
	}

	// Step 7: Patch initramfs with live CD init + busybox
	if err := r.patchFedoraInitramfs(); err != nil {
		return nil, err
	}

	return r, nil
}

// BootstrapFedoraDisk bootstraps a Fedora rootfs suitable for installation onto a raw
// disk image. It skips live-CD initramfs generation and patching — the kernel's
// %posttrans dracut scriptlet already produced a correct initramfs during dnf --installroot.
func BootstrapFedoraDisk(name, distroType string) (*Rootfs, error) {
	workDir := filepath.Join(os.TempDir(), fmt.Sprintf("distrorun-%s", name))
	rootfsPath := filepath.Join(workDir, "rootfs")

	if _, err := os.Stat(workDir); err == nil {
		stale := &Rootfs{Path: rootfsPath, WorkDir: workDir}
		stale.Unmount()
		if err := os.RemoveAll(workDir); err != nil {
			return nil, fmt.Errorf("removing stale workdir: %w", err)
		}
	}

	if err := os.MkdirAll(rootfsPath, 0755); err != nil {
		return nil, fmt.Errorf("creating rootfs directory: %w", err)
	}

	r := &Rootfs{
		Path:    rootfsPath,
		WorkDir: workDir,
		arch:    "x86_64",
		distro:  "fedora",
	}

	if err := r.setupChrootMounts(); err != nil {
		return nil, err
	}
	if err := r.installFedoraBaseSystem(distroType); err != nil {
		return nil, err
	}
	if err := r.copyResolv(); err != nil {
		return nil, err
	}
	if err := r.configureFedoraNetwork(name); err != nil {
		return nil, err
	}
	r.configureOSRelease(name)

	return r, nil
}

// installFedoraBaseSystem bootstraps the Fedora rootfs using dnf --installroot on the host.
func (r *Rootfs) installFedoraBaseSystem(distroType string) error {
	pkgs := fedoraServerPackages
	if distroType == "workstation" {
		pkgs = fedoraWorkstationPackages
	}

	ui.SubStep(fmt.Sprintf("Bootstrapping Fedora %s rootfs via dnf --installroot...", distroType))

	args := []string{
		"install",
		"--installroot", r.Path,
		"--releasever", "40",
		"--use-host-config",
		"--setopt=install_weak_deps=False",
		"--setopt=tsflags=nodocs",
		"--nogpgcheck",
		"-y",
	}
	args = append(args, pkgs...)

	cmd := exec.Command("dnf", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("dnf --installroot: %w", err)
	}

	return nil
}

// configureFedoraNetwork writes a NetworkManager connection for DHCP on the first ethernet interface.
func (r *Rootfs) configureFedoraNetwork(name string) error {
	ui.SubStep("Configuring network (NetworkManager DHCP)...")

	connDir := filepath.Join(r.Path, "etc", "NetworkManager", "system-connections")
	if err := os.MkdirAll(connDir, 0700); err != nil {
		return fmt.Errorf("creating NM connections dir: %w", err)
	}

	conn := `[connection]
id=dhcp
type=ethernet
autoconnect=true

[ipv4]
method=auto

[ipv6]
method=auto
`
	connPath := filepath.Join(connDir, "dhcp.nmconnection")
	if err := os.WriteFile(connPath, []byte(conn), 0600); err != nil {
		return fmt.Errorf("writing NM connection: %w", err)
	}

	// Write hostname
	hostnamePath := filepath.Join(r.Path, "etc", "hostname")
	os.WriteFile(hostnamePath, []byte(name+"\n"), 0644)

	// Enable NetworkManager
	cmd := exec.Command("chroot", r.Path, "systemctl", "enable", "NetworkManager")
	_ = cmd.Run() // best-effort

	return nil
}

// generateFedoraInitramfs runs dracut inside the chroot to produce a gzip-compressed initramfs.
func (r *Rootfs) generateFedoraInitramfs() error {
	ui.SubStep("Generating initramfs via dracut...")

	kver, err := r.fedoraKernelVersion()
	if err != nil {
		return err
	}

	initramfsPath := fmt.Sprintf("/boot/initramfs-%s.img", kver)

	cmd := exec.Command("chroot", r.Path,
		"dracut",
		"--force",
		"--compress=gzip",
		"--no-hostonly",
		"--add-drivers", "squashfs loop iso9660 overlay sr_mod cdrom ata_piix ahci virtio_blk virtio_pci virtio_scsi",
		initramfsPath,
		kver,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("dracut: %w", err)
	}

	return nil
}

// patchFedoraInitramfs extracts the dracut initramfs, injects busybox at /bin/busybox,
// creates essential symlinks, replaces /init with the live-CD init script, and repacks.
func (r *Rootfs) patchFedoraInitramfs() error {
	ui.SubStep("Patching initramfs with live CD init...")

	kver, err := r.fedoraKernelVersion()
	if err != nil {
		return err
	}

	initramfsPath := filepath.Join(r.Path, "boot", fmt.Sprintf("initramfs-%s.img", kver))
	if _, err := os.Stat(initramfsPath); err != nil {
		return fmt.Errorf("initramfs not found at %s: %w", initramfsPath, err)
	}

	workDir := filepath.Join(r.WorkDir, "initramfs-work")
	if err := os.MkdirAll(workDir, 0755); err != nil {
		return fmt.Errorf("creating initramfs work dir: %w", err)
	}
	defer os.RemoveAll(workDir)

	// Decompress gzip
	gzFile, err := os.Open(initramfsPath)
	if err != nil {
		return fmt.Errorf("opening initramfs: %w", err)
	}
	gz, err := gzip.NewReader(gzFile)
	if err != nil {
		gzFile.Close()
		return fmt.Errorf("reading initramfs gzip: %w (was dracut --compress=gzip used?)", err)
	}

	cpioPath := filepath.Join(workDir, "initramfs.cpio")
	cpioFile, err := os.Create(cpioPath)
	if err != nil {
		gz.Close()
		gzFile.Close()
		return fmt.Errorf("creating cpio file: %w", err)
	}
	if _, err := io.Copy(cpioFile, gz); err != nil {
		cpioFile.Close()
		gz.Close()
		gzFile.Close()
		return fmt.Errorf("decompressing initramfs: %w", err)
	}
	cpioFile.Close()
	gz.Close()
	gzFile.Close()

	// Extract cpio archive
	extractDir := filepath.Join(workDir, "extracted")
	if err := os.MkdirAll(extractDir, 0755); err != nil {
		return fmt.Errorf("creating extract dir: %w", err)
	}

	cpioIn, err := os.Open(cpioPath)
	if err != nil {
		return fmt.Errorf("opening cpio: %w", err)
	}
	cpioCmd := exec.Command("cpio", "-idm", "--quiet")
	cpioCmd.Dir = extractDir
	cpioCmd.Stdin = cpioIn
	cpioCmd.Stderr = os.Stderr
	if err := cpioCmd.Run(); err != nil {
		cpioIn.Close()
		return fmt.Errorf("extracting cpio: %w", err)
	}
	cpioIn.Close()

	// Inject busybox into /bin/busybox inside the initramfs
	busyboxSrc := findBusybox(r.Path)
	if busyboxSrc != "" {
		binDir := filepath.Join(extractDir, "bin")
		os.MkdirAll(binDir, 0755)
		if err := copyFilePath(busyboxSrc, filepath.Join(binDir, "busybox")); err == nil {
			os.Chmod(filepath.Join(binDir, "busybox"), 0755)
			// Create symlinks for applets our init script needs
			applets := []string{"sh", "mount", "umount", "modprobe", "sleep", "echo", "cat"}
			for _, a := range applets {
				dst := filepath.Join(binDir, a)
				if _, err := os.Lstat(dst); os.IsNotExist(err) {
					os.Symlink("busybox", dst)
				}
			}
			// switch_root lives in /sbin on most systems
			sbinDir := filepath.Join(extractDir, "sbin")
			os.MkdirAll(sbinDir, 0755)
			if _, err := os.Lstat(filepath.Join(sbinDir, "switch_root")); os.IsNotExist(err) {
				os.Symlink("../bin/busybox", filepath.Join(sbinDir, "switch_root"))
			}
		}
	}

	// Replace /init with our live CD init script
	initPath := filepath.Join(extractDir, "init")
	if err := os.WriteFile(initPath, []byte(customInit), 0755); err != nil {
		return fmt.Errorf("writing custom init: %w", err)
	}

	// Repack: cpio | gzip
	newCpioPath := filepath.Join(workDir, "new-initramfs.cpio")
	packCmd := exec.Command("sh", "-c",
		fmt.Sprintf("cd %s && find . | cpio -o -H newc --quiet > %s", extractDir, newCpioPath))
	packCmd.Stderr = os.Stderr
	if err := packCmd.Run(); err != nil {
		return fmt.Errorf("creating new cpio: %w", err)
	}

	cpioData, err := os.ReadFile(newCpioPath)
	if err != nil {
		return fmt.Errorf("reading new cpio: %w", err)
	}

	outFile, err := os.Create(initramfsPath)
	if err != nil {
		return fmt.Errorf("creating new initramfs: %w", err)
	}
	gzWriter := gzip.NewWriter(outFile)
	if _, err := gzWriter.Write(cpioData); err != nil {
		outFile.Close()
		return fmt.Errorf("compressing initramfs: %w", err)
	}
	gzWriter.Close()
	outFile.Close()

	ui.SubStep("Initramfs patched successfully")
	return nil
}

// fedoraKernelVersion finds the installed kernel version from /lib/modules/ in the rootfs.
func (r *Rootfs) fedoraKernelVersion() (string, error) {
	modulesDir := filepath.Join(r.Path, "lib", "modules")
	entries, err := os.ReadDir(modulesDir)
	if err != nil {
		return "", fmt.Errorf("reading modules directory: %w", err)
	}
	if len(entries) == 0 {
		return "", fmt.Errorf("no kernel modules found in %s", modulesDir)
	}
	// Prefer the highest version (last alphabetically, which sorts by version)
	kver := entries[len(entries)-1].Name()
	return kver, nil
}

// findBusybox searches for the busybox binary inside the rootfs.
func findBusybox(rootfsPath string) string {
	candidates := []string{
		filepath.Join(rootfsPath, "usr", "sbin", "busybox"),
		filepath.Join(rootfsPath, "bin", "busybox"),
		filepath.Join(rootfsPath, "usr", "bin", "busybox"),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// copyFilePath copies src to dst (plain file copy).
func copyFilePath(src, dst string) error {
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
	_, err = io.Copy(out, in)
	return err
}

// FedoraKernelFiles returns the absolute paths of the vmlinuz and initramfs
// inside the rootfs, and the kernel version string. Used by the bootloader.
func (r *Rootfs) FedoraKernelFiles() (kver, vmlinuz, initramfs string, err error) {
	kver, err = r.fedoraKernelVersion()
	if err != nil {
		return
	}

	bootDir := filepath.Join(r.Path, "boot")

	// vmlinuz may be named vmlinuz-<kver> or just vmlinuz
	vmlinuzCandidates := []string{
		filepath.Join(bootDir, "vmlinuz-"+kver),
		filepath.Join(bootDir, "vmlinuz"),
	}
	for _, p := range vmlinuzCandidates {
		if _, e := os.Stat(p); e == nil {
			vmlinuz = p
			break
		}
	}
	if vmlinuz == "" {
		// Glob fallback
		matches, _ := filepath.Glob(filepath.Join(bootDir, "vmlinuz*"))
		if len(matches) > 0 {
			vmlinuz = matches[len(matches)-1]
		}
	}
	if vmlinuz == "" {
		err = fmt.Errorf("vmlinuz not found in %s", bootDir)
		return
	}

	initramfs = filepath.Join(bootDir, fmt.Sprintf("initramfs-%s.img", kver))
	if _, e := os.Stat(initramfs); e != nil {
		matches, _ := filepath.Glob(filepath.Join(bootDir, "initramfs*.img"))
		if len(matches) == 0 {
			err = fmt.Errorf("initramfs not found in %s", bootDir)
			return
		}
		initramfs = matches[len(matches)-1]
	}

	// Strip rootfs prefix so grub.cfg gets paths relative to ISO root
	kver = strings.TrimPrefix(filepath.Base(vmlinuz), "vmlinuz-")
	return
}
