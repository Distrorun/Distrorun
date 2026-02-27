// Package rootfs handles Alpine Linux rootfs bootstrapping and configuration.
package rootfs

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"
)

// alpine base packages needed for a bootable system
var alpineBasePackages = []string{
	"alpine-base",
	"linux-lts",
	"linux-firmware-none",
	"mkinitfs",
	"openrc",
	"e2fsprogs",
}

// minirootfsURL returns the download URL for the latest Alpine minirootfs.
func minirootfsURL() string {
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x86_64"
	}
	return fmt.Sprintf(
		"https://dl-cdn.alpinelinux.org/alpine/latest-stable/releases/%s/latest-releases.yaml",
		arch,
	)
}

// Rootfs holds the state for a rootfs build.
type Rootfs struct {
	Path    string // absolute path to the rootfs directory
	WorkDir string // parent working directory
	arch    string
}

// Bootstrap creates a new Alpine rootfs by downloading the minirootfs tarball,
// extracting it, setting up chroot mounts, and installing base system packages.
func Bootstrap(name string) (*Rootfs, error) {
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x86_64"
	}

	workDir := filepath.Join(os.TempDir(), fmt.Sprintf("distrorun-%s", name))
	rootfsPath := filepath.Join(workDir, "rootfs")

	if err := os.MkdirAll(rootfsPath, 0755); err != nil {
		return nil, fmt.Errorf("creating rootfs directory: %w", err)
	}

	r := &Rootfs{
		Path:    rootfsPath,
		WorkDir: workDir,
		arch:    arch,
	}

	// Step 1: Download minirootfs tarball
	tarball := filepath.Join(workDir, "minirootfs.tar.gz")
	if err := r.downloadMinirootfs(tarball); err != nil {
		return nil, err
	}

	// Step 2: Extract tarball
	if err := r.extractTarball(tarball); err != nil {
		return nil, err
	}

	// Step 3: Setup chroot mounts
	if err := r.setupChrootMounts(); err != nil {
		return nil, err
	}

	// Step 4: Copy DNS resolution config
	if err := r.copyResolv(); err != nil {
		return nil, err
	}

	// Step 5: Update apk repos and install base packages
	if err := r.installBaseSystem(); err != nil {
		return nil, err
	}

	// Step 6: Configure mkinitfs for live CD and generate initramfs
	if err := r.configureMkinitfs(); err != nil {
		return nil, err
	}
	if err := r.generateInitramfs(); err != nil {
		return nil, err
	}

	// Step 7: Patch initramfs with live CD init script
	if err := r.PatchInitramfs(); err != nil {
		return nil, err
	}

	return r, nil
}

// alpineRelease represents one entry in Alpine's latest-releases.yaml.
type alpineRelease struct {
	Flavor string `yaml:"flavor"`
	File   string `yaml:"file"`
}

// downloadMinirootfs fetches the Alpine minirootfs tarball by first querying
// latest-releases.yaml to discover the current filename dynamically.
func (r *Rootfs) downloadMinirootfs(dest string) error {
	baseURL := fmt.Sprintf(
		"https://dl-cdn.alpinelinux.org/alpine/latest-stable/releases/%s", r.arch)

	// Fetch the releases index to find the minirootfs filename
	releasesURL := baseURL + "/latest-releases.yaml"
	fmt.Printf("  Fetching release index from %s\n", releasesURL)

	resp, err := http.Get(releasesURL)
	if err != nil {
		return fmt.Errorf("fetching releases index: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetching releases index: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading releases index: %w", err)
	}

	var releases []alpineRelease
	if err := yaml.Unmarshal(body, &releases); err != nil {
		return fmt.Errorf("parsing releases index: %w", err)
	}

	// Find the minirootfs entry
	var filename string
	for _, rel := range releases {
		if rel.Flavor == "alpine-minirootfs" {
			filename = rel.File
			break
		}
	}
	if filename == "" {
		return fmt.Errorf("minirootfs entry not found in releases index")
	}

	tarballURL := baseURL + "/" + filename
	fmt.Printf("  Downloading %s\n", tarballURL)

	resp2, err := http.Get(tarballURL)
	if err != nil {
		return fmt.Errorf("downloading minirootfs: %w", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading minirootfs: HTTP %d", resp2.StatusCode)
	}

	f, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("creating tarball file: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(f, resp2.Body); err != nil {
		return fmt.Errorf("writing tarball: %w", err)
	}

	return nil
}

// extractTarball extracts the minirootfs tarball into the rootfs directory.
func (r *Rootfs) extractTarball(tarball string) error {
	fmt.Println("  Extracting minirootfs...")
	cmd := exec.Command("tar", "xzf", tarball, "-C", r.Path)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("extracting tarball: %w", err)
	}
	return nil
}

// setupChrootMounts binds /dev, /proc, /sys into the rootfs for chroot operations.
func (r *Rootfs) setupChrootMounts() error {
	fmt.Println("  Setting up chroot mounts...")

	mounts := []struct {
		fstype string
		src    string
		target string
	}{
		{"proc", "none", filepath.Join(r.Path, "proc")},
		{"", "/dev", filepath.Join(r.Path, "dev")}, // bind mount
		{"", "/sys", filepath.Join(r.Path, "sys")}, // bind mount
	}

	for _, m := range mounts {
		if err := os.MkdirAll(m.target, 0755); err != nil {
			return fmt.Errorf("creating mount point %s: %w", m.target, err)
		}

		var cmd *exec.Cmd
		if m.fstype != "" {
			cmd = exec.Command("mount", "-t", m.fstype, m.src, m.target)
		} else {
			cmd = exec.Command("mount", "--bind", m.src, m.target)
		}
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("mounting %s: %w", m.target, err)
		}
	}

	return nil
}

// copyResolv copies the host's /etc/resolv.conf into the rootfs for DNS resolution.
func (r *Rootfs) copyResolv() error {
	src := "/etc/resolv.conf"
	dest := filepath.Join(r.Path, "etc", "resolv.conf")

	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("reading host resolv.conf: %w", err)
	}

	if err := os.WriteFile(dest, data, 0644); err != nil {
		return fmt.Errorf("writing rootfs resolv.conf: %w", err)
	}

	return nil
}

// installBaseSystem updates apk repositories and installs the base system packages.
func (r *Rootfs) installBaseSystem() error {
	fmt.Println("  Installing base system packages...")

	// Set up repositories
	reposPath := filepath.Join(r.Path, "etc", "apk", "repositories")
	repos := "https://dl-cdn.alpinelinux.org/alpine/latest-stable/main\nhttps://dl-cdn.alpinelinux.org/alpine/latest-stable/community\n"
	if err := os.MkdirAll(filepath.Dir(reposPath), 0755); err != nil {
		return fmt.Errorf("creating apk dir: %w", err)
	}
	if err := os.WriteFile(reposPath, []byte(repos), 0644); err != nil {
		return fmt.Errorf("writing repositories: %w", err)
	}

	// apk update
	cmd := exec.Command("chroot", r.Path, "apk", "update")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("apk update: %w", err)
	}

	// Install base packages
	args := append([]string{r.Path, "apk", "add", "--no-cache"}, alpineBasePackages...)
	cmd = exec.Command("chroot", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("installing base packages: %w", err)
	}

	return nil
}

// configureMkinitfs sets up mkinitfs.conf with features needed for live CD boot.
func (r *Rootfs) configureMkinitfs() error {
	fmt.Println("  Configuring mkinitfs for live CD...")

	// Features needed for live CD: cdrom, scsi, squashfs, loop, virtio
	confPath := filepath.Join(r.Path, "etc", "mkinitfs", "mkinitfs.conf")
	features := `features="ata base cdrom scsi squashfs usb virtio loop"
`
	if err := os.MkdirAll(filepath.Dir(confPath), 0755); err != nil {
		return fmt.Errorf("creating mkinitfs dir: %w", err)
	}
	if err := os.WriteFile(confPath, []byte(features), 0644); err != nil {
		return fmt.Errorf("writing mkinitfs.conf: %w", err)
	}

	return nil
}

// generateInitramfs creates the initramfs using mkinitfs inside the chroot.
func (r *Rootfs) generateInitramfs() error {
	fmt.Println("  Generating initramfs...")

	// Find kernel version from /lib/modules/
	modulesDir := filepath.Join(r.Path, "lib", "modules")
	entries, err := os.ReadDir(modulesDir)
	if err != nil {
		return fmt.Errorf("reading modules directory: %w", err)
	}

	var kernelVersion string
	for _, e := range entries {
		if e.IsDir() && strings.Contains(e.Name(), "lts") {
			kernelVersion = e.Name()
			break
		}
	}
	if kernelVersion == "" && len(entries) > 0 {
		kernelVersion = entries[0].Name()
	}
	if kernelVersion == "" {
		return fmt.Errorf("no kernel modules found in %s", modulesDir)
	}

	cmd := exec.Command("chroot", r.Path, "mkinitfs", kernelVersion)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("mkinitfs: %w", err)
	}

	return nil
}

// ChrootExec runs an arbitrary command inside the rootfs chroot.
func (r *Rootfs) ChrootExec(name string, args ...string) error {
	chrootArgs := append([]string{r.Path, name}, args...)
	cmd := exec.Command("chroot", chrootArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
