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
	} else if c.Distro.Base != "alpine" {
		errs = append(errs, fmt.Sprintf("unsupported distro base %q: only \"alpine\" is supported in this version", c.Distro.Base))
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

	if len(errs) > 0 {
		return fmt.Errorf("config validation failed:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}
