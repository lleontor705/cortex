package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type captureAuditSink struct {
	events []AuditEvent
	err    error
}

func (s *captureAuditSink) Record(_ context.Context, event AuditEvent) error {
	s.events = append(s.events, event)
	return s.err
}

type captureAuditTelemetry struct{ failures []AuditFailure }

func (t *captureAuditTelemetry) AuditDeliveryFailed(failure AuditFailure) {
	t.failures = append(t.failures, failure)
}

func TestAuditorRecordsOnlyBoundedOperationalMetadata(t *testing.T) {
	sink := &captureAuditSink{}
	auditor := Auditor{Sink: sink}
	metadata := AuditEvent{
		CorrelationID: "req-123", ActorID: "actor-1", TenantID: "tenant-1",
		WorkspaceID: "workspace-1", Project: "cortex", Transport: TransportJSON,
		ResultClass: "success", Duration: 125 * time.Millisecond,
		InputTokens: 44, OutputTokens: 12, SourceCount: 2,
		Confidence: ConfidenceHigh, Degraded: []string{DegradedCodeUnavailable},
	}
	if err := auditor.RecordAuthorization(context.Background(), metadata); err != nil {
		t.Fatalf("RecordAuthorization: %v", err)
	}
	auditor.RecordOutcome(context.Background(), metadata)
	if len(sink.events) != 2 || sink.events[0].Phase != AuditPhaseAuthorization || sink.events[1].Phase != AuditPhaseOutcome {
		t.Fatalf("events = %#v", sink.events)
	}
	raw, err := json.Marshal(sink.events)
	if err != nil {
		t.Fatalf("marshal audit: %v", err)
	}
	for _, forbidden := range []string{"question", "history", "answer", "content", "embedding", "provider_url", "api_key", "secret"} {
		if strings.Contains(strings.ToLower(string(raw)), forbidden) {
			t.Fatalf("audit schema contains forbidden field %q: %s", forbidden, raw)
		}
	}
}

func TestAuditorFailsClosedBeforeProviderAndReportsOutcomeFailure(t *testing.T) {
	sinkErr := errors.New("audit database unavailable")
	sink := &captureAuditSink{err: sinkErr}
	telemetry := &captureAuditTelemetry{}
	auditor := Auditor{Sink: sink, Telemetry: telemetry}
	event := AuditEvent{CorrelationID: "req-123", ActorID: "actor", TenantID: "tenant", WorkspaceID: "workspace", Project: "cortex", Transport: TransportStream, ResultClass: "authorized"}

	err := auditor.RecordAuthorization(context.Background(), event)
	var agentErr *Error
	if !errors.As(err, &agentErr) || agentErr.Code != ErrorAuditUnavailable || !errors.Is(err, sinkErr) {
		t.Fatalf("authorization error = %v, want audit_unavailable wrapping sink error", err)
	}
	auditor.RecordOutcome(context.Background(), event)
	if len(telemetry.failures) != 1 {
		t.Fatalf("telemetry failures = %#v", telemetry.failures)
	}
	failure := telemetry.failures[0]
	if failure.ErrorClass != AuditErrorSinkDelivery || failure.CorrelationID != event.CorrelationID || failure.Project != event.Project {
		t.Fatalf("telemetry failure = %#v", failure)
	}
	raw, _ := json.Marshal(telemetry.failures[0])
	for _, forbidden := range []string{sinkErr.Error(), event.ActorID, event.TenantID, event.WorkspaceID, "question", "answer", "context", "secret"} {
		if strings.Contains(strings.ToLower(string(raw)), strings.ToLower(forbidden)) {
			t.Fatalf("telemetry leaked forbidden detail %q: %s", forbidden, raw)
		}
	}
}

func TestAuditorRejectsInvalidMetadataBeforeSink(t *testing.T) {
	sink := &captureAuditSink{}
	err := (Auditor{Sink: sink}).RecordAuthorization(context.Background(), AuditEvent{CorrelationID: strings.Repeat("x", 257)})
	var agentErr *Error
	if !errors.As(err, &agentErr) || agentErr.Code != ErrorAuditUnavailable {
		t.Fatalf("error = %v, want audit_unavailable", err)
	}
	if len(sink.events) != 0 {
		t.Fatalf("invalid metadata reached sink: %#v", sink.events)
	}
}
