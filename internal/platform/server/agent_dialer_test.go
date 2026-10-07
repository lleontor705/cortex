package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/config"
)

type fakeAgentResolver struct {
	ips []net.IP
	err error
}

func (f fakeAgentResolver) LookupIP(ctx context.Context, network, host string) ([]net.IP, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.ips, nil
}

func withAgentFakeResolver(t *testing.T, ips []net.IP) {
	t.Helper()
	prev := agentResolver
	agentResolver = fakeAgentResolver{ips: ips}
	t.Cleanup(func() { agentResolver = prev })
}

func withAgentFakeDial(t *testing.T, failIPs map[string]bool) {
	t.Helper()
	prev := agentDial
	agentDial = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, _, _ := net.SplitHostPort(address)
		if failIPs[host] {
			return nil, errors.New("fake dial failure for " + host)
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	t.Cleanup(func() { agentDial = prev })
}

// TestAgentDialContextMultiIPFallback verifies the SSRF-safe dialer tries all
// approved resolved IPs in order and dials the first reachable one, skipping
// unapproved and unreachable candidates (dual-stack IPv6-first regression).
func TestAgentDialContextMultiIPFallback(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = listener.Close() }()
	server := httptest.NewUnstartedServer(http.NewServeMux())
	server.Listener = listener
	server.Start()
	defer server.Close()

	private := net.ParseIP("10.0.0.7")
	unreachable := net.ParseIP("203.0.113.9") // TEST-NET-3: approved but fake-dialed as failing
	loopback := net.ParseIP("127.0.0.1")

	cfg := config.ServerLLMConfig{AllowLoopback: true}
	dialer := agentDialContext(cfg, server.URL)
	port := strings.SplitN(listener.Addr().String(), ":", 2)[1]

	cases := []struct {
		name    string
		ips     []net.IP
		failIPs map[string]bool
		wantErr string
	}{
		{
			name: "unreachable approved IP first falls through to reachable IP",
			ips:  []net.IP{unreachable, loopback},
			failIPs: map[string]bool{
				"203.0.113.9": true,
			},
		},
		{
			name: "unapproved IP is skipped without counting as a dial attempt",
			ips:  []net.IP{private, loopback},
		},
		{
			name:    "all approved IPs fail",
			ips:     []net.IP{unreachable},
			failIPs: map[string]bool{"203.0.113.9": true},
			wantErr: "server: agent provider dial rejected",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withAgentFakeResolver(t, tc.ips)
			withAgentFakeDial(t, tc.failIPs)

			conn, err := dialer(context.Background(), "tcp", net.JoinHostPort("provider.example", port))
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("dial err = %v, want %q", err, tc.wantErr)
				}
				if conn != nil {
					_ = conn.Close()
				}
				return
			}
			if err != nil {
				t.Fatalf("dial err = %v, want success via fallback", err)
			}
			if conn == nil {
				t.Fatal("dial returned nil conn without error")
			}
			_ = conn.Close()
		})
	}
}
