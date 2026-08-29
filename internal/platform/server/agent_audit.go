package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/lleontor705/cortex/v2/internal/authz"
	agentdomain "github.com/lleontor705/cortex/v2/internal/domain/agent"
)

// agentAuditSinkAdapter writes the agent's closed metadata schema through the
// existing tenant-bound, append-only PostgreSQL authorization audit sink.
// Conversational content and provider configuration are not representable.
type agentAuditSinkAdapter struct{ sink authz.AuditSink }

func newAgentAuditor(sink authz.AuditSink) agentdomain.Auditor {
	return agentdomain.Auditor{
		Sink:      agentAuditSinkAdapter{sink: sink},
		Telemetry: agentAuditTelemetry{logger: log.Default()},
	}
}

// agentAuditTelemetry emits a closed, content-free operational record through
// the server's existing process logger. Sink errors are represented only by a
// sanitized domain error class and are never interpolated into the record.
type agentAuditTelemetry struct{ logger *log.Logger }

func (t agentAuditTelemetry) AuditDeliveryFailed(failure agentdomain.AuditFailure) {
	if t.logger == nil {
		return
	}
	record := struct {
		Event         string `json:"event"`
		CorrelationID string `json:"correlation_id"`
		Project       string `json:"project"`
		ResultClass   string `json:"result_class"`
		SourceCount   int    `json:"source_count"`
		DurationMS    int64  `json:"duration_ms"`
		ErrorClass    string `json:"error_class"`
	}{
		Event: "agent_audit_delivery_failed", CorrelationID: failure.CorrelationID,
		Project: failure.Project, ResultClass: failure.ResultClass, SourceCount: failure.SourceCount,
		DurationMS: failure.Duration.Milliseconds(), ErrorClass: failure.ErrorClass,
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return
	}
	t.logger.Printf("%s", payload)
}

func (a agentAuditSinkAdapter) Record(ctx context.Context, event agentdomain.AuditEvent) error {
	if a.sink == nil {
		return fmt.Errorf("agent audit sink unavailable")
	}
	reason := fmt.Sprintf("result=%s transport=%s duration_ms=%d input_tokens=%d output_tokens=%d sources=%d confidence=%s degraded=%v",
		event.ResultClass, event.Transport, event.Duration.Milliseconds(), event.InputTokens, event.OutputTokens,
		event.SourceCount, event.Confidence, event.Degraded)
	return a.sink.Record(ctx, authz.AuditEvent{
		CorrelationID: event.CorrelationID, Actor: event.ActorID,
		Action: "agent." + string(event.Phase), Resource: "agent", ResourceID: event.Project,
		Reason: reason, Allowed: true,
	})
}
