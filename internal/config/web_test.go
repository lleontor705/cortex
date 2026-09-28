package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

func webBoolPtr(v bool) *bool { return &v }

func webConfigEqual(a, b WebConfig) bool {
	return a.IsEnabled() == b.IsEnabled() &&
		a.Host == b.Host &&
		a.Port == b.Port &&
		a.KeyFile == b.KeyFile
}

// TestWebDefaultsRoundTrip proves a default configuration persists no web
// overrides and reloads with identical effective values.
func TestWebDefaultsRoundTrip(t *testing.T) {
	defaults := WebConfig{}

	doc := struct {
		Web WebConfig `yaml:"web"`
	}{Web: defaults}
	data, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal default web config: %v", err)
	}

	var parsed map[string]map[string]any
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal serialized config: %v", err)
	}
	for _, key := range []string{"enabled", "host", "port", "key_file"} {
		if _, ok := parsed["web"][key]; ok {
			t.Errorf("default config must not persist web.%s, got:\n%s", key, data)
		}
	}

	var reloaded struct {
		Web WebConfig `yaml:"web"`
	}
	if err := yaml.Unmarshal(data, &reloaded); err != nil {
		t.Fatalf("reload default web config: %v", err)
	}
	if !webConfigEqual(reloaded.Web, defaults) {
		t.Errorf("default web config did not reload identically: got %+v", reloaded.Web)
	}
	if !reloaded.Web.IsEnabled() {
		t.Error("web must default to enabled")
	}

	effective := reloaded.Web
	effective.SetDefaults(HTTPConfig{Enabled: true, Port: 7438, Host: "localhost"})
	if !effective.IsEnabled() || effective.Port != 7438 || effective.Host != "localhost" {
		t.Errorf("web defaults must inherit the http listener, got %+v", effective)
	}
}

// TestWebOverridesRoundTrip proves explicit overrides persist and reload identically.
func TestWebOverridesRoundTrip(t *testing.T) {
	original := WebConfig{
		Enabled: webBoolPtr(false),
		Host:    "127.0.0.1",
		Port:    9090,
		KeyFile: filepath.Join("custom", "web.key"),
	}

	doc := struct {
		Web WebConfig `yaml:"web"`
	}{Web: original}
	data, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal web overrides: %v", err)
	}

	var reloaded struct {
		Web WebConfig `yaml:"web"`
	}
	if err := yaml.Unmarshal(data, &reloaded); err != nil {
		t.Fatalf("reload web overrides: %v", err)
	}
	if !webConfigEqual(reloaded.Web, original) {
		t.Fatalf("web overrides did not round-trip: got %+v want %+v", reloaded.Web, original)
	}
	if reloaded.Web.IsEnabled() {
		t.Error("explicit enabled=false must survive the round-trip")
	}
}

// TestWebMultiFormatTags proves JSON and TOML tags stay consistent with YAML.
func TestWebMultiFormatTags(t *testing.T) {
	original := WebConfig{Enabled: webBoolPtr(true), Host: "0.0.0.0", Port: 8080, KeyFile: "/tmp/web.key"}

	t.Run("json", func(t *testing.T) {
		doc := struct {
			Web WebConfig `json:"web"`
		}{Web: original}
		data, err := json.Marshal(doc)
		if err != nil {
			t.Fatalf("marshal json: %v", err)
		}
		for _, tag := range []string{`"enabled"`, `"host"`, `"port"`, `"key_file"`} {
			if !strings.Contains(string(data), tag) {
				t.Errorf("json output missing %s: %s", tag, data)
			}
		}
		var reloaded struct {
			Web WebConfig `json:"web"`
		}
		if err := json.Unmarshal(data, &reloaded); err != nil {
			t.Fatalf("unmarshal json: %v", err)
		}
		if !webConfigEqual(reloaded.Web, original) {
			t.Errorf("json round-trip mismatch: got %+v want %+v", reloaded.Web, original)
		}
	})

	t.Run("toml", func(t *testing.T) {
		doc := struct {
			Web WebConfig `toml:"web"`
		}{Web: original}
		data, err := toml.Marshal(doc)
		if err != nil {
			t.Fatalf("marshal toml: %v", err)
		}
		for _, tag := range []string{"enabled", "host", "port", "key_file"} {
			if !strings.Contains(string(data), tag) {
				t.Errorf("toml output missing %s: %s", tag, data)
			}
		}
		var reloaded struct {
			Web WebConfig `toml:"web"`
		}
		if err := toml.Unmarshal(data, &reloaded); err != nil {
			t.Fatalf("unmarshal toml: %v", err)
		}
		if !webConfigEqual(reloaded.Web, original) {
			t.Errorf("toml round-trip mismatch: got %+v want %+v", reloaded.Web, original)
		}
	})
}

// TestWebKeyFileOverrideResolution proves an explicit override wins over the
// default web.key inside the Cortex config directory.
func TestWebKeyFileOverrideResolution(t *testing.T) {
	dir := t.TempDir()
	override := filepath.Join(dir, "custom-web.key")
	if err := os.WriteFile(override, []byte("secret"), 0o600); err != nil {
		t.Fatalf("seed override key file: %v", err)
	}

	cfg := WebConfig{KeyFile: override}
	if got := cfg.ResolveKeyFile(); got != override {
		t.Errorf("key_file override not honored: got %q want %q", got, override)
	}
}

// TestWebKeyFileDefaultResolution proves the fallback is web.key inside the
// Cortex config directory and that blank whitespace is treated as unset.
func TestWebKeyFileDefaultResolution(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	want := filepath.Join(home, ".cortex", DefaultWebKeyFileName)

	if got := (WebConfig{}).ResolveKeyFile(); got != want {
		t.Errorf("default key file path mismatch: got %q want %q", got, want)
	}
	if got := (WebConfig{KeyFile: "   "}).ResolveKeyFile(); got != want {
		t.Errorf("blank key_file must fall back to the default: got %q want %q", got, want)
	}
}

// TestWebSetDefaults proves defaults inherit the http listener while explicit
// overrides are preserved.
func TestWebSetDefaults(t *testing.T) {
	httpCfg := HTTPConfig{Enabled: true, Port: 7438, Host: "localhost"}

	var cfg WebConfig
	cfg.SetDefaults(httpCfg)
	if !cfg.IsEnabled() {
		t.Error("SetDefaults must enable the web namespace by default")
	}
	if cfg.Port != httpCfg.Port || cfg.Host != httpCfg.Host {
		t.Errorf("SetDefaults must inherit the http listener, got %+v", cfg)
	}

	override := WebConfig{Enabled: webBoolPtr(false), Port: 9000, Host: "0.0.0.0"}
	override.SetDefaults(httpCfg)
	if override.IsEnabled() {
		t.Error("SetDefaults must preserve an explicit disabled override")
	}
	if override.Port != 9000 || override.Host != "0.0.0.0" {
		t.Errorf("SetDefaults must preserve explicit overrides, got %+v", override)
	}
}

// TestWebValidateRejectsOutOfRangePort proves range violations fail with a
// deterministic field error.
func TestWebValidateRejectsOutOfRangePort(t *testing.T) {
	for _, port := range []int{-1, 65536, 70000} {
		err := (WebConfig{Port: port}).Validate()
		if err == nil {
			t.Fatalf("port %d must be rejected", port)
		}
		if !strings.Contains(err.Error(), "web.port") {
			t.Errorf("error must name web.port deterministically, got %v", err)
		}
	}
}

// TestWebValidateRejectsHostileHost proves malformed or hostile host values are
// rejected instead of silently accepted.
func TestWebValidateRejectsHostileHost(t *testing.T) {
	for _, host := range []string{"http://evil.example", "localhost:7438", "bad host", "user@host", "trailing-"} {
		err := (WebConfig{Host: host}).Validate()
		if err == nil {
			t.Errorf("host %q must be rejected", host)
		}
		if !strings.Contains(err.Error(), "web.host") {
			t.Errorf("error for %q must name web.host, got %v", host, err)
		}
	}
}

// TestWebValidateAcceptsDefaultsAndValidOverrides proves the zero value and
// legitimate overrides (including IPv6 and range boundaries) are valid.
func TestWebValidateAcceptsDefaultsAndValidOverrides(t *testing.T) {
	cases := []WebConfig{
		{},
		{Enabled: webBoolPtr(false)},
		{Host: "localhost"},
		{Host: "127.0.0.1"},
		{Host: "web.internal.example"},
		{Host: "::1"},
		{Port: 1},
		{Port: 65535},
		{Port: 7438, Host: "0.0.0.0", KeyFile: "/etc/cortex/web.key"},
	}
	for i, cfg := range cases {
		if err := cfg.Validate(); err != nil {
			t.Errorf("case %d (%+v) must be valid: %v", i, cfg, err)
		}
	}
}

// TestWebValidateDoesNotMutate proves validation is pure: a rejected config is
// left untouched and an on-disk file is never rewritten.
func TestWebValidateDoesNotMutate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cortex.yaml")
	content := "web:\n  port: 70000\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("seed config file: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config file: %v", err)
	}
	var doc struct {
		Web WebConfig `yaml:"web"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal config file: %v", err)
	}
	before := doc.Web

	if err := doc.Web.Validate(); err == nil {
		t.Fatal("out-of-range port must fail validation")
	}
	if !reflect.DeepEqual(doc.Web, before) {
		t.Errorf("validation mutated the in-memory config: got %+v want %+v", doc.Web, before)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("re-read config file: %v", err)
	}
	if string(after) != content {
		t.Errorf("validation must not mutate the config file: got %q", after)
	}
}

// TestWebZeroBloatSerialization proves the web namespace only ever serializes
// the four user-settable keys and never server-only or pragma-only fields.
func TestWebZeroBloatSerialization(t *testing.T) {
	full := WebConfig{Enabled: webBoolPtr(false), Host: "127.0.0.1", Port: 8080, KeyFile: "/etc/cortex/web.key"}
	doc := struct {
		Web WebConfig `yaml:"web"`
	}{Web: full}
	data, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal web config: %v", err)
	}

	var parsed map[string]map[string]any
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal web config: %v", err)
	}
	allowed := map[string]bool{"enabled": true, "host": true, "port": true, "key_file": true}
	web := parsed["web"]
	if len(web) != len(allowed) {
		t.Errorf("web namespace must serialize exactly %d keys, got %d:\n%s", len(allowed), len(web), data)
	}
	for key := range web {
		if !allowed[key] {
			t.Errorf("web namespace serialized forbidden key %q", key)
		}
	}
	for _, forbidden := range []string{"tenant", "workspace", "multi_tenant", "dsn", "schema", "pragma", "signing_key", "principal_subject", "storage"} {
		if strings.Contains(string(data), forbidden) {
			t.Errorf("web config leaked server-only field %q:\n%s", forbidden, data)
		}
	}
}
