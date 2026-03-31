// Package a2a provides A2A (Agent-to-Agent) Protocol message format and types
//
// This package implements the A2A Protocol message format and types as defined
// by the A2A specification. It provides structured message handling for
// inter-agent communication.
package a2a

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"time"
)

// MessageType defines A2A protocol message types
type MessageType string

const (
	// Request/Response pattern
	MessageTypeRequest  MessageType = "request"
	MessageTypeResponse MessageType = "response"

	// Notification and Events
	MessageTypeNotify MessageType = "notify"
	MessageTypeEvent  MessageType = "event"

	// Streaming data
	MessageTypeStream MessageType = "stream"
)

// Message represents an A2A protocol message with standardized headers and payload
type Message struct {
	ID        string                 `json:"id"`
	Type      MessageType            `json:"type"`
	From      string                 `json:"from"`
	To        []string               `json:"to"`
	Topic     string                 `json:"topic,omitempty"`
	Headers   map[string]interface{} `json:"headers,omitempty"`
	Timestamp time.Time              `json:"timestamp"`
	Payload   interface{}            `json:"payload"`
	Error     *Error                 `json:"error,omitempty"`
	SessionID string                 `json:"session_id,omitempty"`
	TraceID   string                 `json:"trace_id,omitempty"`
	ReplyTo   string                 `json:"reply_to,omitempty"`
	Sequence  int64                  `json:"sequence,omitempty"`
}

// Error represents A2A protocol error format
type Error struct {
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Details interface{} `json:"details,omitempty"`
}

// Agent represents an A2A protocol agent
type Agent struct {
	ID           string                 `json:"id"`
	Name         string                 `json:"name"`
	Version      string                 `json:"version"`
	Capabilities []Capability           `json:"capabilities"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
	Endpoint     string                 `json:"endpoint,omitempty"`
	Status       AgentStatus            `json:"status"`
	LastSeen     time.Time              `json:"last_seen"`
}

// Capability represents an agent capability
type Capability struct {
	Name        string `json:"name"`
	Version     string `json:"version,omitempty"`
	Description string `json:"description,omitempty"`
	Topics      []string `json:"topics,omitempty"`
}

// AgentStatus defines possible agent states
type AgentStatus string

const (
	StatusActive   AgentStatus = "active"
	StatusInactive AgentStatus = "inactive"
	StatusError    AgentStatus = "error"
	StatusUnknown  AgentStatus = "unknown"
)

// Session represents an A2A protocol session
type Session struct {
	ID           string                 `json:"id"`
	Initiator    string                 `json:"initiator"`
	Participants []string               `json:"participants"`
	StartedAt    time.Time              `json:"started_at"`
	EndedAt      *time.Time             `json:"ended_at,omitempty"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
}

// Subscription represents an agent subscription to topics
type Subscription struct {
	ID        string    `json:"id"`
	AgentID   string    `json:"agent_id"`
	Topics    []string  `json:"topics"`
	Filter    string    `json:"filter,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	Active    bool      `json:"active"`
}

// MessageAcknowledgement represents message delivery acknowledgment
type MessageAcknowledgement struct {
	MessageID string    `json:"message_id"`
	Recipient string    `json:"recipient"`
	Status    AckStatus `json:"status"`
	Timestamp time.Time `json:"timestamp"`
	Error     *Error    `json:"error,omitempty"`
}

// AckStatus defines acknowledgment status values
type AckStatus string

const (
	AckStatusSuccess AckStatus = "success"
	AckStatusFailed  AckStatus = "failed"
	AckStatusSkipped AckStatus = "skipped"
)

// CapabilityManifest represents a complete capability manifest
type CapabilityManifest struct {
	Version    string                `json:"version"`
	Agent      Agent                 `json:"agent"`
	Interfaces []CapabilityInterface `json:"interfaces"`
	Protocols  []Protocol            `json:"protocols"`
}

// CapabilityInterface represents a capability interface
type CapabilityInterface struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Methods     []Method `json:"methods"`
	Description string   `json:"description,omitempty"`
}

// Method represents a capability method
type Method struct {
	Name        string `json:"name"`
	Input       Schema `json:"input"`
	Output      Schema `json:"output"`
	Description string `json:"description,omitempty"`
}

// Schema represents JSON schema for method parameters
type Schema struct {
	Type       string            `json:"type"`
	Properties map[string]Schema `json:"properties,omitempty"`
	Items      *Schema           `json:"items,omitempty"`
	Required   []string          `json:"required,omitempty"`
}

// Protocol represents supported communication protocol
type Protocol struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
	Transport   string `json:"transport"`
}

// NewMessage creates a new A2A message
func NewMessage(msgType MessageType, from string, to []string, topic string, payload interface{}) *Message {
	return &Message{
		ID:        generateMessageID(),
		Type:      msgType,
		From:      from,
		To:        to,
		Topic:     topic,
		Payload:   payload,
		Timestamp: time.Now(),
		Headers:   make(map[string]interface{}),
	}
}

// NewErrorResponse creates an error response message
func NewErrorResponse(originalMessage *Message, errorCode, errorMessage string, details interface{}) *Message {
	to := originalMessage.From
	return &Message{
		ID:        generateMessageID(),
		Type:      MessageTypeResponse,
		From:      to,
		To:        []string{originalMessage.From},
		Topic:     originalMessage.Topic,
		Payload:   nil,
		Error: &Error{
			Code:    errorCode,
			Message: errorMessage,
			Details: details,
		},
		Timestamp: time.Now(),
		ReplyTo:   originalMessage.ID,
		Headers:   make(map[string]interface{}),
	}
}

// IsBroadcast returns true if this is a broadcast message (empty To field)
func (m *Message) IsBroadcast() bool {
	return len(m.To) == 0
}

// AddHeader adds a header to the message
func (m *Message) AddHeader(key string, value interface{}) {
	if m.Headers == nil {
		m.Headers = make(map[string]interface{})
	}
	m.Headers[key] = value
}

// GetHeader gets a header value from the message
func (m *Message) GetHeader(key string) (interface{}, bool) {
	if m.Headers == nil {
		return nil, false
	}
	value, exists := m.Headers[key]
	return value, exists
}

// ToJSON converts the message to JSON
func (m *Message) ToJSON() ([]byte, error) {
	return json.Marshal(m)
}

// FromJSON creates a message from JSON
func FromJSON(data []byte) (*Message, error) {
	var msg Message
	err := json.Unmarshal(data, &msg)
	if err != nil {
		return nil, err
	}
	return &msg, nil
}

// IsRequest returns true if this is a request message
func (m *Message) IsRequest() bool {
	return m.Type == MessageTypeRequest
}

// IsResponse returns true if this is a response message
func (m *Message) IsResponse() bool {
	return m.Type == MessageTypeResponse
}

// IsNotify returns true if this is a notification message
func (m *Message) IsNotify() bool {
	return m.Type == MessageTypeNotify
}

// IsEvent returns true if this is an event message
func (m *Message) IsEvent() bool {
	return m.Type == MessageTypeEvent
}

// IsStream returns true if this is a stream message
func (m *Message) IsStream() bool {
	return m.Type == MessageTypeStream
}

func generateMessageID() string {
	return fmt.Sprintf("a2a_%d_%s", time.Now().UnixNano(), randomString(8))
}

func randomString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}