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
	"github.com/talfaza/distrorun/internal/iso"
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
	fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: distrorun build <config.yaml> [-o output.iso]")
		os.Exit(1)
	}

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

	// Determine output path — override with -o, default to <name>.iso
	outputPath := *output
	if outputPath == "" {
		outputPath = cfg.Name + ".iso"
	}

	// ── Step 2: Check host dependencies ──────────────────────────────────
	ui.StepHeader(2, totalSteps, "Checking host dependencies...")
	if err := iso.CheckHostDeps(); err != nil {
		ui.Error("Missing dependency", err)
	}
	ui.Success("All dependencies found")

	// ── Step 3: Bootstrap rootfs ─────────────────────────────────────────
	ui.StepHeader(3, totalSteps, "Bootstrapping Alpine rootfs...")
	rfs, err := rootfs.Bootstrap(cfg.Name)
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

	// ── Step N-1: Setup bootloader ───────────────────────────────────────
	ui.StepHeader(currentStep, totalSteps, "Setting up bootloader...")

	stagingDir := filepath.Join(rfs.WorkDir, "staging")
	if err := os.MkdirAll(stagingDir, 0755); err != nil {
		ui.Error("Creating staging directory", err)
	}

	// Unmount chroot bind mounts before cleaning — CleanupRootfs removes /dev/*
	// which would destroy the HOST's /dev/null if /dev is still bind-mounted!
	rfs.Unmount()

	// Clean rootfs before packaging
	rfs.CleanupRootfs()

	if err := bootloader.Setup(rfs.Path, stagingDir); err != nil {
		ui.Error("Bootloader setup failed", err)
	}
	ui.Success("Bootloader configured")
	currentStep++

	// ── Step N: Build ISO ────────────────────────────────────────────────
	ui.StepHeader(currentStep, totalSteps, "Building ISO...")
	if err := iso.Build(rfs.Path, stagingDir, outputPath); err != nil {
		ui.Error("ISO build failed", err)
	}

	// ── Done ─────────────────────────────────────────────────────────────
	sbomPath := ""
	if cfg.SBOMEnabled() {
		sbomPath = strings.TrimSuffix(outputPath, filepath.Ext(outputPath)) + "-sbom.spdx.json"
	}
	qemuCmd := "qemu-system-x86_64 -cdrom " + outputPath + " -m 512"
	elapsed := time.Since(buildStart)
	ui.PrintSummary(outputPath, sbomPath, qemuCmd, elapsed)
}

func runTest(args []string) {
	fs := flag.NewFlagSet("test", flag.ExitOnError)
	ram := fs.String("r", "512", "RAM in MB (default: 512)")
	disk := fs.String("d", "", "Create and attach a virtual disk of this size (e.g. 8G)")
	fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: distrorun test <iso-file> [-r RAM_MB] [-d DISK_SIZE]")
		os.Exit(1)
	}

	isoPath := fs.Arg(0)
	ui.PrintBanner(version)

	// Check ISO exists
	if _, err := os.Stat(isoPath); os.IsNotExist(err) {
		ui.Error("ISO not found", fmt.Errorf("%s does not exist", isoPath))
	}

	// Check QEMU is installed
	qemuBin, err := exec.LookPath("qemu-system-x86_64")
	if err != nil {
		ui.Error("QEMU not found", fmt.Errorf("install with: sudo dnf install qemu-system-x86 (or sudo apt install qemu-system-x86)"))
	}

	ui.StepHeader(1, 1, "Launching QEMU...")
	ui.Info("ISO", isoPath)
	ui.Info("RAM", *ram+" MB")

	qemuArgs := []string{
		"-cdrom", isoPath,
		"-m", *ram,
		"-boot", "d",
		"-enable-kvm",
	}

	// Create a virtual disk if requested
	if *disk != "" {
		diskPath := strings.TrimSuffix(isoPath, filepath.Ext(isoPath)) + "-disk.qcow2"

		// Create disk image if it doesn't exist
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
