package config

import (
	"fmt"
	"net"
	"path/filepath"
	"strings"
)

// DefaultWebKeyFileName is the embedded web credential file name inside the
// Cortex config directory.
const DefaultWebKeyFileName = "web.key"

// WebConfig is the self-contained web.* namespace for the embedded web UI. It
// is a sibling of the HTTP namespace and intentionally carries no server-only
// or multi-tenant parameters, so local config files stay Zero-Bloat.
//
// The zero value carries no overrides and resolves to the defaults: the web UI
// is enabled, it inherits the HTTP listener host/port, and its credential lives
// at <Cortex config dir>/web.key. Enabled is a pointer so an explicit false is
// distinguishable from "unset" while the default (true) is never written back
// into a saved config file.
type WebConfig struct {
	Enabled *bool  `yaml:"enabled,omitempty" json:"enabled,omitempty" toml:"enabled,omitempty" mapstructure:"enabled"`
	Host    string `yaml:"host,omitempty" json:"host,omitempty" toml:"host,omitempty" mapstructure:"host"`
	Port    int    `yaml:"port,omitempty" json:"port,omitempty" toml:"port,omitempty" mapstructure:"port"`
	KeyFile string `yaml:"key_file,omitempty" json:"key_file,omitempty" toml:"key_file,omitempty" mapstructure:"key_file"`
}

// DefaultWebKeyFile returns the default web credential path inside the Cortex
// config directory.
func DefaultWebKeyFile() string {
	return filepath.Join(CortexDir(), DefaultWebKeyFileName)
}

// IsEnabled reports whether the embedded web UI is enabled. The default is true;
// only an explicit false override disables it.
func (w WebConfig) IsEnabled() bool {
	return w.Enabled == nil || *w.Enabled
}

// SetDefaults materializes effective values for unset overrides using the HTTP
// listener as the inheritance source. Persist the override struct before calling
// SetDefaults: code-owned defaults through struct tags with omitempty must never
// reach a config file.
func (w *WebConfig) SetDefaults(httpCfg HTTPConfig) {
	if w.Enabled == nil {
		enabled := true
		w.Enabled = &enabled
	}
	if w.Port == 0 {
		w.Port = httpCfg.Port
	}
	if w.Host == "" {
		w.Host = httpCfg.Host
	}
}

// ResolveKeyFile returns the configured key_file override, or the default
// web.key path inside the Cortex config directory when unset or blank.
func (w WebConfig) ResolveKeyFile() string {
	if override := strings.TrimSpace(w.KeyFile); override != "" {
		return override
	}
	return DefaultWebKeyFile()
}

// Validate checks the web namespace in isolation. It never mutates the receiver
// or the filesystem; a failure returns a deterministic field error.
func (w WebConfig) Validate() error {
	if w.Port != 0 && (w.Port < 1 || w.Port > 65535) {
		return fmt.Errorf("invalid web.port: %d (must be 1-65535)", w.Port)
	}
	if err := validateWebHost(w.Host); err != nil {
		return err
	}
	if strings.ContainsRune(w.KeyFile, '\x00') {
		return fmt.Errorf("invalid web.key_file: must not contain NUL bytes")
	}
	return nil
}

// validateWebHost accepts an empty host (inherit http.host), IP literals, and
// bare hostnames. It rejects scheme, path, userinfo, port, whitespace, and
// malformed labels so a hostile listener override fails fast instead of reaching
// the network layer.
func validateWebHost(host string) error {
	if host == "" {
		return nil
	}
	if strings.TrimSpace(host) != host {
		return fmt.Errorf("invalid web.host: %q must not contain surrounding whitespace", host)
	}
	if strings.Contains(host, "://") || strings.ContainsAny(host, "/@") {
		return fmt.Errorf("invalid web.host: %q must be a bare host without scheme, path, or userinfo", host)
	}
	if net.ParseIP(host) != nil {
		return nil
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("invalid web.host: %q is not a valid hostname", host)
		}
		for _, r := range label {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			default:
				return fmt.Errorf("invalid web.host: %q is not a valid hostname", host)
			}
		}
	}
	return nil
}
