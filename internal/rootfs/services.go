package rootfs

import (
	"fmt"
	"os/exec"

	"github.com/talfaza/distrorun/internal/ui"
)

// EnableServices activates services to start at boot.
// Uses rc-update for Alpine (OpenRC) and systemctl for Fedora (systemd).
func (r *Rootfs) EnableServices(services []string) error {
	if len(services) == 0 {
		ui.Detail("No services to enable")
		return nil
	}

	for _, svc := range services {
		ui.ServiceItem(svc)
		var cmd *exec.Cmd
		if r.distro == "fedora" {
			cmd = exec.Command("chroot", r.Path, "systemctl", "enable", svc)
		} else {
			cmd = exec.Command("chroot", r.Path, "rc-update", "add", svc, "default")
		}
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("enabling service %s: %w", svc, err)
		}
	}

	return nil
}
