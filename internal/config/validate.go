package config

import (
	"fmt"
	"strings"
)

// Validate checks all required fields and constraints.
// Returns all errors collected, not just the first one.
func (c *Config) Validate() error {
	var errs []string

	if c.Version == "" {
		errs = append(errs, "\"version\" is required")
	}
	if c.Name == "" {
		errs = append(errs, "\"name\" is required")
	}

	// Distro validation
	if c.Distro.Base == "" {
		errs = append(errs, "\"distro.base\" is required")
	} else if c.Distro.Base != "alpine" && c.Distro.Base != "fedora" {
		errs = append(errs, fmt.Sprintf("unsupported distro base %q: supported values are \"alpine\", \"fedora\"", c.Distro.Base))
	}
	if c.Distro.Base == "fedora" {
		if c.Distro.Type != "" && c.Distro.Type != "server" && c.Distro.Type != "workstation" {
			errs = append(errs, fmt.Sprintf("distro.type %q is invalid: must be \"server\" or \"workstation\"", c.Distro.Type))
		}
	}

	// Users validation
	if len(c.Users) == 0 {
		errs = append(errs, "at least one user must be defined in \"users\"")
	}
	for i, u := range c.Users {
		if u.Name == "" {
			errs = append(errs, fmt.Sprintf("users[%d]: \"name\" is required", i))
		}
		if u.Password == "" {
			errs = append(errs, fmt.Sprintf("users[%d]: \"password\" is required", i))
		}
	}

	if c.Build != nil && c.Build.Output != "" &&
		c.Build.Output != "iso" && c.Build.Output != "disk" {
		errs = append(errs, fmt.Sprintf("build.output %q is invalid: must be \"iso\" or \"disk\"", c.Build.Output))
	}

	if len(errs) > 0 {
		return fmt.Errorf("config validation failed:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}
