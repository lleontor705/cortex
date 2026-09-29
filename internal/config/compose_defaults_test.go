package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const bootstrapDevelopmentKey = "CORTEX_SERVER_BOOTSTRAP_DEVELOPMENT"

type composeStack struct {
	Services map[string]composeService `yaml:"services"`
}

type composeService struct {
	Environment map[string]string `yaml:"environment"`
}

// envDefaultValue resolves the default arm of a Compose environment reference.
// A plain literal is returned unchanged so callers can compare against "false".
func envDefaultValue(reference string) string {
	if !strings.HasPrefix(reference, "${") || !strings.HasSuffix(reference, "}") {
		return reference
	}
	inner := reference[2 : len(reference)-1]
	if idx := strings.Index(inner, ":-"); idx >= 0 {
		return inner[idx+2:]
	}
	// Compose also accepts ${VAR-default}; an unset variable expands to empty here.
	if idx := strings.Index(inner, "-"); idx >= 0 {
		return inner[idx+1:]
	}
	return ""
}

func loadComposeStack(t *testing.T, path string) composeService {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var stack composeStack
	if err := yaml.Unmarshal(raw, &stack); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	service, ok := stack.Services["cortex-server"]
	if !ok {
		t.Fatalf("%s: services.cortex-server is missing", path)
	}
	return service
}

func TestComposeBootstrapDevelopmentDefaults(t *testing.T) {
	files := []struct {
		name string
		path string
	}{
		{"root compose", filepath.Join("..", "..", "docker-compose.yml")},
		{"production compose", filepath.Join("..", "..", "docker", "docker-compose.prod.yml")},
	}

	for _, file := range files {
		t.Run(file.name, func(t *testing.T) {
			service := loadComposeStack(t, file.path)

			reference, ok := service.Environment[bootstrapDevelopmentKey]
			if !ok {
				t.Fatalf("%s: %s is not declared for cortex-server", file.path, bootstrapDevelopmentKey)
			}
			if got := envDefaultValue(reference); got != "false" {
				t.Fatalf("%s: %s default = %q, want %q (distinct-role validation must run by default)",
					file.path, bootstrapDevelopmentKey, got, "false")
			}
			// The default must stay overridable from the environment/.env so the
			// local development fallback remains an explicit opt-in.
			if reference != "${"+bootstrapDevelopmentKey+":-false}" {
				t.Fatalf("%s: %s = %q, want an environment override with a false default",
					file.path, bootstrapDevelopmentKey, reference)
			}
		})
	}
}

func TestComposeDSNWiringIntact(t *testing.T) {
	files := []struct {
		name string
		path string
	}{
		{"root compose", filepath.Join("..", "..", "docker-compose.yml")},
		{"production compose", filepath.Join("..", "..", "docker", "docker-compose.prod.yml")},
	}

	required := []string{
		"CORTEX_SERVER_STORAGE_DRIVER",
		"CORTEX_SERVER_STORAGE_DSN",
		"CORTEX_SERVER_STORAGE_MIGRATION_DSN",
		"CORTEX_SERVER_AUTO_BOOTSTRAP",
	}

	for _, file := range files {
		t.Run(file.name, func(t *testing.T) {
			service := loadComposeStack(t, file.path)
			for _, key := range required {
				if value, ok := service.Environment[key]; !ok || strings.TrimSpace(value) == "" {
					t.Fatalf("%s: %s is missing or empty for cortex-server", file.path, key)
				}
			}
			if got := envDefaultValue(service.Environment["CORTEX_SERVER_STORAGE_DRIVER"]); got != "postgres" {
				t.Fatalf("%s: CORTEX_SERVER_STORAGE_DRIVER = %q, want %q", file.path, got, "postgres")
			}
		})
	}
}
