package rootfs

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/talfaza/distrorun/internal/ui"
)

// apkLineRegex parses lines like "( 3/28) Installing nginx (1.26.3-r0)"
var apkLineRegex = regexp.MustCompile(`^\(\s*(\d+)/(\d+)\)\s+Installing\s+(\S+)\s+\(([^)]+)\)`)

// apkWriter is a custom io.Writer that parses apk output and renders it styled.
type apkWriter struct {
	buf bytes.Buffer
}

func (w *apkWriter) Write(p []byte) (int, error) {
	w.buf.Write(p)

	for {
		line, err := w.buf.ReadString('\n')
		if err != nil {
			// Put back the incomplete line
			w.buf.WriteString(line)
			break
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		matches := apkLineRegex.FindStringSubmatch(line)
		if matches != nil {
			idx, _ := strconv.Atoi(matches[1])
			total, _ := strconv.Atoi(matches[2])
			name := matches[3]
			version := matches[4]
			ui.PackageItem(idx, total, name, version)
		}
		// Skip non-install lines (triggers, OK messages, etc.)
	}

	return len(p), nil
}

// InstallPackages installs user-specified packages inside the chroot.
// Uses apk for Alpine and dnf for Fedora.
func (r *Rootfs) InstallPackages(pkgs []string) error {
	if len(pkgs) == 0 {
		ui.Detail("No additional packages to install")
		return nil
	}

	ui.SubStep(fmt.Sprintf("Installing %d packages...", len(pkgs)))

	if r.distro == "fedora" {
		// Run dnf from the host with --installroot — dnf is not installed inside the chroot.
		args := []string{
			"install",
			"--installroot", r.Path,
			"--releasever", "43",
			"--use-host-config",
			"--setopt=install_weak_deps=False",
			"--setopt=tsflags=nodocs",
			"--nogpgcheck",
			"-y",
		}
		if !debugOutput {
			args = append(args, "-q")
		}
		args = append(args, pkgs...)
		cmd := exec.Command("dnf", args...)

		var stderrBuf bytes.Buffer
		if debugOutput {
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
		} else {
			cmd.Stderr = &stderrBuf
			// Detach controlling TTY so dnf5/librepo can't draw its download
			// progress bar over our spinner via /dev/tty.
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		}

		var sp *ui.Spinner
		if !debugOutput {
			sp = ui.NewSpinner(fmt.Sprintf("dnf installing %d user packages...", len(pkgs)))
			sp.Start()
		}
		err := cmd.Run()
		if sp != nil {
			sp.Stop()
		}
		if err != nil {
			if stderrBuf.Len() > 0 {
				return fmt.Errorf("dnf install %s: %w\n%s", strings.Join(pkgs, " "), err, stderrBuf.String())
			}
			return fmt.Errorf("dnf install %s: %w", strings.Join(pkgs, " "), err)
		}
		return nil
	}

	args := append([]string{r.Path, "apk", "add", "--no-cache"}, pkgs...)
	cmd := exec.Command("chroot", args...)

	var stderrBuf bytes.Buffer
	if debugOutput {
		// Debug: show apk's per-package install lines, styled.
		cmd.Stdout = &apkWriter{}
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stderr = &stderrBuf
	}

	var sp *ui.Spinner
	if !debugOutput {
		sp = ui.NewSpinner(fmt.Sprintf("apk installing %d user packages...", len(pkgs)))
		sp.Start()
	}
	err := cmd.Run()
	if sp != nil {
		sp.Stop()
	}
	if err != nil {
		if stderrBuf.Len() > 0 {
			return fmt.Errorf("apk add %s: %w\n%s", strings.Join(pkgs, " "), err, stderrBuf.String())
		}
		return fmt.Errorf("apk add %s: %w", strings.Join(pkgs, " "), err)
	}

	return nil
}
