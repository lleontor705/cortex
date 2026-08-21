package config

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/lleontor705/cortex/internal/transportpolicy"
)

// ServerLLMConfig holds administrator-owned outbound LLM provider settings
// for server extract/synthesize (SEC-02). Every field comes exclusively from
// trusted server configuration (CORTEX_LLM_* environment variables); request
// data can never influence any field. The API key is read directly from the
// environment and is never stored in Config, rendered by Config.String, or
// echoed in validation errors.
//
// An absent configuration (no provider and no base URL) preserves
// heuristic-only operation with no approved outbound destination.
type ServerLLMConfig struct {
	// Provider selects the provider preset: "" | "openai" | "anthropic" |
	// "generic". With an empty BaseURL the preset's canonical HTTPS endpoint
	// is used.
	Provider string
	// BaseURL is the admin-approved provider destination. It must be an
	// absolute HTTPS URL without userinfo; plain HTTP is permitted only for a
	// strict loopback host under the explicit AllowLoopbackHTTP development
	// switch.
	BaseURL string
	// Model optionally overrides the provider model.
	Model string
	// APIKey is the provider credential supplied via CORTEX_LLM_API_KEY. It
	// is attached to outbound requests only after the destination passes
	// outbound policy validation.
	APIKey string
	// AllowedHosts is an additional destination host allowlist: plain
	// hostnames (no port, userinfo, slash, or whitespace), case-insensitive.
	AllowedHosts []string
	// AllowedPorts optionally extends approved TCP ports. 443 is always
	// approved for HTTPS destinations.
	AllowedPorts []int
	// AllowLoopback is an explicit local-only development switch permitting
	// HTTPS loopback destinations.
	AllowLoopback bool
	// AllowLoopbackHTTP is an explicit local-only development switch
	// permitting plain HTTP to strict loopback hosts only.
	AllowLoopbackHTTP bool
	// MaxConcurrent bounds concurrent outbound provider requests
	// (default 4, range 1-64).
	MaxConcurrent int
	// MaxRedirects caps redirect chains (default 3, range 1-10).
	MaxRedirects int
	// MaxResponseBodyBytes bounds provider success bodies (default 4 MiB,
	// range 1 B - 64 MiB).
	MaxResponseBodyBytes int64
	// MaxErrorBodyBytes bounds drained provider error bodies (default 4 KiB,
	// range 1 B - 1 MiB).
	MaxErrorBodyBytes int64
	// Timeout bounds one provider round-trip (default 45s, max 5m).
	Timeout time.Duration
	// CACertPool optionally holds private CA roots for the provider TLS
	// handshake (CORTEX_LLM_CA_FILE, PEM-encoded certificates).
	CACertPool *x509.CertPool
}

// Configured reports whether an outbound provider is configured. When false,
// extract/synthesize stay heuristic-only with no approved destination.
func (c ServerLLMConfig) Configured() bool {
	return c.BaseURL != "" || c.Provider != ""
}

// Defaults applied by ValidateServerLLM for unset bounds.
const (
	ServerLLMDefaultMaxConcurrent        = 4
	ServerLLMDefaultMaxRedirects         = 3
	ServerLLMDefaultMaxResponseBodyBytes = int64(4 << 20)
	ServerLLMDefaultMaxErrorBodyBytes    = int64(4 << 10)
	ServerLLMDefaultTimeout              = 45 * time.Second
	ServerLLMMaxTimeout                  = 5 * time.Minute
	ServerLLMMaxAllowedHosts             = 64
	ServerLLMMaxAllowedPorts             = 16
)

// ServerLLMFromEnv reads the administrator-owned outbound provider
// configuration from CORTEX_LLM_* environment variables and validates it.
// An invalid or inconsistent configuration is an error: callers must fail
// closed rather than silently fall back to a different destination class.
func ServerLLMFromEnv() (ServerLLMConfig, error) {
	p := &envParser{}
	cfg := ServerLLMConfig{
		Provider:             strings.TrimSpace(os.Getenv("CORTEX_LLM_PROVIDER")),
		BaseURL:              strings.TrimSpace(os.Getenv("CORTEX_LLM_BASE_URL")),
		Model:                strings.TrimSpace(os.Getenv("CORTEX_LLM_MODEL")),
		APIKey:               os.Getenv("CORTEX_LLM_API_KEY"),
		AllowedHosts:         splitLLMHostList(os.Getenv("CORTEX_LLM_ALLOWED_HOSTS")),
		AllowedPorts:         splitLLMPortList(os.Getenv("CORTEX_LLM_ALLOWED_PORTS")),
		AllowLoopback:        p.boolEnv("CORTEX_LLM_ALLOW_LOOPBACK"),
		AllowLoopbackHTTP:    p.boolEnv("CORTEX_LLM_ALLOW_LOOPBACK_HTTP"),
		MaxConcurrent:        p.intEnv("CORTEX_LLM_MAX_CONCURRENT"),
		MaxRedirects:         p.intEnv("CORTEX_LLM_MAX_REDIRECTS"),
		MaxResponseBodyBytes: p.int64Env("CORTEX_LLM_MAX_RESPONSE_BODY_BYTES"),
		MaxErrorBodyBytes:    p.int64Env("CORTEX_LLM_MAX_ERROR_BODY_BYTES"),
		Timeout:              p.durationEnv("CORTEX_LLM_TIMEOUT"),
	}
	if caFile := strings.TrimSpace(os.Getenv("CORTEX_LLM_CA_FILE")); caFile != "" {
		pool, err := loadLLMCACertPool(caFile)
		if err != nil {
			return ServerLLMConfig{}, err
		}
		cfg.CACertPool = pool
	}
	if err := p.err(); err != nil {
		return ServerLLMConfig{}, err
	}
	if err := ValidateServerLLM(&cfg); err != nil {
		return ServerLLMConfig{}, err
	}
	return cfg, nil
}

// ValidateServerLLM validates and normalizes the outbound LLM configuration.
// Error messages never include the API key or the raw base URL: they name the
// offending field and the constraint only.
func ValidateServerLLM(cfg *ServerLLMConfig) error {
	if cfg == nil {
		return fmt.Errorf("invalid llm configuration: nil")
	}
	switch cfg.Provider {
	case "", "openai", "anthropic", "generic":
	default:
		return fmt.Errorf("invalid llm provider %q (valid: openai, anthropic, generic)", cfg.Provider)
	}
	if cfg.BaseURL != "" {
		u, err := url.Parse(cfg.BaseURL)
		if err != nil || u.Host == "" || u.Scheme == "" {
			return fmt.Errorf("invalid llm base_url: must be an absolute HTTP(S) URL")
		}
		if u.User != nil {
			return fmt.Errorf("invalid llm base_url: must not embed userinfo")
		}
		loopbackHost := transportpolicy.IsStrictLoopbackHost(u.Hostname())
		switch strings.ToLower(u.Scheme) {
		case "https":
		case "http":
			if !cfg.AllowLoopbackHTTP {
				return fmt.Errorf("invalid llm base_url: plain HTTP requires the explicit allow_loopback_http development switch")
			}
			if !loopbackHost {
				return fmt.Errorf("invalid llm base_url: plain HTTP is only permitted for strict loopback hosts")
			}
		default:
			return fmt.Errorf("invalid llm base_url: scheme must be HTTPS")
		}
	}
	if len(cfg.AllowedHosts) > ServerLLMMaxAllowedHosts {
		return fmt.Errorf("invalid llm allowed_hosts: at most %d entries", ServerLLMMaxAllowedHosts)
	}
	for _, host := range cfg.AllowedHosts {
		if host == "" || strings.ContainsAny(host, "/:@ \t") {
			return fmt.Errorf("invalid llm allowed_hosts: entries must be plain hostnames without port or userinfo")
		}
	}
	if len(cfg.AllowedPorts) > ServerLLMMaxAllowedPorts {
		return fmt.Errorf("invalid llm allowed_ports: at most %d entries", ServerLLMMaxAllowedPorts)
	}
	for _, port := range cfg.AllowedPorts {
		if port < 1 || port > 65535 {
			return fmt.Errorf("invalid llm allowed_ports: ports must be 1-65535")
		}
	}
	if err := normalizeLLMBoundInt("llm max_concurrent", &cfg.MaxConcurrent, ServerLLMDefaultMaxConcurrent, 1, 64); err != nil {
		return err
	}
	if err := normalizeLLMBoundInt("llm max_redirects", &cfg.MaxRedirects, ServerLLMDefaultMaxRedirects, 1, 10); err != nil {
		return err
	}
	if err := normalizeLLMBoundInt64("llm max_response_body_bytes", &cfg.MaxResponseBodyBytes, ServerLLMDefaultMaxResponseBodyBytes, 1, 64<<20); err != nil {
		return err
	}
	if err := normalizeLLMBoundInt64("llm max_error_body_bytes", &cfg.MaxErrorBodyBytes, ServerLLMDefaultMaxErrorBodyBytes, 1, 1<<20); err != nil {
		return err
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = ServerLLMDefaultTimeout
	}
	if cfg.Timeout < 0 || cfg.Timeout > ServerLLMMaxTimeout {
		return fmt.Errorf("invalid llm timeout: must be greater than zero and at most %s", ServerLLMMaxTimeout)
	}
	return nil
}

func normalizeLLMBoundInt(name string, value *int, def, min, max int) error {
	if *value == 0 {
		*value = def
		return nil
	}
	if *value < min || *value > max {
		return fmt.Errorf("invalid %s: must be %d-%d", name, min, max)
	}
	return nil
}

func normalizeLLMBoundInt64(name string, value *int64, def, min, max int64) error {
	if *value == 0 {
		*value = def
		return nil
	}
	if *value < min || *value > max {
		return fmt.Errorf("invalid %s: must be %d-%d", name, min, max)
	}
	return nil
}

// envParser accumulates malformed-value errors without echoing values.
type envParser struct {
	errs []error
}

func (p *envParser) fail(name string) {
	p.errs = append(p.errs, fmt.Errorf("invalid %s: malformed value", name))
}

func (p *envParser) err() error {
	if len(p.errs) == 0 {
		return nil
	}
	// All errors joined so every malformed variable is reported at once.
	msgs := make([]string, 0, len(p.errs))
	for _, err := range p.errs {
		msgs = append(msgs, err.Error())
	}
	return fmt.Errorf("%s", strings.Join(msgs, "; "))
}

func (p *envParser) boolEnv(name string) bool {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return false
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		p.fail(name)
		return false
	}
	return v
}

func (p *envParser) intEnv(name string) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return 0
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		p.fail(name)
		return 0
	}
	return v
}

func (p *envParser) int64Env(name string) int64 {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return 0
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		p.fail(name)
		return 0
	}
	return v
}

func (p *envParser) durationEnv(name string) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return 0
	}
	v, err := time.ParseDuration(raw)
	if err != nil {
		p.fail(name)
		return 0
	}
	return v
}

func splitLLMHostList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.ToLower(strings.TrimSpace(part)); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func splitLLMPortList(raw string) []int {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]int, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		v, err := strconv.Atoi(part)
		if err != nil || v < 1 || v > 65535 {
			// Malformed ports are rejected here as zero, which validation
			// flags via the out-of-range check.
			out = append(out, 0)
			continue
		}
		out = append(out, v)
	}
	return out
}

// loadLLMCACertPool loads a PEM certificate file for the provider TLS
// handshake. The path is operator-controlled and safe to name in errors; the
// file contents never are.
func loadLLMCACertPool(path string) (*x509.CertPool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("invalid llm ca_file: cannot be read: %w", err)
	}
	pool := x509.NewCertPool()
	certs := 0
	rest := data
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("invalid llm ca_file: certificate could not be parsed")
		}
		pool.AddCert(cert)
		certs++
	}
	if certs == 0 {
		return nil, fmt.Errorf("invalid llm ca_file: no PEM certificates found")
	}
	return pool, nil
}
