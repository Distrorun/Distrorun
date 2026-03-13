package rootfs

import (
	"fmt"
	"os/exec"

	"github.com/talfaza/distrorun/internal/ui"
)

// EnableServices activates OpenRC services to start at boot.
func (r *Rootfs) EnableServices(services []string) error {
	if len(services) == 0 {
		ui.Detail("No services to enable")
		return nil
	}

	for _, svc := range services {
		ui.ServiceItem(svc)
		cmd := exec.Command("chroot", r.Path, "rc-update", "add", svc, "default")
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("enabling service %s: %w", svc, err)
		}
	}

	return nil
}
