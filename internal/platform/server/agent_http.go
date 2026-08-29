package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/lleontor705/cortex/v2/internal/domain"
	agentdomain "github.com/lleontor705/cortex/v2/internal/domain/agent"
)

type agentAnswerer interface {
	Answer(context.Context, serverAgentRequest) (agentdomain.Answer, error)
	Stream(context.Context, serverAgentRequest, agentdomain.StreamCallbacks) (agentdomain.Answer, error)
}

type agentAuditorFactory func(context.Context, domain.Principal) (agentdomain.Auditor, error)

type agentAnswerInput struct {
	ProjectID string                `json:"project_id"`
	Question  string                `json:"question"`
	History   []agentdomain.Message `json:"history,omitempty"`
}

func (a *apiHandler) agentProjects(w http.ResponseWriter, r *http.Request) {
	setAgentNoStore(w)
	projects, err := a.ops.ListAgentProjects(r.Context())
	if err != nil {
		respondAgentOperationError(w, err)
		return
	}
	result := make([]agentProject, 0, len(projects))
	for id, label := range projects {
		result = append(result, agentProject{ID: id, Label: label})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Label != result[j].Label {
			return result[i].Label < result[j].Label
		}
		return result[i].ID < result[j].ID
	})
	writeJSON(w, http.StatusOK, map[string]any{"projects": result})
}

func (a *apiHandler) agentAnswer(w http.ResponseWriter, r *http.Request) {
	prepared, ok := a.prepareAgentRequest(w, r, agentdomain.TransportJSON)
	if !ok {
		return
	}
	defer prepared.release(prepared.limits.DefaultOutputTokens)
	var answer agentdomain.Answer
	var outcomeErr error
	defer func() { prepared.recordOutcome(r.Context(), answer, outcomeErr) }()

	ctx, cancel := agentdomain.WithRequestTimeout(r.Context(), prepared.limits, agentdomain.TransportJSON)
	defer cancel()
	answer, outcomeErr = a.agent.Answer(ctx, prepared.request)
	if outcomeErr != nil {
		if ctx.Err() != nil {
			outcomeErr = &agentdomain.Error{Code: agentdomain.ContextErrorCode(ctx.Err()), Err: ctx.Err()}
		}
		respondAgentError(w, outcomeErr)
		return
	}
	writeJSON(w, http.StatusOK, answer)
}

type preparedAgentRequest struct {
	request serverAgentRequest
	limits  agentdomain.Limits
	release func(int)
	auditor agentdomain.Auditor
	audit   agentdomain.AuditEvent
	started time.Time
	outcome *sync.Once
}

func (p preparedAgentRequest) recordOutcome(ctx context.Context, answer agentdomain.Answer, err error) {
	if p.outcome == nil {
		return
	}
	p.outcome.Do(func() {
		event := p.audit
		event.ResultClass = agentResultClass(err)
		event.Duration = time.Since(p.started)
		event.SourceCount = len(answer.Sources)
		event.Confidence = answer.Confidence.Level
		event.Degraded = append([]string(nil), answer.Retrieval.Degraded...)
		event.InputTokens = answer.Usage.InputTokens
		event.OutputTokens = answer.Usage.OutputTokens
		auditCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		p.auditor.RecordOutcome(auditCtx, event)
	})
}

func agentResultClass(err error) string {
	if err == nil {
		return "success"
	}
	var agentErr *agentdomain.Error
	if errors.As(err, &agentErr) {
		return string(agentErr.Code)
	}
	return string(agentdomain.ErrorProviderUnavailable)
}

func (a *apiHandler) prepareAgentRequest(w http.ResponseWriter, r *http.Request, transport agentdomain.Transport) (preparedAgentRequest, bool) {
	setAgentNoStore(w)
	if a.agent == nil || a.agentQuota == nil {
		writeError(w, http.StatusServiceUnavailable, "agent_unavailable", "project agent is not configured")
		return preparedAgentRequest{}, false
	}
	var input agentAnswerInput
	if !decodeAgentBody(w, r, &input) {
		return preparedAgentRequest{}, false
	}
	if code, status := validateAgentInput(input); code != "" {
		writeError(w, status, code, "agent request is invalid")
		return preparedAgentRequest{}, false
	}

	principal, ok := principalFromContext(r.Context())
	if !ok {
		writeUnauthorized(w)
		return preparedAgentRequest{}, false
	}
	workspaceID, ok := workspaceFromContext(r.Context())
	if !ok {
		if a.cfg.Server.MultiTenant {
			writeError(w, http.StatusUnauthorized, "unauthorized", "verified workspace is required")
			return preparedAgentRequest{}, false
		}
		workspaceID = strings.TrimSpace(a.cfg.Server.WorkspaceID)
	}
	if workspaceID == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized", "verified workspace is required")
		return preparedAgentRequest{}, false
	}
	projects, err := a.ops.ListAgentProjects(r.Context())
	if err != nil {
		respondAgentOperationError(w, err)
		return preparedAgentRequest{}, false
	}
	projectID := strings.TrimSpace(input.ProjectID)
	label, granted := projects[projectID]
	if !granted {
		writeError(w, http.StatusForbidden, "project_not_granted", "project is not granted")
		return preparedAgentRequest{}, false
	}
	if a.agentAuditor == nil {
		respondAgentError(w, &agentdomain.Error{Code: agentdomain.ErrorAuditUnavailable})
		return preparedAgentRequest{}, false
	}
	auditor, err := a.agentAuditor(r.Context(), principal)
	if err != nil {
		respondAgentError(w, &agentdomain.Error{Code: agentdomain.ErrorAuditUnavailable, Err: err})
		return preparedAgentRequest{}, false
	}
	auditEvent := agentdomain.AuditEvent{
		CorrelationID: uuid.NewString(), ActorID: principal.Subject, TenantID: principal.OrgID,
		WorkspaceID: workspaceID, Project: projectID, Transport: transport, ResultClass: "authorized",
	}
	started := time.Now()
	if err := auditor.RecordAuthorization(r.Context(), auditEvent); err != nil {
		respondAgentError(w, err)
		return preparedAgentRequest{}, false
	}
	prepared := preparedAgentRequest{auditor: auditor, audit: auditEvent, started: started, outcome: new(sync.Once)}

	tier, err := agentLimitTierFromPrincipal(principal)
	if err != nil {
		prepared.recordOutcome(r.Context(), agentdomain.Answer{}, &agentdomain.Error{Code: agentdomain.ErrorProviderUnavailable, Err: err})
		writeError(w, http.StatusServiceUnavailable, "agent_unavailable", "project agent is not configured")
		return preparedAgentRequest{}, false
	}
	limits, err := a.agentLimits.ForTier(tier)
	if err != nil {
		prepared.recordOutcome(r.Context(), agentdomain.Answer{}, &agentdomain.Error{Code: agentdomain.ErrorProviderUnavailable, Err: err})
		writeError(w, http.StatusServiceUnavailable, "agent_unavailable", "project agent is not configured")
		return preparedAgentRequest{}, false
	}
	tokenKey := principal.GrantDigest
	if tokenKey == "" {
		tokenKey = fmt.Sprintf("%s:%d", principal.Subject, principal.GrantVersion)
	}
	release, err := a.agentQuota.acquire(r.Context(), agentAdmission{
		TenantID: principal.OrgID, TokenID: tokenKey, Tier: string(tier),
		Transport: transport, EstimatedTokens: limits.DefaultOutputTokens,
	})
	if err != nil {
		prepared.recordOutcome(r.Context(), agentdomain.Answer{}, err)
		respondAgentError(w, err)
		return preparedAgentRequest{}, false
	}
	prepared.request = serverAgentRequest{
		tenantID: principal.OrgID, workspaceID: workspaceID,
		project: agentProject{ID: projectID, Label: label}, question: input.Question, history: input.History,
	}
	prepared.limits = limits
	prepared.release = release
	return prepared, true
}

func agentLimitTierFromPrincipal(principal domain.Principal) (agentdomain.LimitTier, error) {
	tier := agentdomain.LimitTier(principal.RateLimitTier)
	switch tier {
	case agentdomain.TierLimited, agentdomain.TierStandard, agentdomain.TierElevated:
		return tier, nil
	default:
		return "", agentdomain.ErrUnknownLimitTier
	}
}

func (a *apiHandler) agentStream(w http.ResponseWriter, r *http.Request) {
	prepared, ok := a.prepareAgentRequest(w, r, agentdomain.TransportStream)
	if !ok {
		return
	}
	defer prepared.release(prepared.limits.DefaultOutputTokens)
	var answer agentdomain.Answer
	var outcomeErr error
	defer func() { prepared.recordOutcome(r.Context(), answer, outcomeErr) }()

	flusher, ok := w.(http.Flusher)
	if !ok {
		outcomeErr = &agentdomain.Error{Code: agentdomain.ErrorProviderUnavailable, Err: errors.New("stream response writer does not support flushing")}
		writeError(w, http.StatusInternalServerError, "stream_unavailable", "streaming is unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ctx, cancel := agentdomain.WithRequestTimeout(r.Context(), prepared.limits, agentdomain.TransportStream)
	defer cancel()
	answer, outcomeErr = a.agent.Stream(ctx, prepared.request, agentdomain.StreamCallbacks{
		Meta: func(status agentdomain.RetrievalStatus) error {
			if !writeAgentSSE(w, flusher, "meta", map[string]any{"retrieval": status}) {
				return context.Canceled
			}
			return nil
		},
		Delta: func(text string) error {
			if !writeAgentSSE(w, flusher, "delta", map[string]string{"text": text}) {
				return context.Canceled
			}
			return nil
		},
		Sources: func(sources []agentdomain.Source) error {
			if !writeAgentSSE(w, flusher, "sources", sources) {
				return context.Canceled
			}
			return nil
		},
	})
	if outcomeErr != nil {
		if ctx.Err() != nil {
			outcomeErr = &agentdomain.Error{Code: agentdomain.ContextErrorCode(ctx.Err()), Err: ctx.Err()}
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			return
		}
		writeAgentStreamError(w, flusher, outcomeErr)
		return
	}
	if ctx.Err() != nil {
		outcomeErr = &agentdomain.Error{Code: agentdomain.ContextErrorCode(ctx.Err()), Err: ctx.Err()}
		return
	}
	if !writeAgentSSE(w, flusher, "done", answer) {
		outcomeErr = &agentdomain.Error{Code: agentdomain.ErrorRequestCancelled, Err: context.Canceled}
	}
}

func writeAgentSSE(w http.ResponseWriter, flusher http.Flusher, event string, payload any) bool {
	data, err := json.Marshal(payload)
	if err != nil {
		return false
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data); err != nil {
		return false
	}
	flusher.Flush()
	return true
}

func writeAgentStreamError(w http.ResponseWriter, flusher http.Flusher, err error) {
	status, code, message := agentErrorResponse(err)
	writeAgentSSE(w, flusher, "error", map[string]any{"status": status, "code": code, "message": message})
}

func agentErrorResponse(err error) (int, string, string) {
	var agentErr *agentdomain.Error
	if !errors.As(err, &agentErr) {
		return http.StatusServiceUnavailable, "provider_unavailable", "agent provider is unavailable"
	}
	switch agentErr.Code {
	case agentdomain.ErrorInvalidRequest, agentdomain.ErrorInvalidHistoryRole:
		return http.StatusBadRequest, string(agentErr.Code), "agent request is invalid"
	case agentdomain.ErrorQuestionTooLarge, agentdomain.ErrorHistoryTooLarge:
		return http.StatusRequestEntityTooLarge, string(agentErr.Code), "agent request is too large"
	case agentdomain.ErrorQuotaExceeded:
		return http.StatusTooManyRequests, string(agentErr.Code), "agent quota exceeded"
	case agentdomain.ErrorAgentTimeout:
		return http.StatusGatewayTimeout, string(agentErr.Code), "agent request timed out"
	case agentdomain.ErrorRequestCancelled:
		return 499, string(agentErr.Code), "agent request was cancelled"
	case agentdomain.ErrorAuditUnavailable:
		return http.StatusServiceUnavailable, string(agentErr.Code), "agent audit is unavailable"
	default:
		return http.StatusServiceUnavailable, "provider_unavailable", "agent provider is unavailable"
	}
}

func setAgentNoStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
}

func decodeAgentBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body is too large")
		} else {
			writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		}
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must contain one JSON value")
		return false
	}
	return true
}

func validateAgentInput(input agentAnswerInput) (string, int) {
	if strings.TrimSpace(input.ProjectID) == "" || strings.TrimSpace(input.Question) == "" {
		return "invalid_request", http.StatusBadRequest
	}
	if len([]byte(input.Question)) > agentdomain.MaxQuestionBytes {
		return "question_too_large", http.StatusRequestEntityTooLarge
	}
	if len(input.History) > agentdomain.MaxHistoryMessages {
		return "history_too_large", http.StatusRequestEntityTooLarge
	}
	total := 0
	for _, message := range input.History {
		if message.Role != agentdomain.RoleUser && message.Role != agentdomain.RoleAssistant {
			return "invalid_history_role", http.StatusBadRequest
		}
		if len([]byte(message.Content)) > agentdomain.MaxHistoryMessageBytes {
			return "history_too_large", http.StatusRequestEntityTooLarge
		}
		total += len([]byte(message.Content))
	}
	if total > agentdomain.MaxHistoryBytes {
		return "history_too_large", http.StatusRequestEntityTooLarge
	}
	return "", 0
}

func respondAgentOperationError(w http.ResponseWriter, err error) {
	if isAuthorizationDenial(err) {
		writeError(w, http.StatusForbidden, "forbidden", "principal is not authorized for this operation")
		return
	}
	writeError(w, http.StatusServiceUnavailable, "agent_unavailable", "project agent is unavailable")
}

func respondAgentError(w http.ResponseWriter, err error) {
	var agentErr *agentdomain.Error
	if errors.As(err, &agentErr) && agentErr.Code == agentdomain.ErrorQuotaExceeded {
		var quota *agentdomain.QuotaError
		if errors.As(err, &quota) {
			seconds := int(quota.RetryAfter.Seconds())
			if seconds < 1 {
				seconds = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
		}
	}
	status, code, message := agentErrorResponse(err)
	writeError(w, status, code, message)
}
