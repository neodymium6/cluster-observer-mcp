// Package mcpserver exposes the bounded observation contracts as MCP tools.
package mcpserver

import (
	"context"
	"errors"
	"io"
	"slices"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/neodymium6/cluster-observer-mcp/internal/flux"
	"github.com/neodymium6/cluster-observer-mcp/internal/kubernetes"
	"github.com/neodymium6/cluster-observer-mcp/internal/observer"
	"github.com/neodymium6/cluster-observer-mcp/internal/sourcehttp"
)

const (
	// ToolListTargets is the endpoint-free target discovery tool.
	ToolListTargets = "observer_list_targets"
	// ToolGetClusterHealth is the bounded Kubernetes health summary tool.
	ToolGetClusterHealth = "kubernetes_get_cluster_health"
	// ToolListUnhealthyWorkloads is the bounded unhealthy controller tool.
	ToolListUnhealthyWorkloads = "kubernetes_list_unhealthy_workloads"
	// ToolListActiveAlerts is the bounded Alertmanager active-alert tool.
	ToolListActiveAlerts = "monitoring_list_active_alerts"
	// ToolGetScrapeHealth is the fixed Prometheus up-series tool.
	ToolGetScrapeHealth = "monitoring_get_scrape_health"
	// ToolListUnhealthyReconciliations is the bounded Flux readiness tool.
	ToolListUnhealthyReconciliations = "flux_list_unhealthy_reconciliations"
)

var (
	errTargetNotConfigured = errors.New("target is not configured")
	errObservationFailed   = errors.New("observation failed")
	errObservationCanceled = errors.New("observation canceled")
)

// Options configures non-protocol server behavior.
type Options struct {
	AuditWriter io.Writer
	Now         func() time.Time
}

// KubernetesSource is the narrow source surface required by the MCP tools.
type KubernetesSource interface {
	Target() observer.Target
	ClusterHealth(context.Context) (observer.ClusterHealthOutput, error)
	ListUnhealthyWorkloads(
		context.Context,
		observer.ListUnhealthyWorkloadsInput,
	) (observer.ListUnhealthyWorkloadsOutput, error)
}

// MonitoringSource is the narrow source surface required by monitoring tools.
type MonitoringSource interface {
	Target() observer.Target
	ListActiveAlerts(
		context.Context,
		observer.ListActiveAlertsInput,
	) (observer.ListActiveAlertsOutput, error)
	GetScrapeHealth(
		context.Context,
		observer.GetScrapeHealthInput,
	) (observer.GetScrapeHealthOutput, error)
}

// FluxSource is the narrow source surface required by Flux tools.
type FluxSource interface {
	Target() observer.Target
	ListUnhealthyReconciliations(
		context.Context,
		observer.ListUnhealthyReconciliationsInput,
	) (observer.ListUnhealthyReconciliationsOutput, error)
}

// SourceSet contains every configured purpose-built source family.
type SourceSet struct {
	Kubernetes []KubernetesSource
	Monitoring []MonitoringSource
	Flux       []FluxSource
}

// New constructs a transport-independent MCP server.
func New(version string, sources []KubernetesSource) (*mcp.Server, error) {
	return NewWithOptions(version, sources, Options{})
}

// NewWithOptions constructs a transport-independent MCP server with optional
// structured audit output.
func NewWithOptions(
	version string,
	sources []KubernetesSource,
	options Options,
) (*mcp.Server, error) {
	return NewWithSourceSet(version, SourceSet{Kubernetes: sources}, options)
}

// NewWithSourceSet constructs a transport-independent server containing all
// configured purpose-built source families.
func NewWithSourceSet(
	version string,
	sources SourceSet,
	options Options,
) (*mcp.Server, error) {
	targets := make([]observer.Target, 0,
		len(sources.Kubernetes)+len(sources.Monitoring)+len(sources.Flux))
	clusterHealthByID := make(map[string]KubernetesSource, len(sources.Kubernetes))
	unhealthyWorkloadsByID := make(map[string]KubernetesSource, len(sources.Kubernetes))
	for _, source := range sources.Kubernetes {
		if source == nil {
			return nil, errors.New("Kubernetes source must not be nil")
		}
		target := source.Target()
		if target.Kind != observer.TargetKindKubernetes {
			return nil, errors.New("Kubernetes source has the wrong target kind")
		}
		targets = append(targets, target)
		if slices.Contains(target.Capabilities, observer.CapabilityKubernetesClusterHealth) {
			clusterHealthByID[target.ID] = source
		}
		if slices.Contains(target.Capabilities, observer.CapabilityKubernetesUnhealthyWorkloads) {
			unhealthyWorkloadsByID[target.ID] = source
		}
	}
	activeAlertsByID := make(map[string]MonitoringSource, len(sources.Monitoring))
	scrapeHealthByID := make(map[string]MonitoringSource, len(sources.Monitoring))
	for _, source := range sources.Monitoring {
		if source == nil {
			return nil, errors.New("monitoring source must not be nil")
		}
		target := source.Target()
		if target.Kind != observer.TargetKindMonitoring {
			return nil, errors.New("monitoring source has the wrong target kind")
		}
		targets = append(targets, target)
		if slices.Contains(target.Capabilities, observer.CapabilityMonitoringActiveAlerts) {
			activeAlertsByID[target.ID] = source
		}
		if slices.Contains(target.Capabilities, observer.CapabilityMonitoringScrapeHealth) {
			scrapeHealthByID[target.ID] = source
		}
	}
	fluxByID := make(map[string]FluxSource, len(sources.Flux))
	for _, source := range sources.Flux {
		if source == nil {
			return nil, errors.New("Flux source must not be nil")
		}
		target := source.Target()
		if target.Kind != observer.TargetKindFlux {
			return nil, errors.New("Flux source has the wrong target kind")
		}
		targets = append(targets, target)
		fluxByID[target.ID] = source
	}

	catalog, err := observer.NewCatalog(targets)
	if err != nil {
		return nil, err
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "cluster-observer-mcp",
		Title:   "Cluster Observer MCP",
		Version: version,
	}, nil)
	if audit := newAuditLogger(options.AuditWriter, options.Now); audit != nil {
		server.AddReceivingMiddleware(audit.middleware())
	}

	mcp.AddTool(server, &mcp.Tool{
		Name:        ToolListTargets,
		Title:       "List observer targets",
		Description: "List opaque configured target identifiers and capabilities without endpoints.",
		InputSchema: emptyInputSchema(),
		Annotations: readOnlyAnnotations("List observer targets"),
	}, func(
		_ context.Context,
		_ *mcp.CallToolRequest,
		_ observer.ListTargetsInput,
	) (*mcp.CallToolResult, observer.ListTargetsOutput, error) {
		output := catalog.List()
		if err := observer.CheckResultSize(output); err != nil {
			return nil, observer.ListTargetsOutput{}, safeToolError(err)
		}
		return summaryResult("Returned configured observer targets."), output, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        ToolGetClusterHealth,
		Title:       "Get Kubernetes cluster health",
		Description: "Return bounded node and workload readiness totals for one configured target.",
		InputSchema: clusterHealthInputSchema(),
		Annotations: readOnlyAnnotations("Get Kubernetes cluster health"),
	}, func(
		ctx context.Context,
		_ *mcp.CallToolRequest,
		input observer.GetClusterHealthInput,
	) (*mcp.CallToolResult, observer.ClusterHealthOutput, error) {
		if err := input.Validate(); err != nil {
			return nil, observer.ClusterHealthOutput{}, safeToolError(err)
		}
		source, ok := clusterHealthByID[input.Target]
		if !ok {
			return nil, observer.ClusterHealthOutput{}, errTargetNotConfigured
		}
		output, err := source.ClusterHealth(ctx)
		if err != nil {
			return nil, observer.ClusterHealthOutput{}, safeToolError(err)
		}
		if err := observer.CheckResultSize(output); err != nil {
			return nil, observer.ClusterHealthOutput{}, safeToolError(err)
		}
		return summaryResult("Returned bounded Kubernetes cluster health."), output, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        ToolListUnhealthyWorkloads,
		Title:       "List unhealthy Kubernetes workloads",
		Description: "List at most 50 unhealthy controllers from configured scopes.",
		InputSchema: unhealthyWorkloadsInputSchema(),
		Annotations: readOnlyAnnotations("List unhealthy Kubernetes workloads"),
	}, func(
		ctx context.Context,
		_ *mcp.CallToolRequest,
		input observer.ListUnhealthyWorkloadsInput,
	) (*mcp.CallToolResult, observer.ListUnhealthyWorkloadsOutput, error) {
		normalized, err := input.Normalize()
		if err != nil {
			return nil, observer.ListUnhealthyWorkloadsOutput{}, safeToolError(err)
		}
		source, ok := unhealthyWorkloadsByID[normalized.Target]
		if !ok {
			return nil, observer.ListUnhealthyWorkloadsOutput{}, errTargetNotConfigured
		}
		output, err := source.ListUnhealthyWorkloads(ctx, normalized)
		if err != nil {
			return nil, observer.ListUnhealthyWorkloadsOutput{}, safeToolError(err)
		}
		if err := observer.CheckResultSize(output); err != nil {
			return nil, observer.ListUnhealthyWorkloadsOutput{}, safeToolError(err)
		}
		return summaryResult("Returned bounded unhealthy Kubernetes workloads."), output, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        ToolListActiveAlerts,
		Title:       "List active monitoring alerts",
		Description: "List at most 50 active, non-silenced, non-inhibited alerts without raw labels or annotations.",
		InputSchema: boundedTargetListInputSchema("Maximum number of alerts to return."),
		Annotations: readOnlyAnnotations("List active monitoring alerts"),
	}, func(
		ctx context.Context,
		_ *mcp.CallToolRequest,
		input observer.ListActiveAlertsInput,
	) (*mcp.CallToolResult, observer.ListActiveAlertsOutput, error) {
		normalized, err := input.Normalize()
		if err != nil {
			return nil, observer.ListActiveAlertsOutput{}, safeToolError(err)
		}
		source, ok := activeAlertsByID[normalized.Target]
		if !ok {
			return nil, observer.ListActiveAlertsOutput{}, errTargetNotConfigured
		}
		output, err := source.ListActiveAlerts(ctx, normalized)
		if err != nil {
			return nil, observer.ListActiveAlertsOutput{}, safeToolError(err)
		}
		if err := observer.CheckResultSize(output); err != nil {
			return nil, observer.ListActiveAlertsOutput{}, safeToolError(err)
		}
		return summaryResult("Returned bounded active monitoring alerts."), output, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        ToolGetScrapeHealth,
		Title:       "Get monitoring scrape health",
		Description: "Return normalized up, down, or missing state for configured opaque scrape identities.",
		InputSchema: targetInputSchema(),
		Annotations: readOnlyAnnotations("Get monitoring scrape health"),
	}, func(
		ctx context.Context,
		_ *mcp.CallToolRequest,
		input observer.GetScrapeHealthInput,
	) (*mcp.CallToolResult, observer.GetScrapeHealthOutput, error) {
		if err := input.Validate(); err != nil {
			return nil, observer.GetScrapeHealthOutput{}, safeToolError(err)
		}
		source, ok := scrapeHealthByID[input.Target]
		if !ok {
			return nil, observer.GetScrapeHealthOutput{}, errTargetNotConfigured
		}
		output, err := source.GetScrapeHealth(ctx, input)
		if err != nil {
			return nil, observer.GetScrapeHealthOutput{}, safeToolError(err)
		}
		if err := observer.CheckResultSize(output); err != nil {
			return nil, observer.GetScrapeHealthOutput{}, safeToolError(err)
		}
		return summaryResult("Returned bounded monitoring scrape health."), output, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        ToolListUnhealthyReconciliations,
		Title:       "List unhealthy Flux reconciliations",
		Description: "List at most 50 non-ready Flux resources from configured scopes without raw objects or condition messages.",
		InputSchema: fluxReconciliationsInputSchema(),
		Annotations: readOnlyAnnotations("List unhealthy Flux reconciliations"),
	}, func(
		ctx context.Context,
		_ *mcp.CallToolRequest,
		input observer.ListUnhealthyReconciliationsInput,
	) (*mcp.CallToolResult, observer.ListUnhealthyReconciliationsOutput, error) {
		normalized, err := input.Normalize()
		if err != nil {
			return nil, observer.ListUnhealthyReconciliationsOutput{}, safeToolError(err)
		}
		source, ok := fluxByID[normalized.Target]
		if !ok {
			return nil, observer.ListUnhealthyReconciliationsOutput{}, errTargetNotConfigured
		}
		output, err := source.ListUnhealthyReconciliations(ctx, normalized)
		if err != nil {
			return nil, observer.ListUnhealthyReconciliationsOutput{}, safeToolError(err)
		}
		if err := observer.CheckResultSize(output); err != nil {
			return nil, observer.ListUnhealthyReconciliationsOutput{}, safeToolError(err)
		}
		return summaryResult("Returned bounded unhealthy Flux reconciliations."), output, nil
	})

	return server, nil
}

func summaryResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

func readOnlyAnnotations(title string) *mcp.ToolAnnotations {
	falseValue := false
	return &mcp.ToolAnnotations{
		Title:           title,
		ReadOnlyHint:    true,
		DestructiveHint: &falseValue,
		IdempotentHint:  true,
		OpenWorldHint:   &falseValue,
	}
}

func safeToolError(err error) error {
	var validationError *observer.ValidationError
	if errors.As(err, &validationError) {
		return validationError
	}

	switch {
	case errors.Is(err, context.Canceled):
		return errObservationCanceled
	case errors.Is(err, observer.ErrResultTooLarge):
		return observer.ErrResultTooLarge
	case errors.Is(err, kubernetes.ErrSourceTimeout):
		return kubernetes.ErrSourceTimeout
	case errors.Is(err, kubernetes.ErrSourceUnavailable):
		return kubernetes.ErrSourceUnavailable
	case errors.Is(err, kubernetes.ErrSourceRejected):
		return kubernetes.ErrSourceRejected
	case errors.Is(err, kubernetes.ErrCredentialUnavailable):
		return kubernetes.ErrCredentialUnavailable
	case errors.Is(err, kubernetes.ErrInvalidSourceResponse):
		return kubernetes.ErrInvalidSourceResponse
	case errors.Is(err, kubernetes.ErrUnknownScope):
		return kubernetes.ErrUnknownScope
	case errors.Is(err, flux.ErrUnknownScope):
		return flux.ErrUnknownScope
	case errors.Is(err, sourcehttp.ErrSourceTimeout):
		return sourcehttp.ErrSourceTimeout
	case errors.Is(err, sourcehttp.ErrSourceUnavailable):
		return sourcehttp.ErrSourceUnavailable
	case errors.Is(err, sourcehttp.ErrSourceRejected):
		return sourcehttp.ErrSourceRejected
	case errors.Is(err, sourcehttp.ErrCredentialUnavailable):
		return sourcehttp.ErrCredentialUnavailable
	case errors.Is(err, sourcehttp.ErrInvalidSourceResponse):
		return sourcehttp.ErrInvalidSourceResponse
	default:
		return errObservationFailed
	}
}

func emptyInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"properties":           map[string]any{},
		"additionalProperties": false,
	}
}

func clusterHealthInputSchema() map[string]any {
	return targetInputSchema()
}

func targetInputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"target": identifierSchema("Opaque configured target identifier."),
		},
		"required":             []string{"target"},
		"additionalProperties": false,
	}
}

func boundedTargetListInputSchema(limitDescription string) map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"target": identifierSchema("Opaque configured target identifier."),
			"limit": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"maximum":     observer.MaxListLimit,
				"default":     observer.DefaultListLimit,
				"description": limitDescription,
			},
		},
		"required":             []string{"target"},
		"additionalProperties": false,
	}
}

func fluxReconciliationsInputSchema() map[string]any {
	schema := unhealthyWorkloadsInputSchema()
	properties := schema["properties"].(map[string]any)
	properties["limit"].(map[string]any)["description"] =
		"Maximum number of reconciliations to return."
	return schema
}

func unhealthyWorkloadsInputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"target": identifierSchema("Opaque configured target identifier."),
			"scope":  identifierSchema("Optional configured scope identifier."),
			"limit": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"maximum":     observer.MaxListLimit,
				"default":     observer.DefaultListLimit,
				"description": "Maximum number of workloads to return.",
			},
		},
		"required":             []string{"target"},
		"additionalProperties": false,
	}
}

func identifierSchema(description string) map[string]any {
	return map[string]any{
		"type":        "string",
		"pattern":     `^[a-z](?:[a-z0-9-]{0,30}[a-z0-9])?$`,
		"maxLength":   32,
		"description": description,
	}
}
