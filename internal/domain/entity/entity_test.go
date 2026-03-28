package entity

import (
	"testing"

	"github.com/lleontor705/cortex/internal/domain"
)

func TestExtract(t *testing.T) {
	tests := []struct {
		name     string
		obs      *domain.Observation
		wantType map[string][]string // entityType -> expected values
	}{
		{
			name: "file paths",
			obs: &domain.Observation{
				ID:      1,
				Title:   "Updated auth middleware",
				Content: "Changed src/auth/middleware.ts and internal/store/store.go",
			},
			wantType: map[string][]string{
				domain.EntityFile: {"src/auth/middleware.ts", "internal/store/store.go"},
			},
		},
		{
			name: "URLs",
			obs: &domain.Observation{
				ID:      2,
				Title:   "API reference",
				Content: "See https://api.example.com/docs and http://localhost:8080/health",
			},
			wantType: map[string][]string{
				domain.EntityURL: {"https://api.example.com/docs", "http://localhost:8080/health"},
			},
		},
		{
			name: "Go packages",
			obs: &domain.Observation{
				ID:      3,
				Title:   "Dependencies",
				Content: "Using github.com/spf13/viper for config",
			},
			wantType: map[string][]string{
				domain.EntityPackage: {"github.com/spf13/viper"},
			},
		},
		{
			name: "symbols",
			obs: &domain.Observation{
				ID:      4,
				Title:   "New types",
				Content: "Added func HandleSave and type Config struct",
			},
			wantType: map[string][]string{
				domain.EntitySymbol: {"HandleSave", "Config"},
			},
		},
		{
			name: "mixed content",
			obs: &domain.Observation{
				ID:      5,
				Title:   "JWT migration",
				Content: "Changed src/auth/jwt.go. See https://jwt.io for docs. Added func ValidateToken",
			},
			wantType: map[string][]string{
				domain.EntityFile:   {"src/auth/jwt.go"},
				domain.EntityURL:    {"https://jwt.io"},
				domain.EntitySymbol: {"ValidateToken"},
			},
		},
		{
			name: "empty content",
			obs: &domain.Observation{
				ID:      6,
				Title:   "Empty",
				Content: "",
			},
			wantType: map[string][]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			links := Extract(tt.obs)

			// Group by type
			byType := map[string][]string{}
			for _, l := range links {
				byType[l.EntityType] = append(byType[l.EntityType], l.EntityValue)
			}

			for entityType, expected := range tt.wantType {
				actual := byType[entityType]
				for _, exp := range expected {
					found := false
					for _, act := range actual {
						if act == exp {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("expected %s entity %q not found in %v", entityType, exp, actual)
					}
				}
			}
		})
	}
}

func TestExtract_NoDuplicates(t *testing.T) {
	obs := &domain.Observation{
		ID:      1,
		Title:   "Duplicate test",
		Content: "File src/main.go and again src/main.go referenced twice",
	}

	links := Extract(obs)

	seen := map[string]bool{}
	for _, l := range links {
		key := l.EntityType + ":" + l.EntityValue
		if seen[key] {
			t.Errorf("duplicate entity found: %s", key)
		}
		seen[key] = true
	}
}
