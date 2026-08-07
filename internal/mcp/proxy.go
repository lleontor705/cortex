package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	clienttransport "github.com/mark3labs/mcp-go/client/transport"
	protocol "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// RemoteProxyConfig describes the remote MCP endpoint used by the local stdio
// bridge. TokenEnv names the environment variable; secrets are never stored in
// the Cortex YAML model or copied into tool definitions.
type RemoteProxyConfig struct {
	URL      string
	TokenEnv string
	Timeout  time.Duration
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

// OpenRemoteProxy connects, negotiates, and snapshots the remote tool catalog.
// It fails closed so an unavailable or unauthenticated remote cannot silently
// fall back to a different local memory database.
func OpenRemoteProxy(ctx context.Context, cfg RemoteProxyConfig) (*RemoteProxy, error) {
	token := strings.TrimSpace(os.Getenv(cfg.TokenEnv))
	if token == "" {
		return nil, fmt.Errorf("remote MCP token environment variable %s is empty", cfg.TokenEnv)
	}
	remote, err := mcpclient.NewStreamableHttpClient(
		cfg.URL,
		clienttransport.WithHTTPHeaders(map[string]string{"Authorization": "Bearer " + token}),
		clienttransport.WithHTTPTimeout(cfg.Timeout),
	)
	if err != nil {
		return nil, fmt.Errorf("create remote MCP client: %w", err)
	}
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
			return remote.CallTool(ctx, request)
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
