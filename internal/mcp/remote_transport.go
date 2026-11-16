package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	clienttransport "github.com/mark3labs/mcp-go/client/transport"
	protocol "github.com/mark3labs/mcp-go/mcp"
)

// bearerStreamableTransport is Cortex's own MCP Streamable HTTP client
// transport for the remote proxy.
//
// It replaces mcp-go's transport.StreamableHTTP (GO-2026-6911: that transport
// can follow redirects to arbitrary hosts while re-attaching custom headers and
// bearer tokens, with no fixed upstream release). Every request here goes
// through the caller's *http.Client — always built by newBearerHTTPClient with
// the shared transport policy — and the bearer is set per request, never in a
// RoundTripper that would replay it on redirect hops.
//
// Scope is what the stdio proxy needs: request/response (JSON or SSE),
// notifications, session id and protocol-version headers, and session DELETE
// on close. Server-initiated requests (sampling, elicitation) are not
// supported and are ignored if a server sends them.
type bearerStreamableTransport struct {
	url    string
	token  string
	client *http.Client

	sessionID       atomic.Value // string
	protocolVersion atomic.Value // string

	notifyMu sync.RWMutex
	notify   func(protocol.JSONRPCNotification)

	closed    chan struct{}
	closeOnce sync.Once
}

var (
	_ clienttransport.Interface      = (*bearerStreamableTransport)(nil)
	_ clienttransport.HTTPConnection = (*bearerStreamableTransport)(nil)
)

// errRemoteSessionTerminated reports a 404 for an established session.
var errRemoteSessionTerminated = errors.New("remote MCP session terminated by server")

// maxRemoteErrorBody bounds how much of a non-JSON error body is echoed.
const maxRemoteErrorBody = 4 << 10

func newBearerStreamableTransport(url, token string, client *http.Client) *bearerStreamableTransport {
	t := &bearerStreamableTransport{url: url, token: token, client: client, closed: make(chan struct{})}
	t.sessionID.Store("")
	t.protocolVersion.Store("")
	return t
}

func (t *bearerStreamableTransport) Start(context.Context) error {
	select {
	case <-t.closed:
		return errors.New("remote MCP transport is closed")
	default:
		return nil
	}
}

func (t *bearerStreamableTransport) GetSessionId() string {
	return t.sessionID.Load().(string)
}

func (t *bearerStreamableTransport) SetProtocolVersion(version string) {
	t.protocolVersion.Store(version)
}

func (t *bearerStreamableTransport) SetNotificationHandler(handler func(protocol.JSONRPCNotification)) {
	t.notifyMu.Lock()
	t.notify = handler
	t.notifyMu.Unlock()
}

// modern reports whether the negotiated protocol is the stateless 2026-07-28+
// core, which carries no session header.
func (t *bearerStreamableTransport) modern() bool {
	return protocol.IsModernProtocol(t.protocolVersion.Load().(string))
}

// withClose derives a context that is also cancelled when the transport closes.
func (t *bearerStreamableTransport) withClose(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		select {
		case <-t.closed:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

func (t *bearerStreamableTransport) newRequest(ctx context.Context, method string, body []byte, header http.Header) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, t.url, reader)
	if err != nil {
		return nil, fmt.Errorf("create remote MCP request: %w", err)
	}
	for key, values := range header {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+t.token)
	if version := t.protocolVersion.Load().(string); version != "" {
		req.Header.Set(clienttransport.HeaderKeyProtocolVersion, version)
	}
	if session := t.GetSessionId(); session != "" && !t.modern() {
		req.Header.Set(clienttransport.HeaderKeySessionID, session)
	}
	return req, nil
}

func (t *bearerStreamableTransport) post(ctx context.Context, payload any, header http.Header) (*http.Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal remote MCP message: %w", err)
	}
	req, err := t.newRequest(ctx, http.MethodPost, body, header)
	if err != nil {
		return nil, err
	}
	session := t.GetSessionId()
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("remote MCP request failed: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound && session != "" {
		_ = resp.Body.Close()
		t.sessionID.CompareAndSwap(session, "")
		return nil, errRemoteSessionTerminated
	}
	return resp, nil
}

func (t *bearerStreamableTransport) SendRequest(ctx context.Context, request clienttransport.JSONRPCRequest) (*clienttransport.JSONRPCResponse, error) {
	ctx, cancel := t.withClose(ctx)
	defer cancel()

	resp, err := t.post(ctx, request, request.Header)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return nil, remoteStatusError(resp)
	}

	if request.Method == string(protocol.MethodInitialize) {
		if session := resp.Header.Get(clienttransport.HeaderKeySessionID); session != "" {
			t.sessionID.Store(session)
		}
	}

	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	switch mediaType {
	case "application/json":
		var response clienttransport.JSONRPCResponse
		if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
			return nil, fmt.Errorf("decode remote MCP response: %w", err)
		}
		if response.ID.IsNil() {
			return nil, fmt.Errorf("remote MCP response has no id")
		}
		return &response, nil
	case "text/event-stream":
		return t.readSSEResponse(ctx, resp.Body)
	default:
		return nil, fmt.Errorf("remote MCP returned unexpected content type %q (HTTP %d)", resp.Header.Get("Content-Type"), resp.StatusCode)
	}
}

// remoteStatusError turns a non-success response into either the JSON-RPC
// error the server sent (so the client surfaces it as such) or a bounded,
// actionable transport error.
func remoteStatusError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxRemoteErrorBody))
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("%w: remote MCP rejected the token (HTTP 401); check the remote token", clienttransport.ErrAuthorizationRequired)
	case http.StatusForbidden:
		return fmt.Errorf("remote MCP denied access (HTTP 403): %s", strings.TrimSpace(string(body)))
	case http.StatusTooManyRequests:
		if retry := resp.Header.Get("Retry-After"); retry != "" {
			return fmt.Errorf("remote MCP rate limited (HTTP 429, retry after %ss)", retry)
		}
		return fmt.Errorf("remote MCP rate limited (HTTP 429)")
	}
	return fmt.Errorf("remote MCP request failed with HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
}

// readSSEResponse consumes an SSE stream until the JSON-RPC response arrives,
// dispatching any notifications seen on the way.
func (t *bearerStreamableTransport) readSSEResponse(ctx context.Context, body io.Reader) (*clienttransport.JSONRPCResponse, error) {
	type outcome struct {
		resp *clienttransport.JSONRPCResponse
		err  error
	}
	done := make(chan outcome, 1)
	go func() {
		reader := bufio.NewReader(body)
		var data strings.Builder
		flush := func() *clienttransport.JSONRPCResponse {
			payload := data.String()
			data.Reset()
			if payload == "" {
				return nil
			}
			return t.handleSSEMessage([]byte(payload))
		}
		for {
			line, err := reader.ReadString('\n')
			line = strings.TrimRight(line, "\r\n")
			switch {
			case line == "":
				if resp := flush(); resp != nil {
					done <- outcome{resp: resp}
					return
				}
			case strings.HasPrefix(line, "data:"):
				if data.Len() > 0 {
					data.WriteByte('\n')
				}
				data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			}
			if err != nil {
				if resp := flush(); resp != nil {
					done <- outcome{resp: resp}
					return
				}
				if errors.Is(err, io.EOF) {
					err = errors.New("remote MCP stream ended before a response")
				}
				done <- outcome{err: err}
				return
			}
		}
	}()
	select {
	case got := <-done:
		return got.resp, got.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// handleSSEMessage returns the message when it is a response; notifications
// are dispatched and server-initiated requests are dropped.
func (t *bearerStreamableTransport) handleSSEMessage(payload []byte) *clienttransport.JSONRPCResponse {
	var envelope struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil
	}
	hasID := len(envelope.ID) > 0 && string(envelope.ID) != "null"
	switch {
	case envelope.Method != "" && !hasID:
		var notification protocol.JSONRPCNotification
		if err := json.Unmarshal(payload, &notification); err == nil {
			t.notifyMu.RLock()
			handler := t.notify
			t.notifyMu.RUnlock()
			if handler != nil {
				handler(notification)
			}
		}
		return nil
	case envelope.Method != "":
		return nil // server-initiated request: unsupported by the proxy
	case hasID:
		var response clienttransport.JSONRPCResponse
		if err := json.Unmarshal(payload, &response); err != nil {
			return nil
		}
		return &response
	}
	return nil
}

func (t *bearerStreamableTransport) SendNotification(ctx context.Context, notification protocol.JSONRPCNotification) error {
	ctx, cancel := t.withClose(ctx)
	defer cancel()

	resp, err := t.post(ctx, notification, nil)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK, http.StatusAccepted, http.StatusNoContent:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxRemoteErrorBody))
		return nil
	default:
		return remoteStatusError(resp)
	}
}

// Close cancels in-flight requests and, for legacy sessions, asks the server
// to drop the session (best effort, bounded).
func (t *bearerStreamableTransport) Close() error {
	t.closeOnce.Do(func() {
		close(t.closed)
		session := t.GetSessionId()
		if session == "" || t.modern() {
			return
		}
		t.sessionID.Store("")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		req, err := t.newRequest(ctx, http.MethodDelete, nil, nil)
		if err != nil {
			return
		}
		req.Header.Set(clienttransport.HeaderKeySessionID, session)
		if resp, err := t.client.Do(req); err == nil {
			_ = resp.Body.Close()
		}
	})
	return nil
}
