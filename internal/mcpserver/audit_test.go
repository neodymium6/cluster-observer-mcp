package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/neodymium6/cluster-observer-mcp/internal/flux"
	"github.com/neodymium6/cluster-observer-mcp/internal/kubernetes"
	"github.com/neodymium6/cluster-observer-mcp/internal/observer"
	"github.com/neodymium6/cluster-observer-mcp/internal/sourcehttp"
)

func TestAuditSuccessEventsContainOnlyBoundedSummaries(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	source := &auditSource{}
	server, err := NewWithSourceSet("test", SourceSet{
		Kubernetes: []KubernetesSource{source},
		Monitoring: []MonitoringSource{&auditMonitoringSource{}},
		Flux:       []FluxSource{&auditFluxSource{}},
	}, Options{
		AuditWriter: &output,
		Now: func() time.Time {
			return time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatalf("NewWithOptions() error = %v", err)
	}
	session := connectTestClient(t, server)

	callAuditTool(t, session, ToolListTargets, map[string]any{})
	callAuditTool(t, session, ToolGetClusterHealth, map[string]any{
		"target": "cluster-a",
	})
	callAuditTool(t, session, ToolListUnhealthyWorkloads, map[string]any{
		"target": "cluster-a",
		"scope":  "system",
	})
	callAuditTool(t, session, ToolListActiveAlerts, map[string]any{
		"target": "monitoring-a",
	})
	callAuditTool(t, session, ToolGetScrapeHealth, map[string]any{
		"target": "monitoring-a",
	})
	callAuditTool(t, session, ToolListUnhealthyReconciliations, map[string]any{
		"target": "flux-a",
		"scope":  "system",
	})

	events := decodeAuditEvents(t, output.Bytes())
	if len(events) != 6 {
		t.Fatalf("audit event count = %d, want 6", len(events))
	}
	for _, event := range events {
		if event.Timestamp != "2026-08-03T12:00:00Z" ||
			event.SchemaVersion != auditSchemaVersion ||
			event.DurationMS != 0 || event.Outcome != auditSuccess {
			t.Fatalf("audit event metadata = %#v", event)
		}
	}
	if events[0].Tool != ToolListTargets || events[0].ItemCount == nil ||
		*events[0].ItemCount != 3 {
		t.Fatalf("target audit event = %#v", events[0])
	}
	if events[1].Tool != ToolGetClusterHealth || events[1].Target != "cluster-a" ||
		events[1].Partial == nil || !*events[1].Partial {
		t.Fatalf("health audit event = %#v", events[1])
	}
	if events[2].Tool != ToolListUnhealthyWorkloads ||
		events[2].Target != "cluster-a" || events[2].Scope != "system" ||
		events[2].ItemCount == nil || *events[2].ItemCount != 2 ||
		events[2].Truncated == nil || !*events[2].Truncated {
		t.Fatalf("workload audit event = %#v", events[2])
	}
	if events[3].Tool != ToolListActiveAlerts ||
		events[3].Target != "monitoring-a" || events[3].ItemCount == nil ||
		*events[3].ItemCount != 1 || events[3].Truncated == nil ||
		!*events[3].Truncated {
		t.Fatalf("alert audit event = %#v", events[3])
	}
	if events[4].Tool != ToolGetScrapeHealth ||
		events[4].Target != "monitoring-a" || events[4].ItemCount == nil ||
		*events[4].ItemCount != 2 || events[4].Partial == nil ||
		!*events[4].Partial {
		t.Fatalf("scrape audit event = %#v", events[4])
	}
	if events[5].Tool != ToolListUnhealthyReconciliations ||
		events[5].Target != "flux-a" || events[5].Scope != "system" ||
		events[5].ItemCount == nil || *events[5].ItemCount != 1 ||
		events[5].Truncated == nil || !*events[5].Truncated {
		t.Fatalf("Flux audit event = %#v", events[5])
	}
}

func TestAuditRedactsInvalidArgumentsAndSourceErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		source      *auditSource
		tool        string
		arguments   map[string]any
		wantOutcome string
		wantTarget  string
		wantTool    string
	}{
		{
			name:   "schema validation",
			source: &auditSource{},
			tool:   ToolGetClusterHealth,
			arguments: map[string]any{
				"target":      "cluster-a",
				"bearerToken": "clearly-fake-secret-value",
			},
			wantOutcome: auditValidationError,
			wantTool:    ToolGetClusterHealth,
		},
		{
			name: "source diagnostic",
			source: &auditSource{healthErr: errors.New(
				"clearly-fake-secret-value kubernetes.example.invalid fixture-system",
			)},
			tool:        ToolGetClusterHealth,
			arguments:   map[string]any{"target": "cluster-a"},
			wantOutcome: auditInternalError,
			wantTarget:  "cluster-a",
			wantTool:    ToolGetClusterHealth,
		},
		{
			name:        "cancellation",
			source:      &auditSource{healthErr: context.Canceled},
			tool:        ToolGetClusterHealth,
			arguments:   map[string]any{"target": "cluster-a"},
			wantOutcome: auditCanceled,
			wantTarget:  "cluster-a",
			wantTool:    ToolGetClusterHealth,
		},
		{
			name:        "unknown tool",
			source:      &auditSource{},
			tool:        "clearly-fake-secret-tool",
			arguments:   map[string]any{"token": "clearly-fake-secret-value"},
			wantOutcome: auditUnsupportedTool,
			wantTool:    "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var output bytes.Buffer
			server, err := NewWithOptions("test", []KubernetesSource{tt.source}, Options{
				AuditWriter: &output,
			})
			if err != nil {
				t.Fatalf("NewWithOptions() error = %v", err)
			}
			session := connectTestClient(t, server)
			_, _ = session.CallTool(context.Background(), &mcp.CallToolParams{
				Name:      tt.tool,
				Arguments: tt.arguments,
			})

			events := decodeAuditEvents(t, output.Bytes())
			if len(events) != 1 {
				t.Fatalf("audit event count = %d, want 1", len(events))
			}
			event := events[0]
			if event.Outcome != tt.wantOutcome || event.Target != tt.wantTarget ||
				event.Tool != tt.wantTool {
				t.Fatalf("audit event = %#v", event)
			}
			logText := output.String()
			for _, private := range []string{
				"clearly-fake-secret",
				"example.invalid",
				"fixture-system",
				"bearerToken",
			} {
				if strings.Contains(logText, private) {
					t.Fatalf("audit output contains private value %q: %s", private, logText)
				}
			}
		})
	}
}

func TestAuditOutcomeUsesCanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := auditOutcome(ctx, true, nil, errors.New("private diagnostic")); got != auditCanceled {
		t.Fatalf("auditOutcome() = %q, want %q", got, auditCanceled)
	}
}

func TestAuditOutcomeCategoriesAreBounded(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err  error
		want string
	}{
		{errTargetNotConfigured, auditTargetNotConfigured},
		{kubernetes.ErrUnknownScope, auditScopeNotConfigured},
		{kubernetes.ErrSourceTimeout, auditSourceTimeout},
		{kubernetes.ErrSourceUnavailable, auditSourceUnavailable},
		{kubernetes.ErrSourceRejected, auditSourceRejected},
		{kubernetes.ErrCredentialUnavailable, auditCredentialUnavailable},
		{kubernetes.ErrInvalidSourceResponse, auditInvalidSourceResponse},
		{flux.ErrUnknownScope, auditScopeNotConfigured},
		{sourcehttp.ErrSourceTimeout, auditSourceTimeout},
		{sourcehttp.ErrSourceUnavailable, auditSourceUnavailable},
		{sourcehttp.ErrSourceRejected, auditSourceRejected},
		{sourcehttp.ErrCredentialUnavailable, auditCredentialUnavailable},
		{sourcehttp.ErrInvalidSourceResponse, auditInvalidSourceResponse},
		{observer.ErrResultTooLarge, auditResultTooLarge},
		{errObservationCanceled, auditCanceled},
		{errObservationFailed, auditInternalError},
	}
	for _, tt := range tests {
		result := &mcp.CallToolResult{}
		result.SetError(tt.err)
		if got := auditOutcome(context.Background(), true, result, nil); got != tt.want {
			t.Errorf("auditOutcome(%v) = %q, want %q", tt.err, got, tt.want)
		}
	}
}

type auditSource struct {
	healthErr error
}

type auditMonitoringSource struct{}

func (*auditMonitoringSource) Target() observer.Target {
	return (&fakeMonitoringSource{}).Target()
}

func (*auditMonitoringSource) ListActiveAlerts(
	context.Context,
	observer.ListActiveAlertsInput,
) (observer.ListActiveAlertsOutput, error) {
	return observer.ListActiveAlertsOutput{
		Target:     "monitoring-a",
		ObservedAt: time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC),
		Alerts: []observer.ActiveAlert{{
			Name:     "FixtureDegraded",
			Severity: observer.AlertSeverityWarning,
		}},
		Truncated: true,
	}, nil
}

func (*auditMonitoringSource) GetScrapeHealth(
	context.Context,
	observer.GetScrapeHealthInput,
) (observer.GetScrapeHealthOutput, error) {
	return observer.GetScrapeHealthOutput{
		Target:     "monitoring-a",
		ObservedAt: time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC),
		Scrapes: []observer.ScrapeHealth{
			{ID: "api", State: observer.ScrapeStateUp},
			{ID: "node", State: observer.ScrapeStateDown},
		},
		Partial: true,
	}, nil
}

type auditFluxSource struct{}

func (*auditFluxSource) Target() observer.Target {
	return (&fakeFluxSource{}).Target()
}

func (*auditFluxSource) ListUnhealthyReconciliations(
	context.Context,
	observer.ListUnhealthyReconciliationsInput,
) (observer.ListUnhealthyReconciliationsOutput, error) {
	return observer.ListUnhealthyReconciliationsOutput{
		Target:     "flux-a",
		ObservedAt: time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC),
		Reconciliations: []observer.UnhealthyReconciliation{{
			Scope:  "system",
			Kind:   observer.ReconciliationKindKustomization,
			Name:   "platform",
			State:  observer.ReadinessStateFalse,
			Reason: observer.ReconciliationReasonHealthCheckFailed,
		}},
		Truncated: true,
	}, nil
}

func (*auditSource) Target() observer.Target {
	return observer.Target{
		ID:   "cluster-a",
		Kind: observer.TargetKindKubernetes,
		Capabilities: []observer.Capability{
			observer.CapabilityKubernetesClusterHealth,
			observer.CapabilityKubernetesUnhealthyWorkloads,
		},
	}
}

func (s *auditSource) ClusterHealth(context.Context) (observer.ClusterHealthOutput, error) {
	if s.healthErr != nil {
		return observer.ClusterHealthOutput{}, s.healthErr
	}
	return observer.ClusterHealthOutput{
		Target:     "cluster-a",
		ObservedAt: time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC),
		Status:     observer.HealthStatusUnknown,
		Warnings:   []observer.HealthWarning{},
		Partial:    true,
	}, nil
}

func (*auditSource) ListUnhealthyWorkloads(
	_ context.Context,
	_ observer.ListUnhealthyWorkloadsInput,
) (observer.ListUnhealthyWorkloadsOutput, error) {
	return observer.ListUnhealthyWorkloadsOutput{
		Target:     "cluster-a",
		ObservedAt: time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC),
		Workloads: []observer.WorkloadHealth{
			{Scope: "system", Kind: observer.WorkloadKindDeployment, Name: "api"},
			{Scope: "system", Kind: observer.WorkloadKindStatefulSet, Name: "database"},
		},
		Truncated: true,
	}, nil
}

func callAuditTool(
	t *testing.T,
	session *mcp.ClientSession,
	name string,
	arguments map[string]any,
) {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      name,
		Arguments: arguments,
	})
	if err != nil || result.IsError {
		t.Fatalf("CallTool(%q) result = %#v, error = %v", name, result, err)
	}
}

func decodeAuditEvents(t *testing.T, encoded []byte) []auditEvent {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	var events []auditEvent
	for decoder.More() {
		var event auditEvent
		if err := decoder.Decode(&event); err != nil {
			t.Fatalf("decode audit event error = %v", err)
		}
		events = append(events, event)
	}
	return events
}
