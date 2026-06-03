package registry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/talfaza/distrorun/internal/config"
	"github.com/talfaza/distrorun/internal/ui"
)

const (
	defaultServer = "http://localhost:3000"
	pollInterval  = 2 * time.Second
	pollTimeout   = 10 * time.Minute
)

// Client talks to the DistroRun registry API.
type Client struct {
	Server string
	Token  string
	http   *http.Client
}

// NewClient creates a registry client from stored credentials.
func NewClient(server string) *Client {
	if server == "" {
		server = defaultServer
	}
	c := &Client{
		Server: server,
		http:   &http.Client{Timeout: 30 * time.Second},
	}

	creds, _ := LoadCredentials()
	if creds != nil {
		c.Token = creds.Token
		c.Server = creds.Server
	}

	return c
}

// ── Login ────────────────────────────────────────────────────────────────────

// deviceCodeResponse from POST /api/cli/token
type deviceCodeResponse struct {
	Code      string `json:"code"`
	ExpiresAt string `json:"expiresAt"`
}

// pollResponse from GET /api/cli/poll
type pollResponse struct {
	Status    string `json:"status"`
	Token     string `json:"token"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	ExpiresAt string `json:"expiresAt"`
}

// Login performs the device-code auth flow.
func (c *Client) Login() error {
	ui.StepHeader(1, 3, "Requesting device code...")

	// Request device code
	resp, err := c.http.Post(c.Server+"/api/cli/token", "application/json", nil)
	if err != nil {
		return fmt.Errorf("failed to connect to %s: %w", c.Server, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("server returned %d", resp.StatusCode)
	}

	var device deviceCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&device); err != nil {
		return fmt.Errorf("parsing response: %w", err)
	}

	ui.Success("Device code generated")

	// Open browser
	ui.StepHeader(2, 3, "Opening browser for authentication...")
	authURL := fmt.Sprintf("%s/cli-auth?code=%s", c.Server, device.Code)

	if err := openBrowser(authURL); err != nil {
		ui.Warn("Could not open browser automatically")
	}

	fmt.Println()
	ui.Info("URL", authURL)
	ui.Info("Code", device.Code)
	fmt.Println()
	ui.SubStep("Waiting for authorization...")

	// Poll for approval
	deadline := time.Now().Add(pollTimeout)
	for time.Now().Before(deadline) {
		time.Sleep(pollInterval)

		pollResp, err := c.http.Get(fmt.Sprintf("%s/api/cli/poll?code=%s", c.Server, device.Code))
		if err != nil {
			continue // retry
		}

		var poll pollResponse
		json.NewDecoder(pollResp.Body).Decode(&poll)
		pollResp.Body.Close()

		switch poll.Status {
		case "approved":
			ui.StepHeader(3, 3, "Authentication successful!")

			// Save credentials
			creds := &Credentials{
				Server:    c.Server,
				Token:     poll.Token,
				Email:     poll.Email,
				Name:      poll.Name,
				ExpiresAt: poll.ExpiresAt,
			}
			if err := SaveCredentials(creds); err != nil {
				return fmt.Errorf("saving credentials: %w", err)
			}

			c.Token = poll.Token

			fmt.Println()
			ui.Success(fmt.Sprintf("Logged in as %s (%s)", poll.Name, poll.Email))

			// Parse and show expiry
			if expires, err := time.Parse(time.RFC3339, poll.ExpiresAt); err == nil {
				days := int(time.Until(expires).Hours() / 24)
				ui.Info("Token expires", fmt.Sprintf("in %d days", days))
			}

			path, _ := credentialsPath()
			ui.InfoPath("Credentials", path)
			return nil

		case "expired":
			return fmt.Errorf("device code expired, please try again")
		}
		// status == "pending" → keep polling
	}

	return fmt.Errorf("login timed out after %v", pollTimeout)
}

// Logout clears stored credentials.
func (c *Client) Logout() error {
	creds, _ := LoadCredentials()
	if creds == nil {
		ui.Warn("No active session found")
		return nil
	}

	if err := ClearCredentials(); err != nil {
		return err
	}

	ui.Success(fmt.Sprintf("Logged out (was %s)", creds.Email))
	return nil
}

// ── Push ─────────────────────────────────────────────────────────────────────

type pushRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	YamlContent string   `json:"yamlContent"`
	SbomContent string   `json:"sbomContent,omitempty"`
	Tags        []string `json:"tags"`
	BaseDistro  string   `json:"baseDistro"`
	Packages    []string `json:"packages"`
	Services    []string `json:"services"`
	UserCount   int      `json:"userCount"`
	SbomEnabled bool     `json:"sbomEnabled"`
}

type pushResponse struct {
	Message   string `json:"message"`
	Version   int    `json:"version"`
	PackageID int    `json:"packageId"`
	Error     string `json:"error"`
}

// Push uploads a YAML config (and optionally an SBOM) to the registry.
func (c *Client) Push(yamlPath, sbomPath string) error {
	if c.Token == "" {
		return fmt.Errorf("not logged in, run: distrorun login")
	}

	totalSteps := 3
	if sbomPath != "" {
		totalSteps = 4
	}

	// Step 1: Read and parse YAML
	ui.StepHeader(1, totalSteps, "Reading configuration...")
	yamlData, err := os.ReadFile(yamlPath)
	if err != nil {
		return fmt.Errorf("reading %s: %w", yamlPath, err)
	}

	cfg, err := config.LoadConfig(yamlPath)
	if err != nil {
		return fmt.Errorf("parsing config: %w", err)
	}

	ui.Info("Name", cfg.Name)
	ui.Info("Base", cfg.Distro.Base)
	ui.Info("Packages", fmt.Sprintf("%d packages", len(cfg.Packages)))

	// Step 2: Read SBOM if provided
	currentStep := 2
	var sbomData string
	if sbomPath != "" {
		ui.StepHeader(currentStep, totalSteps, "Reading SBOM...")
		data, err := os.ReadFile(sbomPath)
		if err != nil {
			return fmt.Errorf("reading SBOM %s: %w", sbomPath, err)
		}
		sbomData = string(data)
		ui.Info("SBOM", sbomPath)
		ui.Success("SBOM loaded")
		currentStep++
	}

	// Prepare tags
	tags := []string{cfg.Distro.Base + " Based"}
	services := []string{}
	if cfg.Services != nil {
		services = cfg.Services.Enable
	}

	// Step N-1: Upload
	ui.StepHeader(currentStep, totalSteps, "Uploading to registry...")

	body := pushRequest{
		Name:        cfg.Name,
		Description: fmt.Sprintf("Custom %s-based Linux distribution", cfg.Distro.Base),
		YamlContent: string(yamlData),
		SbomContent: sbomData,
		Tags:        tags,
		BaseDistro:  cfg.Distro.Base,
		Packages:    cfg.Packages,
		Services:    services,
		UserCount:   len(cfg.Users),
		SbomEnabled: sbomData != "",
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequest("POST", c.Server+"/api/registry", bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("uploading: %w", err)
	}
	defer resp.Body.Close()

	var result pushResponse
	json.NewDecoder(resp.Body).Decode(&result)

	if resp.StatusCode != 200 {
		if result.Error != "" {
			return fmt.Errorf("push failed: %s", result.Error)
		}
		return fmt.Errorf("push failed: HTTP %d", resp.StatusCode)
	}

	currentStep++

	creds, _ := LoadCredentials()
	author := "unknown author"
	if creds != nil {
		author = creds.Name
	}

	// Done
	ui.StepHeader(currentStep, totalSteps, "Published!")
	ui.Success(fmt.Sprintf("Published %s v%d to registry by %s", cfg.Name, result.Version, author))
	ui.Info("Pull command", fmt.Sprintf("distrorun pull %s", cfg.Name))

	return nil
}

// ── Pull ─────────────────────────────────────────────────────────────────────

type pullPackageResponse struct {
	Name          string `json:"name"`
	LatestVersion int    `json:"latestVersion"`
	Version       struct {
		Number      int    `json:"number"`
		YamlContent string `json:"yamlContent"`
		SbomContent string `json:"sbomContent"`
		SbomEnabled bool   `json:"sbomEnabled"`
		BaseDistro  string `json:"baseDistro"`
	} `json:"version"`
	Error string `json:"error"`
}

// Pull downloads a YAML config (and SBOM if available) from the registry.
func (c *Client) Pull(name string) error {
	ui.StepHeader(1, 3, "Fetching package info...")

	resp, err := c.http.Get(fmt.Sprintf("%s/api/registry/%s?download=true", c.Server, name))
	if err != nil {
		return fmt.Errorf("connecting to registry: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return fmt.Errorf("package %q not found in registry", name)
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("registry error: %s", string(body))
	}

	var pkg pullPackageResponse
	if err := json.NewDecoder(resp.Body).Decode(&pkg); err != nil {
		return fmt.Errorf("parsing response: %w", err)
	}

	ui.Info("Package", pkg.Name)
	ui.Info("Version", fmt.Sprintf("v%d", pkg.Version.Number))
	ui.Info("Base", pkg.Version.BaseDistro)

	// Step 2: Write YAML
	ui.StepHeader(2, 3, "Saving files...")
	yamlFile := fmt.Sprintf("%s.distrorun.yaml", name)
	if err := os.WriteFile(yamlFile, []byte(pkg.Version.YamlContent), 0644); err != nil {
		return fmt.Errorf("writing %s: %w", yamlFile, err)
	}
	ui.InfoPath("YAML", yamlFile)

	// Write SBOM if available
	if pkg.Version.SbomEnabled && pkg.Version.SbomContent != "" {
		sbomFile := fmt.Sprintf("%s-sbom.spdx.json", name)
		if err := os.WriteFile(sbomFile, []byte(pkg.Version.SbomContent), 0644); err != nil {
			ui.Warn(fmt.Sprintf("Could not write SBOM: %v", err))
		} else {
			ui.InfoPath("SBOM", sbomFile)
		}
	}

	// Done
	ui.StepHeader(3, 3, "Download complete!")
	ui.Success(fmt.Sprintf("Saved %s v%d", pkg.Name, pkg.Version.Number))

	return nil
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return fmt.Errorf("unsupported platform")
	}
	return cmd.Start()
}
