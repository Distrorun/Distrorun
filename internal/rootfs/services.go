package rootfs

import (
	"fmt"
	"os/exec"
)

// EnableServices activates OpenRC services to start at boot.
func (r *Rootfs) EnableServices(services []string) error {
	if len(services) == 0 {
		fmt.Println("  No services to enable")
		return nil
	}

	for _, svc := range services {
		fmt.Printf("  Enabling service: %s\n", svc)
		cmd := exec.Command("chroot", r.Path, "rc-update", "add", svc, "default")
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("enabling service %s: %w", svc, err)
		}
	}

	return nil
}
