// Package sbom generates SPDX 2.3 JSON Software Bill of Materials from the rootfs.
package sbom

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
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
	DownloadLocation string            `json:"downloadLocation"`
	FilesAnalyzed    bool              `json:"filesAnalyzed"`
	ExternalRefs     []SPDXExternalRef `json:"externalRefs,omitempty"`
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
func Generate(rootfsPath, configName, outputPath string) error {
	fmt.Println("  Scanning installed packages...")

	// Get list of installed packages with versions
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
				RelatedElement: "SPDXRef-rootfs",
			},
		},
	}

	// Add a root package representing the entire rootfs
	doc.Packages = append(doc.Packages, SPDXPackage{
		SPDXID:           "SPDXRef-rootfs",
		Name:             configName,
		VersionInfo:      "1.0",
		DownloadLocation: "NOASSERTION",
		FilesAnalyzed:    false,
	})

	// Parse each "package-name-version" line from apk info -v
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// apk info -v outputs "name-version", split at last hyphen
		name, version := parseApkPackage(line)
		spdxID := fmt.Sprintf("SPDXRef-Package-%d", i)

		pkg := SPDXPackage{
			SPDXID:           spdxID,
			Name:             name,
			VersionInfo:      version,
			DownloadLocation: fmt.Sprintf("https://pkgs.alpinelinux.org/package/edge/main/x86_64/%s", name),
			FilesAnalyzed:    false,
			ExternalRefs: []SPDXExternalRef{
				{
					ReferenceCategory: "PACKAGE-MANAGER",
					ReferenceType:     "purl",
					ReferenceLocator:  fmt.Sprintf("pkg:apk/alpine/%s@%s", name, version),
				},
			},
		}
		doc.Packages = append(doc.Packages, pkg)

		// Relationship: rootfs CONTAINS this package
		doc.Relationships = append(doc.Relationships, SPDXRelationship{
			Element:        "SPDXRef-rootfs",
			RelationType:   "CONTAINS",
			RelatedElement: spdxID,
		})
	}

	// Write to file
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling SBOM: %w", err)
	}

	if err := os.WriteFile(outputPath, data, 0644); err != nil {
		return fmt.Errorf("writing SBOM: %w", err)
	}

	fmt.Printf("  SBOM written to %s (%d packages)\n", outputPath, len(lines))
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
