package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/neodymium6/cluster-observer-mcp/internal/kubernetes"
	"github.com/neodymium6/cluster-observer-mcp/internal/observer"
)

const auditSchemaVersion = 1

const (
	auditSuccess               = "success"
	auditValidationError       = "validation_error"
	auditTargetNotConfigured   = "target_not_configured"
	auditScopeNotConfigured    = "scope_not_configured"
	auditSourceTimeout         = "source_timeout"
	auditSourceUnavailable     = "source_unavailable"
	auditSourceRejected        = "source_rejected"
	auditCredentialUnavailable = "credential_unavailable"
	auditInvalidSourceResponse = "invalid_source_response"
	auditResultTooLarge        = "result_too_large"
	auditCanceled              = "canceled"
	auditUnsupportedTool       = "unsupported_tool"
	auditInternalError         = "internal_error"
)

type auditEvent struct {
	Timestamp     string `json:"timestamp"`
	SchemaVersion int    `json:"schemaVersion"`
	Tool          string `json:"tool"`
	Target        string `json:"target,omitempty"`
	Scope         string `json:"scope,omitempty"`
	DurationMS    int64  `json:"durationMs"`
	Outcome       string `json:"outcome"`
	ItemCount     *int   `json:"itemCount,omitempty"`
	Partial       *bool  `json:"partial,omitempty"`
	Truncated     *bool  `json:"truncated,omitempty"`
}

type auditLogger struct {
	mu      sync.Mutex
	encoder *json.Encoder
	now     func() time.Time
}

func newAuditLogger(writer io.Writer, now func() time.Time) *auditLogger {
	if writer == nil {
		return nil
	}
	if now == nil {
		now = time.Now
	}
	return &auditLogger{encoder: json.NewEncoder(writer), now: now}
}

func (l *auditLogger) middleware() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(
			ctx context.Context,
			method string,
			request mcp.Request,
		) (mcp.Result, error) {
			if method != "tools/call" {
				return next(ctx, method, request)
			}

			startedAt := l.now()
			event, knownTool := auditRequest(request)
			result, callErr := next(ctx, method, request)
			finishedAt := l.now()
			event.Timestamp = finishedAt.UTC().Format(time.RFC3339Nano)
			event.SchemaVersion = auditSchemaVersion
			event.DurationMS = max(finishedAt.Sub(startedAt).Milliseconds(), 0)
			event.Outcome = auditOutcome(ctx, knownTool, result, callErr)
			if event.Outcome == auditValidationError || event.Outcome == auditUnsupportedTool {
				event.Target = ""
				event.Scope = ""
			}
			if event.Outcome == auditSuccess {
				addAuditResult(&event, result)
			}
			l.write(event)
			return result, callErr
		}
	}
}

func (l *auditLogger) write(event auditEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()
	_ = l.encoder.Encode(event)
}

func auditRequest(request mcp.Request) (auditEvent, bool) {
	event := auditEvent{Tool: "unknown"}
	params, ok := request.GetParams().(*mcp.CallToolParamsRaw)
	if !ok {
		return event, false
	}
	switch params.Name {
	case ToolListTargets:
		event.Tool = ToolListTargets
		return event, true
	case ToolGetClusterHealth, ToolListUnhealthyWorkloads:
		event.Tool = params.Name
	default:
		return event, false
	}

	var input struct {
		Target string `json:"target"`
		Scope  string `json:"scope"`
	}
	if err := json.Unmarshal(params.Arguments, &input); err != nil {
		return event, true
	}
	if observer.ValidateIdentifier("target", input.Target) == nil {
		event.Target = input.Target
	}
	if input.Scope != "" && observer.ValidateIdentifier("scope", input.Scope) == nil {
		event.Scope = input.Scope
	}
	return event, true
}

func auditOutcome(
	ctx context.Context,
	knownTool bool,
	result mcp.Result,
	callErr error,
) string {
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return auditCanceled
	}
	if !knownTool {
		return auditUnsupportedTool
	}
	if callErr != nil {
		return auditInternalError
	}
	toolResult, ok := result.(*mcp.CallToolResult)
	if !ok {
		return auditInternalError
	}
	toolErr := toolResult.GetError()
	if toolErr == nil {
		if toolResult.IsError {
			return auditInternalError
		}
		return auditSuccess
	}

	var validationError *observer.ValidationError
	switch {
	case errors.As(toolErr, &validationError):
		return auditValidationError
	case errors.Is(toolErr, errTargetNotConfigured):
		return auditTargetNotConfigured
	case errors.Is(toolErr, kubernetes.ErrUnknownScope):
		return auditScopeNotConfigured
	case errors.Is(toolErr, kubernetes.ErrSourceTimeout):
		return auditSourceTimeout
	case errors.Is(toolErr, kubernetes.ErrSourceUnavailable):
		return auditSourceUnavailable
	case errors.Is(toolErr, kubernetes.ErrSourceRejected):
		return auditSourceRejected
	case errors.Is(toolErr, kubernetes.ErrCredentialUnavailable):
		return auditCredentialUnavailable
	case errors.Is(toolErr, kubernetes.ErrInvalidSourceResponse):
		return auditInvalidSourceResponse
	case errors.Is(toolErr, observer.ErrResultTooLarge):
		return auditResultTooLarge
	case errors.Is(toolErr, errObservationCanceled):
		return auditCanceled
	case errors.Is(toolErr, errObservationFailed):
		return auditInternalError
	default:
		// Errors created by the SDK while decoding or validating typed input do
		// not expose a stable public type. No other raw handler error can reach
		// this point because safeToolError maps it to errObservationFailed.
		return auditValidationError
	}
}

func addAuditResult(event *auditEvent, result mcp.Result) {
	toolResult, ok := result.(*mcp.CallToolResult)
	if !ok {
		return
	}
	encoded, ok := toolResult.StructuredContent.(json.RawMessage)
	if !ok {
		return
	}
	switch event.Tool {
	case ToolListTargets:
		var output observer.ListTargetsOutput
		if json.Unmarshal(encoded, &output) == nil {
			count := len(output.Targets)
			event.ItemCount = &count
		}
	case ToolGetClusterHealth:
		var output observer.ClusterHealthOutput
		if json.Unmarshal(encoded, &output) == nil {
			partial := output.Partial
			event.Partial = &partial
		}
	case ToolListUnhealthyWorkloads:
		var output observer.ListUnhealthyWorkloadsOutput
		if json.Unmarshal(encoded, &output) == nil {
			count := len(output.Workloads)
			truncated := output.Truncated
			event.ItemCount = &count
			event.Truncated = &truncated
		}
	}
}
