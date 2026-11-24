// Package dockercfg reads and edits credHelpers in a Docker config.json.
package dockercfg

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Path returns the Docker config file: $DOCKER_CONFIG/config.json, else
// ~/.docker/config.json.
func Path(home string) string {
	if d := os.Getenv("DOCKER_CONFIG"); d != "" {
		return filepath.Join(d, "config.json")
	}
	return filepath.Join(home, ".docker", "config.json")
}

// CredHelpers returns the credHelpers map of a config.json.
func CredHelpers(content []byte) (map[string]string, error) {
	if len(bytes.TrimSpace(content)) == 0 {
		return map[string]string{}, nil
	}
	var cfg struct {
		CredHelpers map[string]string `json:"credHelpers"`
	}
	if err := json.Unmarshal(content, &cfg); err != nil {
		return nil, fmt.Errorf("parse docker config: %w", err)
	}
	if cfg.CredHelpers == nil {
		cfg.CredHelpers = map[string]string{}
	}
	return cfg.CredHelpers, nil
}

// SetCredHelpers sets (value non-nil) or deletes (value nil) credHelpers
// entries, keeping every other key of the config. Key order inside the file
// is not preserved (Docker itself rewrites the file the same way).
func SetCredHelpers(content []byte, changes map[string]*string) ([]byte, error) {
	cfg := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(content)) > 0 {
		if err := json.Unmarshal(content, &cfg); err != nil {
			return nil, fmt.Errorf("parse docker config: %w", err)
		}
	}
	helpers, err := CredHelpers(content)
	if err != nil {
		return nil, err
	}
	for host, v := range changes {
		if v == nil {
			delete(helpers, host)
		} else {
			helpers[host] = *v
		}
	}
	if len(helpers) == 0 {
		delete(cfg, "credHelpers")
	} else {
		raw, err := json.Marshal(helpers)
		if err != nil {
			return nil, err
		}
		cfg["credHelpers"] = raw
	}
	out, err := json.MarshalIndent(cfg, "", "\t")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// IsEmpty reports whether a config.json holds nothing but empty objects
// (e.g. {"auths": {}}).
func IsEmpty(content []byte) bool {
	if len(bytes.TrimSpace(content)) == 0 {
		return true
	}
	cfg := map[string]json.RawMessage{}
	if json.Unmarshal(content, &cfg) != nil {
		return false
	}
	for _, v := range cfg {
		if s := string(bytes.TrimSpace(v)); s != "{}" && s != "null" {
			return false
		}
	}
	return true
}
