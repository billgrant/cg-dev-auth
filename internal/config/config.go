// Package config loads and saves cg-dev-auth's own configuration file.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/billgrant/cg-dev-auth/internal/fsutil"
)

// Ecosystem names accepted in the config.
const (
	EcoNPM    = "npm" // npm, Yarn Classic and Yarn Berry
	EcoPython = "python"
	EcoDocker = "docker"
)

// KnownEcosystems lists every ecosystem name the config accepts.
var KnownEcosystems = []string{EcoNPM, EcoPython, EcoDocker}

// LibrariesHost is the Chainguard Libraries registry host.
const LibrariesHost = "libraries.cgr.dev"

// Config is the on-disk configuration.
type Config struct {
	// Org is the Chainguard organization, e.g. example.com. Only Docker
	// (cgr.dev/ORG/...) and named Libraries repos need it; the default
	// Libraries URLs are the same for every org.
	Org string `yaml:"org"`
	// Ecosystems to manage.
	Ecosystems []string `yaml:"ecosystems"`
	// PythonIndex is "python" or "python-remediated".
	PythonIndex string `yaml:"python_index"`
	// LibrariesRepo optionally names a Libraries repository (REPO, under Org)
	// instead of the org's default repository.
	LibrariesRepo string `yaml:"libraries_repo,omitempty"`
	// RefreshInterval is how often the background agent refreshes tokens.
	RefreshInterval Duration `yaml:"refresh_interval"`
}

// Default returns a config with defaults filled in for org.
func Default(org string) *Config {
	return &Config{
		Org:             org,
		Ecosystems:      []string{EcoNPM, EcoPython, EcoDocker},
		PythonIndex:     "python",
		RefreshInterval: Duration(5 * time.Minute),
	}
}

// Validate reports the first problem with c.
func (c *Config) Validate() error {
	if strings.TrimSpace(c.Org) == "" {
		return errors.New("org is required (run: cg-dev-auth init --org YOUR_ORG)")
	}
	for _, e := range c.Ecosystems {
		if !slices.Contains(KnownEcosystems, e) {
			return fmt.Errorf("unknown ecosystem %q (known: %s)", e, strings.Join(KnownEcosystems, ", "))
		}
	}
	switch c.PythonIndex {
	case "python", "python-remediated":
	default:
		return fmt.Errorf("python_index must be python or python-remediated, got %q", c.PythonIndex)
	}
	if strings.Contains(c.LibrariesRepo, "/") {
		return fmt.Errorf("libraries_repo is the repository name only (the org comes from org), got %q", c.LibrariesRepo)
	}
	if c.RefreshInterval.D() < time.Minute {
		return fmt.Errorf("refresh_interval must be at least 1m, got %s", c.RefreshInterval.D())
	}
	return nil
}

// Enabled reports whether ecosystem eco is managed.
func (c *Config) Enabled(eco string) bool { return slices.Contains(c.Ecosystems, eco) }

// JavaScriptRegistry returns the npm registry URL, with a trailing slash.
func (c *Config) JavaScriptRegistry() string { return c.librariesURL("javascript") }

// PythonIndexURL returns the Python simple index URL, with a trailing slash.
func (c *Config) PythonIndexURL() string { return c.librariesURL(c.PythonIndex) + "simple/" }

func (c *Config) librariesURL(eco string) string {
	if c.LibrariesRepo != "" {
		return fmt.Sprintf("https://%s/%s/~/%s/%s/", LibrariesHost, eco, c.Org, c.LibrariesRepo)
	}
	return fmt.Sprintf("https://%s/%s/", LibrariesHost, eco)
}

// Load reads the config at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("no config at %s (run: cg-dev-auth init --org YOUR_ORG)", path)
	}
	if err != nil {
		return nil, err
	}
	c := Default("")
	if err := yaml.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// Save writes c to path.
func Save(path string, c *Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	header := "# cg-dev-auth configuration. Change org to point this machine at another organization.\n"
	return fsutil.WriteFileAtomic(path, append([]byte(header), data...), 0o644)
}

// Dir returns cg-dev-auth's config directory: $CG_DEV_AUTH_HOME, else
// $XDG_CONFIG_HOME/cg-dev-auth, else ~/.config/cg-dev-auth.
func Dir(home string) string {
	if d := os.Getenv("CG_DEV_AUTH_HOME"); d != "" {
		return d
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "cg-dev-auth")
	}
	return filepath.Join(home, ".config", "cg-dev-auth")
}

// Duration is a time.Duration that marshals as a string like "20m".
type Duration time.Duration

// D returns the time.Duration.
func (d Duration) D() time.Duration { return time.Duration(d) }

// MarshalYAML implements yaml.Marshaler.
func (d Duration) MarshalYAML() (any, error) { return time.Duration(d).String(), nil }

// UnmarshalYAML implements yaml.Unmarshaler.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	v, err := time.ParseDuration(n.Value)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", n.Value, err)
	}
	*d = Duration(v)
	return nil
}
