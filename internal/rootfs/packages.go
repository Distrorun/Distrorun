package rootfs

import (
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

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

// InstallPackages installs user-specified packages via apk inside the chroot.
func (r *Rootfs) InstallPackages(pkgs []string) error {
	if len(pkgs) == 0 {
		ui.Detail("No additional packages to install")
		return nil
	}

	ui.SubStep(fmt.Sprintf("Installing %d packages...", len(pkgs)))

	args := append([]string{r.Path, "apk", "add", "--no-cache"}, pkgs...)
	cmd := exec.Command("chroot", args...)
	cmd.Stdout = &apkWriter{}
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("apk add %s: %w", strings.Join(pkgs, " "), err)
	}

	return nil
}
