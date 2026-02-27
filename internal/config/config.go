// Package config handles parsing and validation of DistroRun YAML configuration files.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config is the top-level DistroRun configuration.
type Config struct {
	Version  string    `yaml:"version"`
	Name     string    `yaml:"name"`
	Distro   Distro    `yaml:"distro"`
	Packages []string  `yaml:"packages"`
	Users    []User    `yaml:"users"`
	Services *Services `yaml:"services"`
	Build    *Build    `yaml:"build"`
}

// Distro defines the target operating system.
type Distro struct {
	Base string `yaml:"base"` // "alpine" or "fedora"
	Type string `yaml:"type"` // "server" or "workstation" (fedora only)
}

// User defines a system user to create.
type User struct {
	Name     string `yaml:"name"`
	Password string `yaml:"password"`
}

// Services controls which services are enabled at boot.
type Services struct {
	Enable []string `yaml:"enable"`
}

// Build controls engine behaviour during artifact generation.
type Build struct {
	SBOM bool `yaml:"sbom"`
}

// SBOMEnabled returns true if the user requested SBOM generation.
func (c *Config) SBOMEnabled() bool {
	return c.Build != nil && c.Build.SBOM
}

// LoadConfig reads a YAML file at path and returns a parsed Config.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing YAML: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}
