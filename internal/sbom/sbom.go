// Package sbom generates SPDX 2.3 JSON Software Bill of Materials from the rootfs.
package sbom

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/talfaza/distrorun/internal/ui"
)

// SPDXDocument represents a minimal SPDX 2.3 JSON document.
type SPDXDocument struct {
	SPDXVersion   string             `json:"spdxVersion"`
	DataLicense   string             `json:"dataLicense"`
	SPDXID        string             `json:"SPDXID"`
	Name          string             `json:"name"`
	Namespace     string             `json:"documentNamespace"`
	CreationInfo  SPDXCreationInfo   `json:"creationInfo"`
	Packages      []SPDXPackage      `json:"packages"`
	Relationships []SPDXRelationship `json:"relationships"`
}

// SPDXCreationInfo holds document creation metadata.
type SPDXCreationInfo struct {
	Created  string   `json:"created"`
	Creators []string `json:"creators"`
}

// SPDXPackage represents a single software package in the SBOM.
type SPDXPackage struct {
	SPDXID           string            `json:"SPDXID"`
	Name             string            `json:"name"`
	VersionInfo      string            `json:"versionInfo"`
	Supplier         string            `json:"supplier,omitempty"`
	DownloadLocation string            `json:"downloadLocation"`
	FilesAnalyzed    bool              `json:"filesAnalyzed"`
	ExternalRefs     []SPDXExternalRef `json:"externalRefs,omitempty"`
	PrimaryPurpose   string            `json:"primaryPackagePurpose,omitempty"`
}

// SPDXExternalRef is a package URL reference.
type SPDXExternalRef struct {
	ReferenceCategory string `json:"referenceCategory"`
	ReferenceType     string `json:"referenceType"`
	ReferenceLocator  string `json:"referenceLocator"`
}

// SPDXRelationship describes a relationship between SPDX elements.
type SPDXRelationship struct {
	Element        string `json:"spdxElementId"`
	RelationType   string `json:"relationshipType"`
	RelatedElement string `json:"relatedSpdxElement"`
}

// Generate creates an SPDX 2.3 JSON SBOM from the packages installed in the rootfs.
// Uses Trivy if available (guaranteed compatibility), falls back to apk-based generation.
func Generate(rootfsPath, configName, outputPath string) error {
	// Try Trivy first — produces a perfectly compatible SBOM
	if trivyPath, err := exec.LookPath("trivy"); err == nil {
		return generateWithTrivy(trivyPath, rootfsPath, outputPath)
	}

	// Fallback: generate from apk info
	return generateFromApk(rootfsPath, configName, outputPath)
}

// generateWithTrivy uses `trivy rootfs` to scan the rootfs and produce an SPDX JSON SBOM.
func generateWithTrivy(trivyPath, rootfsPath, outputPath string) error {
	ui.SubStep("Generating SBOM with Trivy...")

	cmd := exec.Command(trivyPath, "rootfs",
		"--format", "spdx-json",
		"--output", outputPath,
		rootfsPath,
	)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("trivy rootfs: %w", err)
	}

	ui.SubStep("SBOM generated (Trivy SPDX 2.3)")
	ui.InfoPath("SBOM", outputPath)
	return nil
}

// generateFromApk builds an SPDX 2.3 JSON SBOM by reading apk package info.
func generateFromApk(rootfsPath, configName, outputPath string) error {
	ui.SubStep("Scanning installed packages (apk)...")

	alpineVersionFull := detectAlpineVersionFull(rootfsPath)
	alpineVersion := detectAlpineVersion(rootfsPath)

	cmd := exec.Command("chroot", rootfsPath, "apk", "info", "-v")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("listing packages: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")

	doc := SPDXDocument{
		SPDXVersion: "SPDX-2.3",
		DataLicense: "CC0-1.0",
		SPDXID:      "SPDXRef-DOCUMENT",
		Name:        fmt.Sprintf("distrorun-%s", configName),
		Namespace:   fmt.Sprintf("https://distrorun.dev/sbom/%s/%d", configName, time.Now().Unix()),
		CreationInfo: SPDXCreationInfo{
			Created:  time.Now().UTC().Format(time.RFC3339),
			Creators: []string{"Tool: DistroRun"},
		},
		Relationships: []SPDXRelationship{
			{
				Element:        "SPDXRef-DOCUMENT",
				RelationType:   "DESCRIBES",
				RelatedElement: "SPDXRef-operating-system",
			},
		},
	}

	doc.Packages = append(doc.Packages, SPDXPackage{
		SPDXID:           "SPDXRef-operating-system",
		Name:             "alpine",
		VersionInfo:      alpineVersionFull,
		Supplier:         "Organization: Alpine Linux",
		DownloadLocation: "https://alpinelinux.org/",
		FilesAnalyzed:    false,
		PrimaryPurpose:   "OPERATING-SYSTEM",
		ExternalRefs: []SPDXExternalRef{
			{
				ReferenceCategory: "PACKAGE-MANAGER",
				ReferenceType:     "purl",
				ReferenceLocator:  fmt.Sprintf("pkg:apk/alpine/alpine-base@%s?arch=x86_64&distro=%s", alpineVersionFull, alpineVersionFull),
			},
		},
	})

	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		name, version := parseApkPackage(line)
		spdxID := fmt.Sprintf("SPDXRef-Package-%d", i)

		pkg := SPDXPackage{
			SPDXID:           spdxID,
			Name:             name,
			VersionInfo:      version,
			Supplier:         "Organization: Alpine Linux",
			DownloadLocation: fmt.Sprintf("https://pkgs.alpinelinux.org/package/v%s/main/x86_64/%s", alpineVersion, name),
			FilesAnalyzed:    false,
			PrimaryPurpose:   "LIBRARY",
			ExternalRefs: []SPDXExternalRef{
				{
					ReferenceCategory: "PACKAGE-MANAGER",
					ReferenceType:     "purl",
					ReferenceLocator:  fmt.Sprintf("pkg:apk/alpine/%s@%s?arch=x86_64&distro=%s", name, version, alpineVersionFull),
				},
			},
		}
		doc.Packages = append(doc.Packages, pkg)

		doc.Relationships = append(doc.Relationships, SPDXRelationship{
			Element:        "SPDXRef-operating-system",
			RelationType:   "CONTAINS",
			RelatedElement: spdxID,
		})
	}

	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling SBOM: %w", err)
	}

	if err := os.WriteFile(outputPath, data, 0644); err != nil {
		return fmt.Errorf("writing SBOM: %w", err)
	}

	ui.SubStep(fmt.Sprintf("SBOM written with %d packages (Alpine %s)", len(lines), alpineVersion))
	ui.InfoPath("SBOM", outputPath)
	return nil
}

// parseApkPackage splits an "apk info -v" line (e.g. "busybox-1.36.1-r1")
// into name and version by finding the last hyphen that precedes a digit.
func parseApkPackage(s string) (name, version string) {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '-' && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9' {
			return s[:i], s[i+1:]
		}
	}
	return s, "unknown"
}

// detectAlpineVersionFull reads /etc/alpine-release and returns the full version (e.g. "3.23.3").
func detectAlpineVersionFull(rootfsPath string) string {
	data, err := os.ReadFile(filepath.Join(rootfsPath, "etc", "alpine-release"))
	if err != nil {
		return "3.21.0"
	}
	return strings.TrimSpace(string(data))
}

// detectAlpineVersion reads /etc/alpine-release from the rootfs.
// Returns the major.minor version (e.g. "3.21").
func detectAlpineVersion(rootfsPath string) string {
	data, err := os.ReadFile(filepath.Join(rootfsPath, "etc", "alpine-release"))
	if err != nil {
		return "3.21" // fallback
	}
	full := strings.TrimSpace(string(data)) // e.g. "3.21.3"
	parts := strings.SplitN(full, ".", 3)
	if len(parts) >= 2 {
		return parts[0] + "." + parts[1] // "3.21"
	}
	return full
}
