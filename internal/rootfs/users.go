package rootfs

import (
	"fmt"
	"os/exec"

	"github.com/talfaza/distrorun/internal/config"
	"github.com/talfaza/distrorun/internal/ui"
)

// SetupUsers creates system users and sets their passwords.
// Passwords are hashed by chpasswd (SHA-512 on Alpine) — plain text is never
// stored in the final image.
func (r *Rootfs) SetupUsers(users []config.User) error {
	for _, u := range users {
		if u.Name == "root" {
			ui.UserItem(u.Name, "password update")
		} else {
			ui.UserItem(u.Name, "new user")
		}

		if u.Name != "root" {
			var cmd *exec.Cmd
			if r.distro == "fedora" {
				// useradd is the standard tool on Fedora/systemd distros
				cmd = exec.Command("chroot", r.Path, "useradd", "-m", "-s", "/bin/bash", u.Name)
			} else {
				// adduser is Alpine's BusyBox variant
				cmd = exec.Command("chroot", r.Path, "adduser", "-D", "-s", "/bin/bash", u.Name)
			}
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("creating user %s: %w", u.Name, err)
			}
		} else {
			// Set root's shell to bash (best-effort on both distros)
			cmd := exec.Command("chroot", r.Path, "sed", "-i", `s|^root:(.*):/bin/sh$|root:\1:/bin/bash|`, "/etc/passwd")
			_ = cmd.Run()
		}

		// Set password via chpasswd (hashes with SHA-512)
		cmd := exec.Command("chroot", r.Path, "sh", "-c",
			fmt.Sprintf("echo '%s:%s' | chpasswd", u.Name, u.Password))
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("setting password for %s: %w", u.Name, err)
		}
	}

	return nil
}
