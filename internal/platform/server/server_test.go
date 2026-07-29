package server

import (
	"context"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/internal/config"
)

func TestOpenFailsFastWithoutPostgresDSN(t *testing.T) {
	cfg := config.Config{Server: config.ServerConfig{Storage: config.ServerStorageConfig{Driver: "postgres"}}}
	_, err := Open(context.Background(), cfg)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "dsn") {
		t.Fatalf("Open() error = %v, want fail-fast DSN error", err)
	}
}

func TestServerConfigStringRedactsSecrets(t *testing.T) {
	cfg := config.Config{Server: config.ServerConfig{
		Storage: config.ServerStorageConfig{DSN: "postgres://user:secret@db/cortex"},
		Secrets: config.ServerSecretsConfig{SigningKey: "secret-key"},
	}}
	text := cfg.String()
	if strings.Contains(text, "secret") {
		t.Fatalf("Config.String leaked server secret: %q", text)
	}
}
