package rootfs

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/talfaza/distrorun/internal/ui"
)

// customInit is the init script for live CD booting.
// It mounts the CD-ROM, finds rootfs.squashfs, and creates a writable
// overlay using tmpfs so the system behaves like a normal writable OS.
const customInit = `#!/bin/sh
# DistroRun Live Init — works for ISO (CD-ROM) and IMG (USB/disk) outputs

/bin/busybox --install -s
export PATH=/usr/bin:/bin:/usr/sbin:/sbin

mount -t devtmpfs devtmpfs /dev
mount -t proc proc /proc
mount -t sysfs sysfs /sys

for mod in loop squashfs isofs overlay sr_mod cdrom ata_piix ahci virtio_blk virtio_pci virtio_scsi virtio_net e1000 8139cp 8139too vfat ext4; do
    modprobe $mod 2>/dev/null
done

# ── Find boot device (contains rootfs.squashfs) ──────────────────────────────
echo "DistroRun: Scanning for boot device..."
BOOT_MNT=/media/boot
mkdir -p $BOOT_MNT
SQUASHFS=""
i=0
while [ $i -lt 15 ]; do
    for dev in /dev/sr0 /dev/sda1 /dev/sdb1 /dev/vda1 /dev/nvme0n1p1 /dev/sda /dev/vda; do
        [ -b "$dev" ] || continue
        mount -o ro "$dev" $BOOT_MNT 2>/dev/null || continue
        if [ -f $BOOT_MNT/rootfs.squashfs ]; then
            SQUASHFS=$BOOT_MNT/rootfs.squashfs
            break 2
        fi
        umount $BOOT_MNT 2>/dev/null
    done
    sleep 1
    i=$((i+1))
done

if [ -z "$SQUASHFS" ]; then
    echo "ERROR: rootfs.squashfs not found on any device"
    exec /bin/sh
fi

# ── Mount squashfs as read-only lower layer ───────────────────────────────────
mkdir -p /lower
mount -t squashfs -o ro,loop "$SQUASHFS" /lower

# ── Find persistence partition (labeled DISTRORUN_PERS) ──────────────────────
UPPER=/upper/upper
WORK=/upper/work
for dev in /dev/sda2 /dev/sdb2 /dev/vda2 /dev/nvme0n1p2; do
    [ -b "$dev" ] || continue
    label=$(blkid -s LABEL -o value "$dev" 2>/dev/null)
    if [ "$label" = "DISTRORUN_PERS" ]; then
        echo "DistroRun: Persistence partition found ($dev)"
        mkdir -p /persistent
        mount -t ext4 "$dev" /persistent
        mkdir -p /persistent/upper /persistent/work
        UPPER=/persistent/upper
        WORK=/persistent/work
        break
    fi
done

# ── Create overlay ────────────────────────────────────────────────────────────
if [ "$UPPER" = "/upper/upper" ]; then
    echo "DistroRun: No persistence partition, using tmpfs"
    mkdir -p /upper
    mount -t tmpfs tmpfs /upper
    mkdir -p /upper/upper /upper/work
fi

mkdir -p /sysroot
mount -t overlay overlay -o lowerdir=/lower,upperdir=$UPPER,workdir=$WORK /sysroot

mkdir -p /sysroot/dev /sysroot/proc /sysroot/sys /sysroot/run
mount --move /dev  /sysroot/dev
mount --move /proc /sysroot/proc
mount --move /sys  /sysroot/sys

echo "DistroRun: Switching to root..."
exec switch_root /sysroot /sbin/init
`

// PatchInitramfs replaces the /init script inside the generated initramfs
// with our custom live CD init. The initramfs is a gzip-compressed cpio archive.
func (r *Rootfs) PatchInitramfs() error {
	ui.SubStep("Patching initramfs with live CD init...")

	bootDir := filepath.Join(r.Path, "boot")

	// Find the initramfs file
	initramfsPath := filepath.Join(bootDir, "initramfs-lts")
	if _, err := os.Stat(initramfsPath); os.IsNotExist(err) {
		// Try glob
		matches, _ := filepath.Glob(filepath.Join(bootDir, "initramfs-*"))
		if len(matches) == 0 {
			return fmt.Errorf("initramfs not found in %s", bootDir)
		}
		initramfsPath = matches[0]
	}

	// Create temp working directory
	workDir := filepath.Join(r.WorkDir, "initramfs-work")
	if err := os.MkdirAll(workDir, 0755); err != nil {
		return fmt.Errorf("creating initramfs work dir: %w", err)
	}
	defer os.RemoveAll(workDir)

	// Decompress gzip
	compressedData, err := os.ReadFile(initramfsPath)
	if err != nil {
		return fmt.Errorf("reading initramfs: %w", err)
	}

	cpioPath := filepath.Join(workDir, "initramfs.cpio")

	// Open the gzip stream
	gzFile, err := os.Open(initramfsPath)
	if err != nil {
		return fmt.Errorf("opening initramfs: %w", err)
	}
	defer gzFile.Close()

	gz, err := gzip.NewReader(gzFile)
	if err != nil {
		// Might not be gzip — try as raw cpio
		cpioPath = initramfsPath
		goto extractCpio
	}
	defer gz.Close()

	{
		cpioFile, err := os.Create(cpioPath)
		if err != nil {
			return fmt.Errorf("creating cpio file: %w", err)
		}
		if _, err := io.Copy(cpioFile, gz); err != nil {
			cpioFile.Close()
			return fmt.Errorf("decompressing initramfs: %w", err)
		}
		cpioFile.Close()
	}

extractCpio:
	_ = compressedData // used only for size awareness

	// Extract cpio archive
	extractDir := filepath.Join(workDir, "extracted")
	if err := os.MkdirAll(extractDir, 0755); err != nil {
		return fmt.Errorf("creating extract dir: %w", err)
	}

	cpioIn, err := os.Open(cpioPath)
	if err != nil {
		return fmt.Errorf("opening cpio: %w", err)
	}
	cmd := exec.Command("cpio", "-idm", "--quiet")
	cmd.Dir = extractDir
	cmd.Stdin = cpioIn
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		cpioIn.Close()
		return fmt.Errorf("extracting cpio: %w", err)
	}
	cpioIn.Close()

	// Replace /init with our custom init
	initPath := filepath.Join(extractDir, "init")
	if err := os.WriteFile(initPath, []byte(customInit), 0755); err != nil {
		return fmt.Errorf("writing custom init: %w", err)
	}

	// Repack: cpio | gzip
	newCpioPath := filepath.Join(workDir, "new-initramfs.cpio")

	// Create new cpio archive
	findCmd := exec.Command("sh", "-c",
		fmt.Sprintf("cd %s && find . | cpio -o -H newc --quiet > %s", extractDir, newCpioPath))
	findCmd.Stderr = os.Stderr
	if err := findCmd.Run(); err != nil {
		return fmt.Errorf("creating new cpio: %w", err)
	}

	// Gzip compress
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
