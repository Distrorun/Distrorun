// DistroRun Engine — Builds custom Linux ISOs from YAML configurations.
//
// Usage:
//
//	distrorun build <config.yaml> [-o output.iso]
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/talfaza/distrorun/internal/bootloader"
	"github.com/talfaza/distrorun/internal/config"
	"github.com/talfaza/distrorun/internal/disk"
	"github.com/talfaza/distrorun/internal/iso"
	"github.com/talfaza/distrorun/internal/registry"
	"github.com/talfaza/distrorun/internal/rootfs"
	"github.com/talfaza/distrorun/internal/sbom"
	"github.com/talfaza/distrorun/internal/ui"
)

const version = "0.1.0"

func main() {
	if len(os.Args) < 2 {
		ui.PrintUsage(version)
		os.Exit(1)
	}

	switch os.Args[1] {
	case "build":
		runBuild(os.Args[2:])
	case "test":
		runTest(os.Args[2:])
	case "login":
		runLogin(os.Args[2:])
	case "logout":
		runLogout()
	case "push":
		runPush(os.Args[2:])
	case "pull":
		runPull(os.Args[2:])
	case "version":
		ui.PrintBanner(version)
	case "help", "--help", "-h":
		ui.PrintUsage(version)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", os.Args[1])
		ui.PrintUsage(version)
		os.Exit(1)
	}
}

func runBuild(args []string) {
	fs := flag.NewFlagSet("build", flag.ExitOnError)
	output := fs.String("o", "", "Output ISO path (default: <name>.iso)")
	debug := fs.Bool("debug", false, "Show package manager output (dnf/apk)")
	debugShort := fs.Bool("d", false, "Alias for --debug")
	fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: distrorun build <config.yaml> [-o output.iso] [-d|--debug]")
		os.Exit(1)
	}

	rootfs.SetDebug(*debug || *debugShort)

	configPath := fs.Arg(0)

	// Print banner
	buildStart := time.Now()
	ui.PrintBanner(version)

	// Prelude: check root
	if os.Getuid() != 0 {
		ui.Error("This command must be run as root", fmt.Errorf("run with: sudo distrorun build ..."))
	}

	// ── Step 1: Parse config ─────────────────────────────────────────────
	ui.StepHeader(1, 9, "Parsing configuration...")
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		ui.Error("Configuration error", err)
	}
	ui.Info("Config", fmt.Sprintf("%s (base: %s)", cfg.Name, cfg.Distro.Base))
	ui.Info("Packages", strings.Join(cfg.Packages, ", "))
	ui.Info("Users", fmt.Sprintf("%d defined", len(cfg.Users)))

	totalSteps := 8
	if cfg.SBOMEnabled() {
		totalSteps = 9
	}

	// Determine output path — override with -o, default based on output mode
	outputPath := *output
	if outputPath == "" {
		switch cfg.OutputMode() {
		case "disk":
			outputPath = cfg.Name + ".qcow2"
		case "img":
			outputPath = cfg.Name + ".img"
		default:
			outputPath = cfg.Name + ".iso"
		}
	}

	// ── Step 2: Check host dependencies ──────────────────────────────────
	ui.StepHeader(2, totalSteps, "Checking host dependencies...")
	switch cfg.OutputMode() {
	case "disk":
		if err := disk.CheckDiskDeps(); err != nil {
			ui.Error("Missing dependency", err)
		}
	case "img":
		if err := iso.CheckImgDeps(); err != nil {
			ui.Error("Missing dependency", err)
		}
	default:
		if cfg.Distro.Base == "fedora" {
			if err := iso.CheckFedoraDeps(); err != nil {
				ui.Error("Missing dependency", err)
			}
		} else {
			if err := iso.CheckHostDeps(); err != nil {
				ui.Error("Missing dependency", err)
			}
		}
	}
	ui.Success("All dependencies found")

	// ── Step 3: Bootstrap rootfs ─────────────────────────────────────────
	var rfs *rootfs.Rootfs
	if cfg.Distro.Base == "fedora" {
		if cfg.OutputMode() == "disk" {
			ui.StepHeader(3, totalSteps, "Bootstrapping Fedora rootfs (disk mode)...")
			rfs, err = rootfs.BootstrapFedoraDisk(cfg.Name, cfg.Distro.Type)
		} else {
			ui.StepHeader(3, totalSteps, "Bootstrapping Fedora rootfs...")
			rfs, err = rootfs.BootstrapFedora(cfg.Name, cfg.Distro.Type)
		}
	} else {
		ui.StepHeader(3, totalSteps, "Bootstrapping Alpine rootfs...")
		rfs, err = rootfs.Bootstrap(cfg.Name)
	}
	if err != nil {
		ui.Error("Bootstrap failed", err)
	}
	defer rfs.Cleanup(true)
	ui.InfoPath("Rootfs", rfs.Path)

	// ── Step 4: Install packages ─────────────────────────────────────────
	ui.StepHeader(4, totalSteps, "Installing packages...")
	if err := rfs.InstallPackages(cfg.Packages); err != nil {
		ui.Error("Package installation failed", err)
	}
	ui.Success("Packages installed")

	// ── Step 5: Setup users ──────────────────────────────────────────────
	ui.StepHeader(5, totalSteps, "Setting up users...")
	if err := rfs.SetupUsers(cfg.Users); err != nil {
		ui.Error("User setup failed", err)
	}
	// Set hostname to the first user's name
	if len(cfg.Users) > 0 {
		hostname := cfg.Users[0].Name
		os.WriteFile(filepath.Join(rfs.Path, "etc", "hostname"), []byte(hostname+"\n"), 0644)
		ui.Info("Hostname", hostname)
	}
	ui.Success("Users configured (passwords hashed with SHA-512)")

	// ── Step 6: Enable services ──────────────────────────────────────────
	ui.StepHeader(6, totalSteps, "Enabling services...")
	if cfg.Services != nil {
		if err := rfs.EnableServices(cfg.Services.Enable); err != nil {
			ui.Error("Service enablement failed", err)
		}
	}
	ui.Success("Services configured")

	// Track current step
	currentStep := 7

	// ── Step 7 (optional): Generate SBOM ─────────────────────────────────
	if cfg.SBOMEnabled() {
		ui.StepHeader(currentStep, totalSteps, "Generating SBOM (SPDX JSON)...")
		sbomPath := strings.TrimSuffix(outputPath, filepath.Ext(outputPath)) + "-sbom.spdx.json"
		if err := sbom.Generate(rfs.Path, cfg.Name, sbomPath); err != nil {
			ui.Error("SBOM generation failed", err)
		}
		ui.Success("SBOM generated")
		currentStep++
	}

	// ── Step N-1: Setup bootloader / prepare artifact ────────────────────
	// Always unmount and clean rootfs before packaging.
	rfs.Unmount()
	rfs.CleanupRootfs()

	var stagingDir string

	if cfg.OutputMode() == "disk" {
		ui.StepHeader(currentStep, totalSteps, "Building disk image...")
		if err := disk.Build(rfs.Path, outputPath, cfg.DiskSize(), cfg.Name); err != nil {
			ui.Error("Disk build failed", err)
		}
		ui.Success("Disk image built")
	} else {
		ui.StepHeader(currentStep, totalSteps, "Setting up bootloader...")

		stagingDir = filepath.Join(rfs.WorkDir, "staging")
		if err := os.MkdirAll(stagingDir, 0755); err != nil {
			ui.Error("Creating staging directory", err)
		}

		if cfg.Distro.Base == "fedora" {
			kver, vmlinuz, initramfsFile, kErr := rfs.FedoraKernelFiles()
			if kErr != nil {
				ui.Error("Finding Fedora kernel files", kErr)
			}
			kf := bootloader.KernelFiles{
				Version:   kver,
				Vmlinuz:   vmlinuz,
				Initramfs: initramfsFile,
			}
			if err := bootloader.SetupGrub(rfs.Path, stagingDir, kf, cfg.Name); err != nil {
				ui.Error("Bootloader setup failed", err)
			}
		} else {
			if err := bootloader.Setup(rfs.Path, stagingDir); err != nil {
				ui.Error("Bootloader setup failed", err)
			}
		}
		ui.Success("Bootloader configured")
	}
	currentStep++

	// ── Step N: Build final artifact ─────────────────────────────────────
	switch cfg.OutputMode() {
	case "disk":
		// already built above
	case "img":
		ui.StepHeader(currentStep, totalSteps, "Building USB image with persistence...")
		if err := iso.BuildImg(rfs.Path, stagingDir, outputPath, cfg.PersistSize(), cfg.Name); err != nil {
			ui.Error("IMG build failed", err)
		}
	default:
		ui.StepHeader(currentStep, totalSteps, "Building ISO...")
		if cfg.Distro.Base == "fedora" {
			if err := iso.BuildFedora(rfs.Path, stagingDir, outputPath); err != nil {
				ui.Error("ISO build failed", err)
			}
		} else {
			if err := iso.Build(rfs.Path, stagingDir, outputPath); err != nil {
				ui.Error("ISO build failed", err)
			}
		}
	}

	// ── Done ─────────────────────────────────────────────────────────────
	sbomPath := ""
	if cfg.SBOMEnabled() {
		sbomPath = strings.TrimSuffix(outputPath, filepath.Ext(outputPath)) + "-sbom.spdx.json"
	}
	testCmd := "distrorun test " + outputPath + " -r 1024"
	elapsed := time.Since(buildStart)
	ui.PrintSummary(outputPath, sbomPath, testCmd, elapsed)
}

func runTest(args []string) {
	fs := flag.NewFlagSet("test", flag.ExitOnError)
	ram := fs.String("r", "512", "RAM in MB (default: 512)")
	disk := fs.String("d", "", "For ISO: create and attach an extra blank disk of this size (e.g. 8G)")
	fs.Parse(reorderFlagsFirst(args, map[string]bool{"r": true, "d": true}))

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: distrorun test <image> [-r RAM_MB] [-d DISK_SIZE]")
		fmt.Fprintln(os.Stderr, "  <image> is one of: .iso (live), .qcow2 (disk), .img (raw disk)")
		os.Exit(1)
	}

	imagePath := fs.Arg(0)
	ui.PrintBanner(version)

	if _, err := os.Stat(imagePath); os.IsNotExist(err) {
		ui.Error("Image not found", fmt.Errorf("%s does not exist", imagePath))
	}

	qemuBin, err := exec.LookPath("qemu-system-x86_64")
	if err != nil {
		ui.Error("QEMU not found", fmt.Errorf("install with: sudo dnf install qemu-system-x86 (or sudo apt install qemu-system-x86)"))
	}

	ext := strings.ToLower(filepath.Ext(imagePath))

	ui.StepHeader(1, 1, "Launching QEMU...")
	ui.Info("Image", imagePath)
	ui.Info("RAM", *ram+" MB")

	qemuArgs := []string{
		"-m", *ram,
		"-enable-kvm",
	}

	switch ext {
	case ".qcow2":
		qemuArgs = append(qemuArgs,
			"-drive", "file="+imagePath+",format=qcow2,if=virtio",
			"-boot", "c",
		)
		if *disk != "" {
			ui.Warn("-d is ignored when booting a qcow2 disk image")
		}
	case ".img":
		qemuArgs = append(qemuArgs,
			"-drive", "file="+imagePath+",format=raw,if=virtio",
			"-boot", "c",
		)
		if *disk != "" {
			ui.Warn("-d is ignored when booting a raw .img disk image")
		}
	default:
		// Treat anything else (.iso or unknown) as a bootable CD-ROM
		qemuArgs = append(qemuArgs,
			"-cdrom", imagePath,
			"-boot", "d",
		)
		if *disk != "" {
			diskPath := strings.TrimSuffix(imagePath, filepath.Ext(imagePath)) + "-disk.qcow2"

			if _, err := os.Stat(diskPath); os.IsNotExist(err) {
				ui.SubStep("Creating virtual disk: " + *disk)
				createCmd := exec.Command("qemu-img", "create", "-f", "qcow2", diskPath, *disk)
				createCmd.Stderr = os.Stderr
				if err := createCmd.Run(); err != nil {
					ui.Error("Failed to create disk image", err)
				}
			} else {
				ui.SubStep("Using existing disk: " + diskPath)
			}

			ui.Info("Disk", diskPath+" ("+*disk+")")
			qemuArgs = append(qemuArgs, "-hda", diskPath)
		}
	}

	ui.Success("Starting virtual machine...")

	cmd := exec.Command(qemuBin, qemuArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// Check if KVM is unavailable and retry without it
		if strings.Contains(err.Error(), "exit status") {
			ui.Warn("KVM may not be available, retrying without hardware acceleration...")
			// Remove -enable-kvm
			var fallbackArgs []string
			for _, arg := range qemuArgs {
				if arg != "-enable-kvm" {
					fallbackArgs = append(fallbackArgs, arg)
				}
			}
			cmd2 := exec.Command(qemuBin, fallbackArgs...)
			cmd2.Stdin = os.Stdin
			cmd2.Stdout = os.Stdout
			cmd2.Stderr = os.Stderr
			if err2 := cmd2.Run(); err2 != nil {
				ui.Error("QEMU failed", err2)
			}
		}
	}
}

func runLogin(args []string) {
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	server := fs.String("server", "", "Registry server URL (default: http://localhost:3000)")
	fs.Parse(args)

	ui.PrintBanner(version)

	serverURL := *server
	if serverURL == "" {
		serverURL = "http://localhost:3000"
	}

	client := registry.NewClient(serverURL)
	if err := client.Login(); err != nil {
		ui.Error("Login failed", err)
	}
}

func runLogout() {
	ui.PrintBanner(version)

	client := registry.NewClient("")
	if err := client.Logout(); err != nil {
		ui.Error("Logout failed", err)
	}
}

func runPush(args []string) {
	fs := flag.NewFlagSet("push", flag.ExitOnError)
	sbomFile := fs.String("sbom", "", "Path to SBOM file (SPDX JSON)")
	server := fs.String("server", "", "Registry server URL")
	fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: distrorun push <config.yaml> [--sbom <sbom.spdx.json>]")
		os.Exit(1)
	}

	yamlPath := fs.Arg(0)
	ui.PrintBanner(version)

	client := registry.NewClient(*server)
	if err := client.Push(yamlPath, *sbomFile); err != nil {
		ui.Error("Push failed", err)
	}
}

func runPull(args []string) {
	fs := flag.NewFlagSet("pull", flag.ExitOnError)
	server := fs.String("server", "", "Registry server URL")
	fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: distrorun pull <name>")
		os.Exit(1)
	}

	name := fs.Arg(0)
	ui.PrintBanner(version)

	client := registry.NewClient(*server)
	if err := client.Pull(name); err != nil {
		ui.Error("Pull failed", err)
	}
}

// reorderFlagsFirst moves flag args before positional args so Go's flag package
// (which stops parsing at the first non-flag) sees them. valueFlags lists flag
// names that consume the next arg as their value (e.g. {"r": true} for -r 1024).
func reorderFlagsFirst(args []string, valueFlags map[string]bool) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			positional = append(positional, a)
			continue
		}
		name := strings.TrimLeft(a, "-")
		if strings.Contains(name, "=") {
			flags = append(flags, a)
			continue
		}
		if valueFlags[name] && i+1 < len(args) {
			flags = append(flags, a, args[i+1])
			i++
			continue
		}
		flags = append(flags, a)
	}
	return append(flags, positional...)
}
