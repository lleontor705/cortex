package server

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/authz"
	agentdomain "github.com/lleontor705/cortex/v2/internal/domain/agent"
)

type captureAuthzAudit struct {
	events []authz.AuditEvent
	err    error
}

func (s *captureAuthzAudit) Record(_ context.Context, event authz.AuditEvent) error {
	s.events = append(s.events, event)
	return s.err
}

func TestNewAgentAuditorReportsOutcomeFailureWithoutContent(t *testing.T) {
	var output bytes.Buffer
	previousWriter, previousFlags, previousPrefix := log.Writer(), log.Flags(), log.Prefix()
	log.SetOutput(&output)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() {
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
		log.SetPrefix(previousPrefix)
	})

	sink := &captureAuthzAudit{err: errors.New("question=hidden answer=hidden context=hidden api_key=hidden")}
	auditor := newAgentAuditor(sink)
	auditor.RecordOutcome(context.Background(), agentdomain.AuditEvent{
		CorrelationID: "request-id", ActorID: "actor-id", TenantID: "tenant-id",
		WorkspaceID: "workspace-id", Project: agentProjectID, Transport: agentdomain.TransportJSON,
		ResultClass: "provider_unavailable", Duration: 25 * time.Millisecond, SourceCount: 2,
	})

	telemetry := output.String()
	for _, expected := range []string{"agent_audit_delivery_failed", "request-id", agentProjectID, "provider_unavailable", "source_count", "duration_ms", "audit_sink_delivery"} {
		if !strings.Contains(telemetry, expected) {
			t.Fatalf("telemetry %q does not contain %q", telemetry, expected)
		}
	}
	for _, forbidden := range []string{"question", "answer", "context", "api_key", "hidden", "actor-id", "tenant-id", "workspace-id"} {
		if strings.Contains(strings.ToLower(telemetry), forbidden) {
			t.Fatalf("telemetry leaked forbidden field/value %q: %s", forbidden, telemetry)
		}
	}
}

func TestAgentAuditAdapterPersistsOnlyBoundedMetadata(t *testing.T) {
	sink := &captureAuthzAudit{}
	err := (agentAuditSinkAdapter{sink: sink}).Record(context.Background(), agentdomain.AuditEvent{
		Phase: agentdomain.AuditPhaseOutcome, CorrelationID: "request-id", ActorID: "actor-id",
		TenantID: "tenant-id", WorkspaceID: "workspace-id", Project: agentProjectID,
		Transport: agentdomain.TransportJSON, ResultClass: "success", Duration: 15 * time.Millisecond,
		InputTokens: 10, OutputTokens: 5, SourceCount: 2, Confidence: agentdomain.ConfidenceHigh,
		Degraded: []string{agentdomain.DegradedCodeUnavailable},
	})
	if err != nil || len(sink.events) != 1 {
		t.Fatalf("Record() err=%v events=%#v", err, sink.events)
	}
	event := sink.events[0]
	if event.Action != "agent.outcome" || event.Resource != "agent" || event.ResourceID != agentProjectID || !event.Allowed || !strings.Contains(event.Reason, "sources=2") {
		t.Fatalf("persisted event=%#v", event)
	}
}
