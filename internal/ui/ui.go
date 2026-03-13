package ui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// ── Color Palette ────────────────────────────────────────────────────────────

var (
	Teal   = lipgloss.Color("#06b6d4")
	Green  = lipgloss.Color("#22c55e")
	Red    = lipgloss.Color("#ef4444")
	Yellow = lipgloss.Color("#eab308")
	Cyan   = lipgloss.Color("#67e8f9")
	Dim    = lipgloss.Color("#64748b")
	White  = lipgloss.Color("#f8fafc")
	BgDark = lipgloss.Color("#0f172a")
	Purple = lipgloss.Color("#a78bfa")
)

// ── Styles ───────────────────────────────────────────────────────────────────

var (
	// Banner styles
	BannerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(Teal)

	VersionStyle = lipgloss.NewStyle().
			Foreground(Dim)

	// Step header styles
	StepBadgeStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#0f172a")).
			Background(Teal).
			Padding(0, 1)

	StepTextStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(White)

	// Status styles
	SuccessStyle = lipgloss.NewStyle().
			Foreground(Green).
			Bold(true)

	ErrorStyle = lipgloss.NewStyle().
			Foreground(Red).
			Bold(true)

	WarnStyle = lipgloss.NewStyle().
			Foreground(Yellow)

	// Info/detail styles
	LabelStyle = lipgloss.NewStyle().
			Foreground(Dim)

	ValueStyle = lipgloss.NewStyle().
			Foreground(Cyan)

	PathStyle = lipgloss.NewStyle().
			Foreground(Purple).
			Italic(true)

	// Box styles for summary
	SummaryBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(Teal).
			Padding(1, 2).
			MarginTop(1)

	// Usage styles
	CommandStyle = lipgloss.NewStyle().
			Foreground(Teal).
			Bold(true)

	ArgStyle = lipgloss.NewStyle().
			Foreground(Dim).
			Italic(true)
)

// ── Banner ───────────────────────────────────────────────────────────────────

const banner = `
 ██████████    ███           █████                                                      
▒▒███▒▒▒▒███  ▒▒▒           ▒▒███                                                       
 ▒███   ▒▒███ ████   █████  ███████   ████████   ██████  ████████  █████ ████ ████████  
 ▒███    ▒███▒▒███  ███▒▒  ▒▒▒███▒   ▒▒███▒▒███ ███▒▒███▒▒███▒▒███▒▒███ ▒███ ▒▒███▒▒███ 
 ▒███    ▒███ ▒███ ▒▒█████   ▒███     ▒███ ▒▒▒ ▒███ ▒███ ▒███ ▒▒▒  ▒███ ▒███  ▒███ ▒███ 
 ▒███    ███  ▒███  ▒▒▒▒███  ▒███ ███ ▒███     ▒███ ▒███ ▒███      ▒███ ▒███  ▒███ ▒███ 
 ██████████   █████ ██████   ▒▒█████  █████    ▒▒██████  █████     ▒▒████████ ████ █████
▒▒▒▒▒▒▒▒▒▒   ▒▒▒▒▒ ▒▒▒▒▒▒     ▒▒▒▒▒  ▒▒▒▒▒      ▒▒▒▒▒▒  ▒▒▒▒▒       ▒▒▒▒▒▒▒▒ ▒▒▒▒ ▒▒▒▒▒ 
                                                                                        
                                                                                        
                                                                                        `

// PrintBanner prints the DistroRun ASCII art banner with version.
func PrintBanner(version string) {
	fmt.Println(BannerStyle.Render(banner))
	fmt.Println(VersionStyle.Render(fmt.Sprintf("  Custom Linux OS Builder — v%s", version)))
	fmt.Println()
}

// ── Step Progress ────────────────────────────────────────────────────────────

// StepHeader prints a styled step header like: [3/9] Installing packages...
func StepHeader(step, total int, msg string) {
	badge := StepBadgeStyle.Render(fmt.Sprintf(" %d/%d ", step, total))
	text := StepTextStyle.Render(msg)
	fmt.Printf("\n%s %s\n", badge, text)
}

// ── Status Messages ──────────────────────────────────────────────────────────

// Success prints a green checkmark with message.
func Success(msg string) {
	fmt.Println("  " + SuccessStyle.Render("✓") + " " + msg)
}

// Error prints a styled error and exits.
func Error(msg string, err error) {
	errBadge := ErrorStyle.Render(" ERROR ")
	fmt.Fprintf(os.Stderr, "\n%s %s: %v\n\n", errBadge, msg, err)
	os.Exit(1)
}

// Warn prints a yellow warning.
func Warn(msg string) {
	fmt.Println("  " + WarnStyle.Render("⚠") + " " + msg)
}

// ── Info Display ─────────────────────────────────────────────────────────────

// Info prints a labeled value like:  Config: my-alpine-server
func Info(label, value string) {
	fmt.Printf("  %s %s\n", LabelStyle.Render(label+":"), ValueStyle.Render(value))
}

// InfoPath prints a path value.
func InfoPath(label, path string) {
	fmt.Printf("  %s %s\n", LabelStyle.Render(label+":"), PathStyle.Render(path))
}

// ── Sub-step Output (used by internal packages) ──────────────────────────────

var (
	ArrowStyle = lipgloss.NewStyle().
			Foreground(Teal).
			Bold(true)

	SubStepStyle = lipgloss.NewStyle().
			Foreground(White)

	PkgNameStyle = lipgloss.NewStyle().
			Foreground(Cyan).
			Bold(true)

	PkgVersionStyle = lipgloss.NewStyle().
			Foreground(Dim)

	SizeStyle = lipgloss.NewStyle().
			Foreground(Yellow).
			Bold(true)

	DimTextStyle = lipgloss.NewStyle().
			Foreground(Dim)

	UrlStyle = lipgloss.NewStyle().
			Foreground(Dim).
			Italic(true)
)

// SubStep prints a styled sub-step line: ▸ Downloading minirootfs...
func SubStep(msg string) {
	fmt.Printf("  %s %s\n", ArrowStyle.Render("▸"), SubStepStyle.Render(msg))
}

// Detail prints a dimmed detail line.
func Detail(msg string) {
	fmt.Printf("    %s\n", DimTextStyle.Render(msg))
}

// URL prints a styled URL.
func URL(url string) {
	fmt.Printf("    %s\n", UrlStyle.Render(url))
}

// PackageItem prints a styled package with index like: (3/28) nginx 1.26.3-r0
func PackageItem(index, total int, name, version string) {
	counter := DimTextStyle.Render(fmt.Sprintf("(%d/%d)", index, total))
	pkg := PkgNameStyle.Render(name)
	ver := PkgVersionStyle.Render(version)
	fmt.Printf("    %s %s %s\n", counter, pkg, ver)
}

// SizeInfo prints a size with label like: Squashfs size: 179.6 MB
func SizeInfo(label string, sizeMB float64) {
	fmt.Printf("  %s %s %s\n",
		ArrowStyle.Render("▸"),
		LabelStyle.Render(label+":"),
		SizeStyle.Render(fmt.Sprintf("%.1f MB", sizeMB)))
}

// UserItem prints a styled user line.
func UserItem(name, role string) {
	user := PkgNameStyle.Render(name)
	r := PkgVersionStyle.Render("(" + role + ")")
	fmt.Printf("    %s %s %s\n", DimTextStyle.Render("•"), user, r)
}

// ServiceItem prints a styled service line.
func ServiceItem(name string) {
	svc := PkgNameStyle.Render(name)
	fmt.Printf("    %s %s → %s\n", DimTextStyle.Render("•"), svc, DimTextStyle.Render("default runlevel"))
}

// ── Build Summary ────────────────────────────────────────────────────────────

// PrintSummary prints the final build summary in a styled box.
func PrintSummary(isoPath, sbomPath, qemuCmd string, elapsed time.Duration) {
	var lines []string

	// Round to nearest second
	secs := int(elapsed.Seconds())
	mins := secs / 60
	secs = secs % 60
	var timeStr string
	if mins > 0 {
		timeStr = fmt.Sprintf("%dm %ds", mins, secs)
	} else {
		timeStr = fmt.Sprintf("%ds", secs)
	}

	lines = append(lines, SuccessStyle.Render("Build complete!")+"  "+DimTextStyle.Render("in ")+SizeStyle.Render(timeStr))
	lines = append(lines, "")
	lines = append(lines, LabelStyle.Render("ISO  ")+"  "+PathStyle.Render(isoPath))
	if sbomPath != "" {
		lines = append(lines, LabelStyle.Render("SBOM ")+"  "+PathStyle.Render(sbomPath))
	}
	lines = append(lines, "")
	lines = append(lines, LabelStyle.Render("Test:")+"  "+CommandStyle.Render(qemuCmd))

	box := SummaryBoxStyle.Render(strings.Join(lines, "\n"))
	fmt.Println(box)
}

// ── Usage ────────────────────────────────────────────────────────────────────

// PrintUsage prints styled usage information.
func PrintUsage(version string) {
	PrintBanner(version)

	fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(White).Render("Usage:"))
	fmt.Println()
	fmt.Println("  " + CommandStyle.Render("distrorun build") + " " + ArgStyle.Render("<config.yaml>") + " " + ArgStyle.Render("[-o output.iso]"))
	fmt.Println("  " + CommandStyle.Render("distrorun test") + "  " + ArgStyle.Render("<iso-file>") + " " + ArgStyle.Render("[-r RAM_MB] [-d DISK_SIZE]"))
	fmt.Println("  " + CommandStyle.Render("distrorun version"))
	fmt.Println("  " + CommandStyle.Render("distrorun help"))
	fmt.Println()
	fmt.Println(LabelStyle.Render("  The build command must be run as root (uses chroot, mount)."))
	fmt.Println()
}
