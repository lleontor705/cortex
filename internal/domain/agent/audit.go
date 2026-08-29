package agent

import (
	"context"
	"fmt"
	"time"
)

type AuditPhase string

const (
	AuditPhaseAuthorization AuditPhase = "authorization"
	AuditPhaseOutcome       AuditPhase = "outcome"
)

// AuditEvent is deliberately a closed, metadata-only schema. Conversational
// content, evidence, credentials, embeddings and provider destinations have no
// representable field here.
type AuditEvent struct {
	Phase         AuditPhase    `json:"phase"`
	CorrelationID string        `json:"correlation_id"`
	ActorID       string        `json:"actor_id"`
	TenantID      string        `json:"tenant_id"`
	WorkspaceID   string        `json:"workspace_id"`
	Project       string        `json:"project"`
	Transport     Transport     `json:"transport"`
	ResultClass   string        `json:"result_class"`
	Duration      time.Duration `json:"duration_ns"`
	InputTokens   int           `json:"input_tokens"`
	OutputTokens  int           `json:"output_tokens"`
	SourceCount   int           `json:"source_count"`
	Confidence    string        `json:"confidence,omitempty"`
	Degraded      []string      `json:"degraded,omitempty"`
}

type AuditSink interface {
	Record(context.Context, AuditEvent) error
}

// AuditFailure is the only outcome-delivery failure exposed to telemetry. It
// intentionally excludes the sink error and all request content.
type AuditFailure struct {
	CorrelationID string        `json:"correlation_id"`
	Project       string        `json:"project"`
	ResultClass   string        `json:"result_class"`
	SourceCount   int           `json:"source_count"`
	Duration      time.Duration `json:"duration_ns"`
	ErrorClass    string        `json:"error_class"`
}

const (
	AuditErrorInvalidMetadata = "audit_invalid_metadata"
	AuditErrorSinkUnavailable = "audit_sink_unavailable"
	AuditErrorSinkDelivery    = "audit_sink_delivery"
)

type AuditTelemetry interface {
	AuditDeliveryFailed(AuditFailure)
}

type Auditor struct {
	Sink      AuditSink
	Telemetry AuditTelemetry
}

// RecordAuthorization is the mandatory pre-provider audit. It fails closed.
func (a Auditor) RecordAuthorization(ctx context.Context, event AuditEvent) error {
	event.Phase = AuditPhaseAuthorization
	if err := validateAuditEvent(event); err != nil {
		return &Error{Code: ErrorAuditUnavailable, Err: err}
	}
	if a.Sink == nil {
		return &Error{Code: ErrorAuditUnavailable, Err: fmt.Errorf("agent audit sink unavailable")}
	}
	if err := a.Sink.Record(ctx, cloneAuditEvent(event)); err != nil {
		return &Error{Code: ErrorAuditUnavailable, Err: err}
	}
	return nil
}

// RecordOutcome is best effort because a completed answer cannot be recalled.
// Delivery failure is reported using content-free telemetry only.
func (a Auditor) RecordOutcome(ctx context.Context, event AuditEvent) {
	event.Phase = AuditPhaseOutcome
	errorClass := AuditErrorInvalidMetadata
	if err := validateAuditEvent(event); err == nil {
		if a.Sink == nil {
			errorClass = AuditErrorSinkUnavailable
		} else if err = a.Sink.Record(ctx, cloneAuditEvent(event)); err == nil {
			return
		} else {
			errorClass = AuditErrorSinkDelivery
		}
	}
	if a.Telemetry != nil {
		duration := event.Duration
		if duration < 0 {
			duration = 0
		}
		sourceCount := event.SourceCount
		if sourceCount < 0 {
			sourceCount = 0
		}
		a.Telemetry.AuditDeliveryFailed(AuditFailure{
			CorrelationID: bounded(event.CorrelationID, 256), Project: bounded(event.Project, 256),
			ResultClass: bounded(event.ResultClass, 256), SourceCount: sourceCount,
			Duration: duration, ErrorClass: errorClass,
		})
	}
}

func validateAuditEvent(event AuditEvent) error {
	for name, value := range map[string]string{
		"correlation_id": event.CorrelationID, "actor_id": event.ActorID, "tenant_id": event.TenantID,
		"workspace_id": event.WorkspaceID, "project": event.Project, "result_class": event.ResultClass,
	} {
		if value == "" || len([]byte(value)) > 256 {
			return fmt.Errorf("invalid %s", name)
		}
	}
	if event.Transport != TransportJSON && event.Transport != TransportStream {
		return fmt.Errorf("invalid transport")
	}
	if event.Duration < 0 || event.InputTokens < 0 || event.OutputTokens < 0 || event.SourceCount < 0 || len(event.Degraded) > 16 {
		return fmt.Errorf("invalid audit counters")
	}
	for _, reason := range event.Degraded {
		if reason == "" || len([]byte(reason)) > 64 {
			return fmt.Errorf("invalid degradation reason")
		}
	}
	return nil
}

func cloneAuditEvent(event AuditEvent) AuditEvent {
	event.Degraded = append([]string(nil), event.Degraded...)
	return event
}

func bounded(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	return value[:maximum]
}
