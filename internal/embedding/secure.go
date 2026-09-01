package embedding

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const defaultMaxEmbeddingResponse int64 = 4 << 20

type OutboundPolicy struct {
	AllowedHosts              []string
	AllowedPorts              []int
	AllowLoopback             bool
	AllowInsecureLoopbackHTTP bool
	// RailwayInternalEmbeddingHost is one exact, administrator-configured
	// *.railway.internal hostname permitted to resolve to a private IP for
	// the embedding provider. All other private HTTP destinations stay denied.
	RailwayInternalEmbeddingHost string
	MaxRedirects                 int
	MaxResponseBodyBytes         int64
	MaxConcurrent                int
	Timeout                      time.Duration
}

func (p *OutboundPolicy) ApproveDestination(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil {
		return errors.New("embedding: invalid outbound destination")
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "" {
		return errors.New("embedding: invalid outbound destination")
	}
	p.AllowedHosts = append(p.AllowedHosts, host)
	port := u.Port()
	if port == "" {
		if strings.EqualFold(u.Scheme, "https") {
			port = "443"
		} else {
			port = "80"
		}
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return errors.New("embedding: invalid outbound destination")
	}
	p.AllowedPorts = append(p.AllowedPorts, n)
	return nil
}

func NewSecure(cfg Config, policy OutboundPolicy) (Service, error) {
	if cfg.Provider != "" && cfg.Provider != "none" && cfg.Provider != "ollama" && cfg.Provider != "openai" {
		return nil, errors.New("embedding: unsupported secure provider")
	}
	base := cfg.BaseURL
	if base == "" {
		switch cfg.Provider {
		case "openai":
			base = "https://api.openai.com/v1"
		case "ollama":
			base = "http://localhost:11434"
		}
	}
	if cfg.Provider == "" || cfg.Provider == "none" {
		return nil, nil
	}
	if err := policy.validateURL(base); err != nil {
		return nil, err
	}
	if policy.Timeout <= 0 {
		policy.Timeout = 30 * time.Second
	}
	if policy.MaxRedirects <= 0 {
		policy.MaxRedirects = 3
	}
	if policy.MaxResponseBodyBytes <= 0 {
		policy.MaxResponseBodyBytes = defaultMaxEmbeddingResponse
	}
	if policy.MaxConcurrent <= 0 {
		policy.MaxConcurrent = 4
	}
	transport := &http.Transport{DialContext: policy.dialContext, MaxIdleConns: 10, MaxIdleConnsPerHost: 5, IdleConnTimeout: 90 * time.Second}
	client := &http.Client{Timeout: policy.Timeout, Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= policy.MaxRedirects {
			return errors.New("embedding: redirect limit exceeded")
		}
		return policy.validateURL(req.URL.String())
	}}
	cfg.BaseURL = strings.TrimRight(base, "/")
	svc := newWithClient(cfg, client, policy.MaxResponseBodyBytes, policy.MaxConcurrent)
	if svc == nil {
		return nil, errors.New("embedding: configured secure provider is unavailable")
	}
	return svc, nil
}

func (p OutboundPolicy) validateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil {
		return errors.New("embedding: outbound destination rejected")
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if !containsString(p.AllowedHosts, host) {
		return errors.New("embedding: outbound destination rejected")
	}
	port := u.Port()
	if port == "" {
		if strings.EqualFold(u.Scheme, "https") {
			port = "443"
		} else {
			port = "80"
		}
	}
	n, err := strconv.Atoi(port)
	if err != nil || !containsInt(p.AllowedPorts, n) {
		return errors.New("embedding: outbound destination rejected")
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
	case "http":
		if (!p.AllowInsecureLoopbackHTTP || !isLoopbackHost(host)) &&
			!p.isAllowedRailwayPrivateEmbeddingHost(host) {
			return errors.New("embedding: outbound destination rejected")
		}
	default:
		return errors.New("embedding: outbound destination rejected")
	}
	if ip := net.ParseIP(host); ip != nil && !p.allowedIP(ip) {
		return errors.New("embedding: outbound destination rejected")
	}
	return nil
}

func (p OutboundPolicy) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errors.New("embedding: outbound dial rejected")
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return nil, errors.New("embedding: outbound resolution failed")
	}
	for _, ip := range ips {
		if p.allowedIPForHost(ip, host) {
			return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		}
	}
	return nil, errors.New("embedding: outbound dial rejected")
}

func (p OutboundPolicy) allowedIP(ip net.IP) bool {
	if ip.IsLoopback() {
		return p.AllowLoopback
	}
	return !ip.IsPrivate() && !ip.IsUnspecified() && !ip.IsMulticast() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast()
}
func (p OutboundPolicy) allowedIPForHost(ip net.IP, host string) bool {
	if p.isAllowedRailwayPrivateEmbeddingHost(host) && ip.IsPrivate() {
		return true
	}
	return p.allowedIP(ip)
}

func isLoopbackHost(host string) bool {
	return strings.EqualFold(host, "localhost") || (net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback())
}
func (p OutboundPolicy) isAllowedRailwayPrivateEmbeddingHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	configured := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(p.RailwayInternalEmbeddingHost), "."))
	return configured != "" && configured == host && strings.HasSuffix(host, ".railway.internal")
}

func containsString(xs []string, want string) bool {
	for _, x := range xs {
		if strings.EqualFold(strings.TrimSuffix(strings.TrimSpace(x), "."), want) {
			return true
		}
	}
	return false
}
func containsInt(xs []int, want int) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
