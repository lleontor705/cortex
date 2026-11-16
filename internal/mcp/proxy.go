package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	protocol "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/lleontor705/cortex/v2/internal/config"
	"github.com/lleontor705/cortex/v2/internal/mcp/memorycontract"
	"github.com/lleontor705/cortex/v2/internal/transportpolicy"
)

// RemoteProxyConfig describes the remote MCP endpoint used by the local stdio
// bridge. TokenEnv names the environment variable; secrets are never stored in
// the Cortex YAML model or copied into tool definitions.
type RemoteProxyConfig struct {
	URL      string
	TokenEnv string
	// FallbackToken is used when TokenEnv resolves to nothing; callers pass
	// the stored http.token written by `cortex auth login` / TUI connect.
	FallbackToken string
	// Token, when set, is the already-resolved bearer (config.ResolveRemoteBearer,
	// which also reads the OS keychain) and takes precedence over TokenEnv.
	Token   string
	Timeout time.Duration
}

// RemoteProxy owns the remote client and the local stdio-facing MCP server.
type RemoteProxy struct {
	Server *mcpserver.MCPServer
	client remoteToolClient
}

type remoteToolClient interface {
	Start(context.Context) error
	Initialize(context.Context, protocol.InitializeRequest) (*protocol.InitializeResult, error)
	ListTools(context.Context, protocol.ListToolsRequest) (*protocol.ListToolsResult, error)
	CallTool(context.Context, protocol.CallToolRequest) (*protocol.CallToolResult, error)
	Close() error
}

// newBearerHTTPClient builds the HTTP client used for every remote MCP
// request carrying Bearer credentials. The shared transport policy gates
// redirects before the redirected request — and therefore before the
// Authorization header — is ever sent: no scheme downgrade, no cross-origin
// credential forwarding (REM-TRANSPORT-001).
func newBearerHTTPClient() *http.Client {
	return &http.Client{CheckRedirect: transportpolicy.CheckBearerRedirect}
}

// OpenRemoteProxy connects, negotiates, and snapshots the remote tool catalog.
// It fails closed so an unavailable or unauthenticated remote cannot silently
// fall back to a different local memory database.
//
// REM-TRANSPORT-001: the destination is validated against the shared
// transport policy BEFORE the token is read or any client capable of sending
// it is constructed — HTTPS for remote hosts, plain HTTP only on strict
// loopback.
func OpenRemoteProxy(ctx context.Context, cfg RemoteProxyConfig) (*RemoteProxy, error) {
	if err := transportpolicy.ValidateBearerDestination(cfg.URL); err != nil {
		return nil, fmt.Errorf("remote MCP destination rejected: %w", err)
	}
	token := strings.TrimSpace(cfg.Token)
	if token == "" {
		token = config.ResolveRemoteToken(cfg.TokenEnv, cfg.FallbackToken)
	}
	if token == "" {
		return nil, fmt.Errorf("remote MCP token not found: token environment variable %s is empty and no stored token is configured (run `cortex auth login`)", cfg.TokenEnv)
	}
	// Cortex's own Streamable HTTP transport (remote_transport.go) replaces
	// mcp-go's transport.StreamableHTTP, which is affected by GO-2026-6911
	// (redirect-following credential leak, no upstream fix). The bearer is set
	// per request and every hop is checked by the shared transport policy.
	httpClient := newBearerHTTPClient()
	httpClient.Timeout = cfg.Timeout
	remote := mcpclient.NewClient(newBearerStreamableTransport(cfg.URL, token, httpClient))
	proxy, err := openRemoteProxy(ctx, remote)
	if err != nil {
		_ = remote.Close()
		return nil, err
	}
	return proxy, nil
}

func openRemoteProxy(ctx context.Context, remote remoteToolClient) (*RemoteProxy, error) {
	if err := remote.Start(ctx); err != nil {
		return nil, fmt.Errorf("start remote MCP transport: %w", err)
	}
	initialized, err := remote.Initialize(ctx, protocol.InitializeRequest{Params: protocol.InitializeParams{
		ProtocolVersion: protocol.LATEST_PROTOCOL_VERSION,
		ClientInfo:      protocol.Implementation{Name: "cortex-remote-proxy", Version: serverVersion},
	}})
	if err != nil {
		return nil, fmt.Errorf("initialize remote MCP: %w", err)
	}
	tools, err := remote.ListTools(ctx, protocol.ListToolsRequest{})
	if err != nil {
		return nil, fmt.Errorf("list remote MCP tools: %w", err)
	}

	options := []mcpserver.ServerOption{mcpserver.WithToolCapabilities(true)}
	if initialized.Instructions != "" {
		options = append(options, mcpserver.WithInstructions(initialized.Instructions))
	}
	serverName := initialized.ServerInfo.Name
	if serverName == "" {
		serverName = "cortex-remote"
	}
	serverVersion := initialized.ServerInfo.Version
	if serverVersion == "" {
		serverVersion = "remote"
	}
	srv := mcpserver.NewMCPServer(serverName+"-proxy", serverVersion, options...)
	for _, remoteTool := range tools.Tools {
		schema := remoteTool.RawInputSchema
		if len(schema) == 0 {
			schema, err = json.Marshal(remoteTool.InputSchema)
			if err != nil {
				return nil, fmt.Errorf("marshal schema for remote tool %s: %w", remoteTool.Name, err)
			}
		}
		proxiedTool := protocol.NewToolWithRawSchema(remoteTool.Name, remoteTool.Description, schema)
		proxiedTool.Annotations = remoteTool.Annotations
		proxiedTool.OutputSchema = remoteTool.OutputSchema
		proxiedTool.RawOutputSchema = remoteTool.RawOutputSchema
		proxiedTool.Meta = remoteTool.Meta
		proxiedTool.Icons = remoteTool.Icons
		proxiedTool.Execution = remoteTool.Execution
		toolName := remoteTool.Name
		srv.AddTool(proxiedTool, func(ctx context.Context, request protocol.CallToolRequest) (*protocol.CallToolResult, error) {
			request.Params.Name = toolName
			result, err := remote.CallTool(ctx, request)
			if err != nil {
				// Transport-level failure: no MCP result exists at all. The
				// failure is classified into the shared stable error contract
				// and reported as isError — a success result is never
				// fabricated and a valid remote result is never rewritten
				// (REM-MCP-001, RD6).
				payload := memorycontract.FromError(err)
				classified := protocol.NewToolResultStructured(payload, payload.Error.Message)
				classified.IsError = true
				return classified, nil
			}
			return result, nil
		})
	}
	return &RemoteProxy{Server: srv, client: remote}, nil
}

// Close releases the remote Streamable HTTP session.
func (p *RemoteProxy) Close() error {
	if p == nil || p.client == nil {
		return nil
	}
	return p.client.Close()
}
