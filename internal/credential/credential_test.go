package credential

import (
	"errors"
	"testing"
)

func TestAccountNormalizesServerURL(t *testing.T) {
	for _, raw := range []string{"https://Cortex.Example/", "https://cortex.example/mcp", " https://cortex.example "} {
		got, err := Account(raw)
		if err != nil || got != "https://cortex.example" {
			t.Errorf("Account(%q) = %q, %v", raw, got, err)
		}
	}
	if got, _ := Account("http://127.0.0.1:7438/mcp"); got != "http://127.0.0.1:7438" {
		t.Errorf("port lost: %q", got)
	}
	for _, bad := range []string{"", "cortex.example", "://x"} {
		if _, err := Account(bad); err == nil {
			t.Errorf("Account(%q) accepted", bad)
		}
	}
}

func TestSetGetDeleteRoundTrip(t *testing.T) {
	UseInMemoryStore()
	if _, err := Get("https://cortex.example"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty store err = %v", err)
	}
	if err := Set("https://cortex.example/mcp", "ctx_secret"); err != nil {
		t.Fatal(err)
	}
	if got, err := Get("https://cortex.example"); err != nil || got != "ctx_secret" {
		t.Fatalf("Get = %q, %v", got, err)
	}
	if err := Set("https://cortex.example", "  "); err == nil {
		t.Fatal("empty token stored")
	}
	if err := Delete("https://cortex.example"); err != nil {
		t.Fatal(err)
	}
	if err := Delete("https://cortex.example"); err != nil {
		t.Fatalf("deleting a missing entry: %v", err)
	}
	if _, err := Get("https://cortex.example"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete err = %v", err)
	}
}
