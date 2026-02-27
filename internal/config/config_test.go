package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "test.yaml")
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadConfig_Valid(t *testing.T) {
	yaml := `
version: "1.0"
name: test-alpine
distro:
  base: alpine
packages:
  - nginx
  - curl
users:
  - name: root
    password: toor
services:
  enable:
    - nginx
build:
  sbom: true
`
	cfg, err := LoadConfig(writeTemp(t, yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Version != "1.0" {
		t.Errorf("version = %q, want %q", cfg.Version, "1.0")
	}
	if cfg.Name != "test-alpine" {
		t.Errorf("name = %q, want %q", cfg.Name, "test-alpine")
	}
	if cfg.Distro.Base != "alpine" {
		t.Errorf("distro.base = %q, want %q", cfg.Distro.Base, "alpine")
	}
	if len(cfg.Packages) != 2 {
		t.Errorf("packages count = %d, want 2", len(cfg.Packages))
	}
	if len(cfg.Users) != 1 || cfg.Users[0].Name != "root" {
		t.Errorf("unexpected users: %+v", cfg.Users)
	}
	if !cfg.SBOMEnabled() {
		t.Error("SBOMEnabled() = false, want true")
	}
}

func TestLoadConfig_MissingFields(t *testing.T) {
	yaml := `
distro:
  base: alpine
`
	_, err := LoadConfig(writeTemp(t, yaml))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	for _, expected := range []string{"\"version\"", "\"name\"", "at least one user"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("error should mention %s, got: %v", expected, err)
		}
	}
}

func TestLoadConfig_UnsupportedDistro(t *testing.T) {
	yaml := `
version: "1"
name: test
distro:
  base: fedora
  type: server
users:
  - name: root
    password: toor
`
	_, err := LoadConfig(writeTemp(t, yaml))
	if err == nil {
		t.Fatal("expected error for fedora, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported distro") {
		t.Errorf("error should mention unsupported distro, got: %v", err)
	}
}

func TestLoadConfig_EmptyUsers(t *testing.T) {
	yaml := `
version: "1"
name: test
distro:
  base: alpine
users: []
`
	_, err := LoadConfig(writeTemp(t, yaml))
	if err == nil {
		t.Fatal("expected error for empty users, got nil")
	}
	if !strings.Contains(err.Error(), "at least one user") {
		t.Errorf("error should mention users requirement, got: %v", err)
	}
}

func TestLoadConfig_UserMissingPassword(t *testing.T) {
	yaml := `
version: "1"
name: test
distro:
  base: alpine
users:
  - name: root
`
	_, err := LoadConfig(writeTemp(t, yaml))
	if err == nil {
		t.Fatal("expected error for missing password, got nil")
	}
	if !strings.Contains(err.Error(), "\"password\" is required") {
		t.Errorf("error should mention password, got: %v", err)
	}
}
