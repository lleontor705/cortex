package a2a

import (
	"encoding/json"
	"testing"
	"time"
)

func TestMessageTypeConstants(t *testing.T) {
	tests := []struct {
		name     string
		msgType  MessageType
		expected string
	}{
		{"Request", MessageTypeRequest, "request"},
		{"Response", MessageTypeResponse, "response"},
		{"Notify", MessageTypeNotify, "notify"},
		{"Event", MessageTypeEvent, "event"},
		{"Stream", MessageTypeStream, "stream"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.msgType) != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, tt.msgType)
			}
		})
	}
}

func TestAgentStatusConstants(t *testing.T) {
	tests := []struct {
		name     string
		status   AgentStatus
		expected string
	}{
		{"Active", StatusActive, "active"},
		{"Inactive", StatusInactive, "inactive"},
		{"Error", StatusError, "error"},
		{"Unknown", StatusUnknown, "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.status) != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, tt.status)
			}
		})
	}
}

func TestNewMessage(t *testing.T) {
	msg := NewMessage(MessageTypeRequest, "agent-1", []string{"agent-2"}, "test-topic", "hello")

	if msg.ID == "" {
		t.Error("message ID should not be empty")
	}
	if msg.Type != MessageTypeRequest {
		t.Errorf("expected type %s, got %s", MessageTypeRequest, msg.Type)
	}
	if msg.From != "agent-1" {
		t.Errorf("expected from agent-1, got %s", msg.From)
	}
	if len(msg.To) != 1 || msg.To[0] != "agent-2" {
		t.Errorf("expected to [agent-2], got %v", msg.To)
	}
	if msg.Topic != "test-topic" {
		t.Errorf("expected topic test-topic, got %s", msg.Topic)
	}
	if msg.Payload != "hello" {
		t.Errorf("expected payload hello, got %v", msg.Payload)
	}
	if msg.Timestamp.IsZero() {
		t.Error("timestamp should not be zero")
	}
	if msg.Headers == nil {
		t.Error("headers should be initialized")
	}
}

func TestMessageIsBroadcast(t *testing.T) {
	// Test broadcast message (empty To)
	broadcastMsg := &Message{To: []string{}}
	if !broadcastMsg.IsBroadcast() {
		t.Error("message with empty To should be broadcast")
	}

	// Test non-broadcast message
	directMsg := &Message{To: []string{"agent-1"}}
	if directMsg.IsBroadcast() {
		t.Error("message with To should not be broadcast")
	}

	// Test nil To
	nilMsg := &Message{To: nil}
	if !nilMsg.IsBroadcast() {
		t.Error("message with nil To should be broadcast")
	}
}

func TestMessageIsType(t *testing.T) {
	tests := []struct {
		name      string
		msg       *Message
		check     func(*Message) bool
		expected  bool
	}{
		{"Request", &Message{Type: MessageTypeRequest}, (*Message).IsRequest, true},
		{"Response", &Message{Type: MessageTypeResponse}, (*Message).IsResponse, true},
		{"Notify", &Message{Type: MessageTypeNotify}, (*Message).IsNotify, true},
		{"Event", &Message{Type: MessageTypeEvent}, (*Message).IsEvent, true},
		{"Stream", &Message{Type: MessageTypeStream}, (*Message).IsStream, true},
		{"Request not Response", &Message{Type: MessageTypeRequest}, (*Message).IsResponse, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.check(tt.msg) != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, !tt.expected)
			}
		})
	}
}

func TestMessageAddGetHeader(t *testing.T) {
	msg := &Message{Headers: make(map[string]interface{})}

	// Test add header
	msg.AddHeader("priority", "high")
	
	// Test get header
	val, exists := msg.GetHeader("priority")
	if !exists {
		t.Error("header should exist")
	}
	if val != "high" {
		t.Errorf("expected high, got %v", val)
	}

	// Test non-existent header
	_, exists = msg.GetHeader("nonexistent")
	if exists {
		t.Error("non-existent header should not exist")
	}
}

func TestMessageToJSON(t *testing.T) {
	msg := NewMessage(MessageTypeRequest, "agent-1", []string{"agent-2"}, "test", map[string]string{"key": "value"})

	data, err := msg.ToJSON()
	if err != nil {
		t.Fatalf("failed to marshal message: %v", err)
	}

	// Verify it's valid JSON
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}

	if parsed["from"] != "agent-1" {
		t.Errorf("expected from agent-1, got %v", parsed["from"])
	}
}

func TestMessageFromJSON(t *testing.T) {
	original := NewMessage(MessageTypeNotify, "agent-1", []string{}, "updates", "test payload")
	original.SessionID = "session-123"
	original.TraceID = "trace-456"

	data, err := original.ToJSON()
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	parsed, err := FromJSON(data)
	if err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if parsed.ID != original.ID {
		t.Errorf("expected ID %s, got %s", original.ID, parsed.ID)
	}
	if parsed.Type != original.Type {
		t.Errorf("expected type %s, got %s", original.Type, parsed.Type)
	}
	if parsed.SessionID != "session-123" {
		t.Errorf("expected session-123, got %s", parsed.SessionID)
	}
	if parsed.TraceID != "trace-456" {
		t.Errorf("expected trace-456, got %s", parsed.TraceID)
	}
}

func TestNewErrorResponse(t *testing.T) {
	original := NewMessage(MessageTypeRequest, "agent-1", []string{"agent-2"}, "test", nil)
	response := NewErrorResponse(original, "ERR_NOT_FOUND", "Agent not found", map[string]string{"id": "agent-2"})

	if response.Type != MessageTypeResponse {
		t.Errorf("expected response type, got %s", response.Type)
	}
	if response.ReplyTo != original.ID {
		t.Errorf("expected reply_to %s, got %s", original.ID, response.ReplyTo)
	}
	if response.Error == nil {
		t.Fatal("error should not be nil")
	}
	if response.Error.Code != "ERR_NOT_FOUND" {
		t.Errorf("expected code ERR_NOT_FOUND, got %s", response.Error.Code)
	}
	if response.Error.Message != "Agent not found" {
		t.Errorf("expected message 'Agent not found', got %s", response.Error.Message)
	}
}

func TestAgentStruct(t *testing.T) {
	agent := &Agent{
		ID:      "test-agent",
		Name:    "Test Agent",
		Version: "1.0.0",
		Capabilities: []Capability{
			{Name: "memory-search", Version: "1.0", Description: "Search agent memory"},
			{Name: "temporal-graph", Version: "1.0", Description: "Temporal graph operations"},
		},
		Status:   StatusActive,
		LastSeen: time.Now(),
		Endpoint: "http://localhost:8080/a2a",
		Metadata: map[string]interface{}{"team": "platform"},
	}

	data, err := json.Marshal(agent)
	if err != nil {
		t.Fatalf("failed to marshal agent: %v", err)
	}

	var parsed Agent
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal agent: %v", err)
	}

	if parsed.ID != "test-agent" {
		t.Errorf("expected ID test-agent, got %s", parsed.ID)
	}
	if len(parsed.Capabilities) != 2 {
		t.Errorf("expected 2 capabilities, got %d", len(parsed.Capabilities))
	}
	if parsed.Status != StatusActive {
		t.Errorf("expected status active, got %s", parsed.Status)
	}
}

func TestSessionStruct(t *testing.T) {
	now := time.Now()
	session := &Session{
		ID:           "session-123",
		Initiator:    "agent-1",
		Participants: []string{"agent-1", "agent-2", "agent-3"},
		StartedAt:    now,
		Metadata:     map[string]interface{}{"purpose": "code-review"},
	}

	data, err := json.Marshal(session)
	if err != nil {
		t.Fatalf("failed to marshal session: %v", err)
	}

	var parsed Session
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal session: %v", err)
	}

	if parsed.ID != "session-123" {
		t.Errorf("expected ID session-123, got %s", parsed.ID)
	}
	if len(parsed.Participants) != 3 {
		t.Errorf("expected 3 participants, got %d", len(parsed.Participants))
	}
}

func TestSubscriptionStruct(t *testing.T) {
	sub := &Subscription{
		ID:        "sub-123",
		AgentID:   "agent-1",
		Topics:    []string{"memory-updates", "graph-changes"},
		Filter:    "type=event",
		CreatedAt: time.Now(),
		Active:    true,
	}

	data, err := json.Marshal(sub)
	if err != nil {
		t.Fatalf("failed to marshal subscription: %v", err)
	}

	var parsed Subscription
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal subscription: %v", err)
	}

	if parsed.ID != "sub-123" {
		t.Errorf("expected ID sub-123, got %s", parsed.ID)
	}
	if len(parsed.Topics) != 2 {
		t.Errorf("expected 2 topics, got %d", len(parsed.Topics))
	}
	if !parsed.Active {
		t.Error("subscription should be active")
	}
}

func TestCapabilityManifest(t *testing.T) {
	manifest := &CapabilityManifest{
		Version: "1.0.0",
		Agent: Agent{
			ID:      "cortex-memory",
			Name:    "Cortex Memory Agent",
			Version: "1.0.0",
		},
		Interfaces: []CapabilityInterface{
			{
				Name:    "memory",
				Version: "1.0.0",
				Methods: []Method{
					{
						Name:        "search",
						Description: "Search agent memory",
						Input:       Schema{Type: "object"},
						Output:      Schema{Type: "array"},
					},
				},
			},
		},
		Protocols: []Protocol{
			{
				Name:      "A2A",
				Version:   "1.0.0",
				Transport: "stdio",
			},
		},
	}

	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}

	// Verify it's valid JSON
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}

	if parsed["version"] != "1.0.0" {
		t.Errorf("expected version 1.0.0, got %v", parsed["version"])
	}
}