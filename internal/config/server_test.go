package config

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// llmEnvNames is every CORTEX_LLM_* variable consumed by ServerLLMFromEnv.
var llmEnvNames = []string{
	"CORTEX_LLM_PROVIDER",
	"CORTEX_LLM_BASE_URL",
	"CORTEX_LLM_MODEL",
	"CORTEX_LLM_API_KEY",
	"CORTEX_LLM_ALLOWED_HOSTS",
	"CORTEX_LLM_ALLOWED_PORTS",
	"CORTEX_LLM_ALLOW_LOOPBACK",
	"CORTEX_LLM_ALLOW_LOOPBACK_HTTP",
	"CORTEX_LLM_MAX_CONCURRENT",
	"CORTEX_LLM_MAX_REDIRECTS",
	"CORTEX_LLM_MAX_RESPONSE_BODY_BYTES",
	"CORTEX_LLM_MAX_ERROR_BODY_BYTES",
	"CORTEX_LLM_TIMEOUT",
	"CORTEX_LLM_CA_FILE",
}

// setLLMEnv sets the given variables and clears every other CORTEX_LLM_*
// variable so tests are hermetic.
func setLLMEnv(t *testing.T, values map[string]string) {
	t.Helper()
	for _, name := range llmEnvNames {
		if _, ok := values[name]; ok {
			continue
		}
		setenvForTest(t, name, "")
	}
	for name, value := range values {
		setenvForTest(t, name, value)
	}
}

func setenvForTest(t *testing.T, name, value string) {
	t.Helper()
	previous, had := os.LookupEnv(name)
	if err := os.Setenv(name, value); err != nil {
		t.Fatalf("setenv %s: %v", name, err)
	}
	if had {
		t.Cleanup(func() { _ = os.Setenv(name, previous) })
	} else {
		t.Cleanup(func() { _ = os.Unsetenv(name) })
	}
}

func TestServerLLMFromEnvAbsentConfigIsValidAndUnconfigured(t *testing.T) {
	setLLMEnv(t, nil)
	cfg, err := ServerLLMFromEnv()
	if err != nil {
		t.Fatalf("absent config must be valid: %v", err)
	}
	if cfg.Configured() {
		t.Fatal("absent config must not be Configured (heuristic-only operation)")
	}
}

func TestServerLLMFromEnvMapsValidConfiguration(t *testing.T) {
	setLLMEnv(t, map[string]string{
		"CORTEX_LLM_PROVIDER":                "generic",
		"CORTEX_LLM_BASE_URL":                "https://api.example.test/v1",
		"CORTEX_LLM_MODEL":                   "test-model",
		"CORTEX_LLM_API_KEY":                 "sk-admin-canary",
		"CORTEX_LLM_ALLOWED_HOSTS":           " Extra.Test , other.test ",
		"CORTEX_LLM_ALLOWED_PORTS":           "8443",
		"CORTEX_LLM_MAX_CONCURRENT":          "8",
		"CORTEX_LLM_MAX_REDIRECTS":           "2",
		"CORTEX_LLM_MAX_RESPONSE_BODY_BYTES": "1048576",
		"CORTEX_LLM_MAX_ERROR_BODY_BYTES":    "2048",
		"CORTEX_LLM_TIMEOUT":                 "30s",
	})
	cfg, err := ServerLLMFromEnv()
	if err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	if !cfg.Configured() {
		t.Fatal("configured provider must report Configured")
	}
	if cfg.Provider != "generic" || cfg.BaseURL != "https://api.example.test/v1" || cfg.Model != "test-model" {
		t.Fatalf("provider/url/model not mapped: %+v", cfg)
	}
	if cfg.APIKey != "sk-admin-canary" {
		t.Fatal("api key not mapped from environment")
	}
	if len(cfg.AllowedHosts) != 2 || cfg.AllowedHosts[0] != "extra.test" || cfg.AllowedHosts[1] != "other.test" {
		t.Fatalf("allowed hosts not normalized: %v", cfg.AllowedHosts)
	}
	if len(cfg.AllowedPorts) != 1 || cfg.AllowedPorts[0] != 8443 {
		t.Fatalf("allowed ports not mapped: %v", cfg.AllowedPorts)
	}
	if cfg.MaxConcurrent != 8 || cfg.MaxRedirects != 2 || cfg.MaxResponseBodyBytes != 1048576 || cfg.MaxErrorBodyBytes != 2048 {
		t.Fatalf("bounds not mapped: %+v", cfg)
	}
	if cfg.Timeout != 30*time.Second {
		t.Fatalf("timeout not mapped: %s", cfg.Timeout)
	}
}

func TestServerLLMFromEnvAppliesDefaultsForUnsetBounds(t *testing.T) {
	setLLMEnv(t, map[string]string{"CORTEX_LLM_BASE_URL": "https://api.example.test/v1"})
	cfg, err := ServerLLMFromEnv()
	if err != nil {
		t.Fatalf("defaults rejected: %v", err)
	}
	if cfg.MaxConcurrent != ServerLLMDefaultMaxConcurrent || cfg.MaxRedirects != ServerLLMDefaultMaxRedirects ||
		cfg.MaxResponseBodyBytes != ServerLLMDefaultMaxResponseBodyBytes || cfg.MaxErrorBodyBytes != ServerLLMDefaultMaxErrorBodyBytes ||
		cfg.Timeout != ServerLLMDefaultTimeout {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
}

func TestServerLLMFromEnvRejectsUnsafeDestinations(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
	}{
		{
			name: "plain http to public host",
			env:  map[string]string{"CORTEX_LLM_BASE_URL": "http://api.example.test/v1"},
		},
		{
			name: "plain http loopback without dev switch",
			env:  map[string]string{"CORTEX_LLM_BASE_URL": "http://127.0.0.1:11434/v1"},
		},
		{
			name: "plain http loopback with switch but non-loopback host",
			env: map[string]string{
				"CORTEX_LLM_BASE_URL":            "http://api.example.test/v1",
				"CORTEX_LLM_ALLOW_LOOPBACK_HTTP": "true",
			},
		},
		{
			name: "userinfo in destination",
			env:  map[string]string{"CORTEX_LLM_BASE_URL": "https://user:secret@api.example.test/v1"},
		},
		{
			name: "non-HTTP(S) scheme",
			env:  map[string]string{"CORTEX_LLM_BASE_URL": "ftp://api.example.test/v1"},
		},
		{
			name: "unknown provider",
			env: map[string]string{
				"CORTEX_LLM_PROVIDER": "grok",
				"CORTEX_LLM_API_KEY":  "sk-admin-canary",
			},
		},
		{
			name: "concurrency out of range",
			env: map[string]string{
				"CORTEX_LLM_BASE_URL":       "https://api.example.test/v1",
				"CORTEX_LLM_MAX_CONCURRENT": "65",
			},
		},
		{
			name: "redirects out of range",
			env: map[string]string{
				"CORTEX_LLM_BASE_URL":      "https://api.example.test/v1",
				"CORTEX_LLM_MAX_REDIRECTS": "11",
			},
		},
		{
			name: "hostname allowlist entry with port",
			env: map[string]string{
				"CORTEX_LLM_BASE_URL":      "https://api.example.test/v1",
				"CORTEX_LLM_ALLOWED_HOSTS": "evil.test:8443",
			},
		},
		{
			name: "allowed port out of range",
			env: map[string]string{
				"CORTEX_LLM_BASE_URL":      "https://api.example.test/v1",
				"CORTEX_LLM_ALLOWED_PORTS": "70000",
			},
		},
		{
			name: "malformed boolean",
			env: map[string]string{
				"CORTEX_LLM_BASE_URL":       "https://api.example.test/v1",
				"CORTEX_LLM_ALLOW_LOOPBACK": "yes-please",
			},
		},
		{
			name: "malformed timeout",
			env: map[string]string{
				"CORTEX_LLM_BASE_URL": "https://api.example.test/v1",
				"CORTEX_LLM_TIMEOUT":  "later",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setLLMEnv(t, tc.env)
			cfg, err := ServerLLMFromEnv()
			if err == nil {
				t.Fatalf("unsafe configuration accepted: %+v", cfg)
			}
			// Fail-closed: no partial configuration escapes on error.
			if cfg.Configured() || cfg.APIKey != "" {
				t.Fatalf("error must not return partial configuration: %+v", cfg)
			}
		})
	}
}

func TestServerLLMFromEnvLoopbackHTTPDevSwitchAccepted(t *testing.T) {
	setLLMEnv(t, map[string]string{
		"CORTEX_LLM_BASE_URL":            "http://127.0.0.1:11434/v1",
		"CORTEX_LLM_ALLOW_LOOPBACK_HTTP": "true",
	})
	cfg, err := ServerLLMFromEnv()
	if err != nil {
		t.Fatalf("explicit loopback development configuration rejected: %v", err)
	}
	if !cfg.Configured() || !cfg.AllowLoopbackHTTP {
		t.Fatalf("development switches not mapped: %+v", cfg)
	}
}

func TestServerLLMFromEnvErrorsNeverEchoCredential(t *testing.T) {
	const keyCanary = "sk-super-secret-canary"
	setLLMEnv(t, map[string]string{
		"CORTEX_LLM_PROVIDER": "not-a-provider",
		"CORTEX_LLM_API_KEY":  keyCanary,
	})
	_, err := ServerLLMFromEnv()
	if err == nil {
		t.Fatal("invalid provider must be rejected")
	}
	if strings.Contains(err.Error(), keyCanary) {
		t.Fatalf("validation error leaks the credential: %s", err.Error())
	}
}

func TestServerLLMFromEnvCAFile(t *testing.T) {
	dir := t.TempDir()

	t.Run("garbage file rejected", func(t *testing.T) {
		garbage := filepath.Join(dir, "garbage.pem")
		if err := os.WriteFile(garbage, []byte("not a certificate"), 0o600); err != nil {
			t.Fatal(err)
		}
		setLLMEnv(t, map[string]string{
			"CORTEX_LLM_BASE_URL": "https://api.example.test/v1",
			"CORTEX_LLM_CA_FILE":  garbage,
		})
		if _, err := ServerLLMFromEnv(); err == nil {
			t.Fatal("garbage CA file accepted")
		}
	})

	t.Run("valid pem loaded", func(t *testing.T) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		template := x509.Certificate{
			SerialNumber:          big.NewInt(1),
			Subject:               pkix.Name{CommonName: "llm-test-ca"},
			IsCA:                  true,
			BasicConstraintsValid: true,
		}
		der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
		if err != nil {
			t.Fatal(err)
		}
		caFile := filepath.Join(dir, "ca.pem")
		if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
			t.Fatal(err)
		}
		setLLMEnv(t, map[string]string{
			"CORTEX_LLM_BASE_URL": "https://api.example.test/v1",
			"CORTEX_LLM_CA_FILE":  caFile,
		})
		cfg, err := ServerLLMFromEnv()
		if err != nil {
			t.Fatalf("valid CA rejected: %v", err)
		}
		if cfg.CACertPool == nil {
			t.Fatal("CA pool not loaded")
		}
	})
}
