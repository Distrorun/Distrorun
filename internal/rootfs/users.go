package rootfs

import (
	"fmt"
	"os/exec"

	"github.com/talfaza/distrorun/internal/config"
)

// SetupUsers creates system users and sets their passwords.
// Passwords are hashed by chpasswd (SHA-512 on Alpine) — plain text is never
// stored in the final image.
func (r *Rootfs) SetupUsers(users []config.User) error {
	for _, u := range users {
		fmt.Printf("  Setting up user: %s\n", u.Name)

		if u.Name != "root" {
			// Create user (non-interactively, no password yet)
			cmd := exec.Command("chroot", r.Path, "adduser", "-D", u.Name)
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("creating user %s: %w", u.Name, err)
			}
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
