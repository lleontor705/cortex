// Package credential stores Cortex server tokens in the operating system
// keychain (macOS Keychain, Windows Credential Manager, Linux Secret Service)
// so they never have to live in plaintext config files.
package credential

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/zalando/go-keyring"
)

// Service is the keychain service name for every Cortex credential.
const Service = "cortex"

// ErrNotFound reports that no token is stored for the server.
var ErrNotFound = errors.New("no token stored in the OS keychain for this server")

// Account derives the keychain account for a server URL: scheme://host[:port],
// so `https://x.example/` and `https://x.example/mcp` share one entry.
func Account(serverURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(serverURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid server URL %q", serverURL)
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host), nil
}

// Get returns the token stored for serverURL.
func Get(serverURL string) (string, error) {
	account, err := Account(serverURL)
	if err != nil {
		return "", err
	}
	token, err := keyring.Get(Service, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read OS keychain: %w", err)
	}
	return token, nil
}

// Set stores token for serverURL, replacing any previous entry.
func Set(serverURL, token string) error {
	account, err := Account(serverURL)
	if err != nil {
		return err
	}
	if strings.TrimSpace(token) == "" {
		return errors.New("refusing to store an empty token")
	}
	if err := keyring.Set(Service, account, token); err != nil {
		return fmt.Errorf("write OS keychain: %w", err)
	}
	return nil
}

// Delete removes the token for serverURL. A missing entry is not an error.
func Delete(serverURL string) error {
	account, err := Account(serverURL)
	if err != nil {
		return err
	}
	if err := keyring.Delete(Service, account); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("delete OS keychain entry: %w", err)
	}
	return nil
}

// UseInMemoryStore replaces the OS keychain with a process-local map. Tests
// call it so they never touch the developer's real keychain.
func UseInMemoryStore() {
	keyring.MockInit()
}
