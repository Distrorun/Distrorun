package rootfs

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Unmount unmounts all chroot bind mounts (proc, dev, sys).
// Safe to call multiple times.
func (r *Rootfs) Unmount() {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return
	}

	var mountPoints []string
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		mp := fields[1]
		if strings.HasPrefix(mp, r.Path+"/") {
			mountPoints = append(mountPoints, mp)
		}
	}

	// Sort in reverse order to unmount deepest first
	sort.Sort(sort.Reverse(sort.StringSlice(mountPoints)))

	for _, mp := range mountPoints {
		cmd := exec.Command("umount", mp)
		if err := cmd.Run(); err != nil {
			fmt.Printf("  Warning: failed to unmount %s: %v\n", mp, err)
		}
	}
}

// Cleanup unmounts all chroot bind mounts and optionally removes the working directory.
func (r *Rootfs) Cleanup(removeWorkDir bool) {
	fmt.Println("  Unmounting chroot mounts...")
	r.Unmount()

	if removeWorkDir {
		fmt.Printf("  Removing working directory: %s\n", r.WorkDir)
		os.RemoveAll(r.WorkDir)
	}
}

// CleanupRootfs removes unnecessary files from the rootfs before packaging.
// MUST be called AFTER Unmount() — otherwise it would delete host /dev entries.
func (r *Rootfs) CleanupRootfs() error {
	fmt.Println("  Cleaning up rootfs...")

	// Clear apk cache
	cachePath := filepath.Join(r.Path, "var", "cache", "apk")
	os.RemoveAll(cachePath)

	// Clear /dev contents (will be populated at boot by devtmpfs)
	// Only safe because Unmount() was called first
	devPath := filepath.Join(r.Path, "dev")
	entries, _ := os.ReadDir(devPath)
	for _, e := range entries {
		os.RemoveAll(filepath.Join(devPath, e.Name()))
	}

	return nil
}
