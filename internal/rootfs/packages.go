package rootfs

import (
	"fmt"
	"os/exec"
	"strings"
)

// InstallPackages installs user-specified packages via apk inside the chroot.
func (r *Rootfs) InstallPackages(pkgs []string) error {
	if len(pkgs) == 0 {
		fmt.Println("  No additional packages to install")
		return nil
	}

	fmt.Printf("  Installing packages: %s\n", strings.Join(pkgs, ", "))

	args := append([]string{r.Path, "apk", "add", "--no-cache"}, pkgs...)
	cmd := exec.Command("chroot", args...)
	cmd.Stdout = nil // suppress apk noise for user packages
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("apk add %s: %w", strings.Join(pkgs, " "), err)
	}

	return nil
}
