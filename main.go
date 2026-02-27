// DistroRun Engine — Builds custom Alpine Linux ISOs from YAML configurations.
//
// Usage:
//
//	distrorun build <config.yaml> [-o output.iso]
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/talfaza/distrorun/internal/bootloader"
	"github.com/talfaza/distrorun/internal/config"
	"github.com/talfaza/distrorun/internal/iso"
	"github.com/talfaza/distrorun/internal/rootfs"
	"github.com/talfaza/distrorun/internal/sbom"
)

const version = "0.1.0"

// ANSI color helpers
func bold(s string) string   { return "\033[1m" + s + "\033[0m" }
func cyan(s string) string   { return "\033[36m" + s + "\033[0m" }
func green(s string) string  { return "\033[32m" + s + "\033[0m" }
func red(s string) string    { return "\033[31m" + s + "\033[0m" }
func yellow(s string) string { return "\033[33m" + s + "\033[0m" }

func stepHeader(step, total int, msg string) {
	fmt.Printf("\n%s %s\n", bold(cyan(fmt.Sprintf("[%d/%d]", step, total))), bold(msg))
}

func fatal(msg string, err error) {
	fmt.Fprintf(os.Stderr, "\n%s %s: %v\n", bold(red("ERROR")), msg, err)
	os.Exit(1)
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "build":
		runBuild(os.Args[2:])
	case "version":
		fmt.Printf("distrorun %s\n", version)
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(bold("DistroRun") + " — Custom Linux OS Builder")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  distrorun build <config.yaml> [-o output.iso]")
	fmt.Println("  distrorun version")
	fmt.Println("  distrorun help")
	fmt.Println()
	fmt.Println("The build command must be run as root (uses chroot, mount).")
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

	// Prelude: check root
	if os.Getuid() != 0 {
		fmt.Fprintln(os.Stderr, bold(red("ERROR"))+" This command must be run as root (sudo distrorun build ...)")
		os.Exit(1)
	}

	// Determine total steps
	totalSteps := 8 // base steps without SBOM
	// We'll adjust after parsing config

	// ── Step 1: Parse config ─────────────────────────────────────────────
	stepHeader(1, 9, "Parsing configuration...")
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		fatal("Configuration error", err)
	}
	fmt.Printf("  Config: %s (base: %s)\n", green(cfg.Name), cfg.Distro.Base)
	fmt.Printf("  Packages: %s\n", green(strings.Join(cfg.Packages, ", ")))
	fmt.Printf("  Users: %s\n", green(fmt.Sprintf("%d", len(cfg.Users))))

	if cfg.SBOMEnabled() {
		totalSteps = 9
	} else {
		totalSteps = 8
	}

	// Determine output path
	outputPath := *output
	if outputPath == "" {
		outputPath = cfg.Name + ".iso"
	}

	// ── Step 2: Check host dependencies ──────────────────────────────────
	stepHeader(2, totalSteps, "Checking host dependencies...")
	if err := iso.CheckHostDeps(); err != nil {
		fatal("Missing dependency", err)
	}
	fmt.Println("  " + green("✓") + " All dependencies found")

	// ── Step 3: Bootstrap rootfs ─────────────────────────────────────────
	stepHeader(3, totalSteps, "Bootstrapping Alpine rootfs...")
	rfs, err := rootfs.Bootstrap(cfg.Name)
	if err != nil {
		fatal("Bootstrap failed", err)
	}
	defer rfs.Cleanup(true)
	fmt.Printf("  Rootfs at: %s\n", rfs.Path)

	// ── Step 4: Install packages ─────────────────────────────────────────
	stepHeader(4, totalSteps, "Installing packages...")
	if err := rfs.InstallPackages(cfg.Packages); err != nil {
		fatal("Package installation failed", err)
	}
	fmt.Println("  " + green("✓") + " Packages installed")

	// ── Step 5: Setup users ──────────────────────────────────────────────
	stepHeader(5, totalSteps, "Setting up users...")
	if err := rfs.SetupUsers(cfg.Users); err != nil {
		fatal("User setup failed", err)
	}
	fmt.Println("  " + green("✓") + " Users configured (passwords hashed)")

	// ── Step 6: Enable services ──────────────────────────────────────────
	stepHeader(6, totalSteps, "Enabling services...")
	if cfg.Services != nil {
		if err := rfs.EnableServices(cfg.Services.Enable); err != nil {
			fatal("Service enablement failed", err)
		}
	} else {
		fmt.Println("  No services to enable")
	}
	fmt.Println("  " + green("✓") + " Services configured")

	// Track current step
	currentStep := 7

	// ── Step 7 (optional): Generate SBOM ─────────────────────────────────
	if cfg.SBOMEnabled() {
		stepHeader(currentStep, totalSteps, "Generating SBOM (SPDX JSON)...")
		sbomPath := strings.TrimSuffix(outputPath, filepath.Ext(outputPath)) + "-sbom.spdx.json"
		if err := sbom.Generate(rfs.Path, cfg.Name, sbomPath); err != nil {
			fatal("SBOM generation failed", err)
		}
		fmt.Println("  " + green("✓") + " SBOM generated")
		currentStep++
	}

	// ── Step N-1: Setup bootloader ───────────────────────────────────────
	stepHeader(currentStep, totalSteps, "Setting up bootloader...")

	stagingDir := filepath.Join(rfs.WorkDir, "staging")
	if err := os.MkdirAll(stagingDir, 0755); err != nil {
		fatal("Creating staging directory", err)
	}

	// Unmount chroot bind mounts before cleaning — CleanupRootfs removes /dev/*
	// which would destroy the HOST's /dev/null if /dev is still bind-mounted!
	rfs.Unmount()

	// Clean rootfs before packaging
	rfs.CleanupRootfs()

	if err := bootloader.Setup(rfs.Path, stagingDir); err != nil {
		fatal("Bootloader setup failed", err)
	}
	fmt.Println("  " + green("✓") + " Bootloader configured")
	currentStep++

	// ── Step N: Build ISO ────────────────────────────────────────────────
	stepHeader(currentStep, totalSteps, "Building ISO...")
	if err := iso.Build(rfs.Path, stagingDir, outputPath); err != nil {
		fatal("ISO build failed", err)
	}

	// ── Done ─────────────────────────────────────────────────────────────
	fmt.Println()
	fmt.Println(bold(green("✓ Build complete!")))
	fmt.Printf("  ISO: %s\n", bold(outputPath))
	if cfg.SBOMEnabled() {
		sbomPath := strings.TrimSuffix(outputPath, filepath.Ext(outputPath)) + "-sbom.spdx.json"
		fmt.Printf("  SBOM: %s\n", bold(sbomPath))
	}
	fmt.Println()
	fmt.Println(yellow("  Test with: qemu-system-x86_64 -cdrom " + outputPath + " -m 512"))
}
