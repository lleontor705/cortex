package server

import "testing"

func TestReindexRuntimeValidationDoesNotRequireHTTPTransport(t *testing.T) {
	cfg := validBootstrapConfig()
	cfg.HTTP.Enabled = false
	cfg.HTTP.Port = 0
	cfg.HTTP.AllowedOrigins = []string{"*"}

	if err := validateRuntimeConfig(cfg, false); err != nil {
		t.Fatalf("reindex validation unexpectedly required HTTP transport: %v", err)
	}
	if err := validateConfig(cfg); err == nil {
		t.Fatal("server validation accepted disabled/invalid HTTP transport")
	}
}
