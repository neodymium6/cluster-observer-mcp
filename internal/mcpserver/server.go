// Package mcpserver exposes the bounded observation contracts as MCP tools.
package mcpserver

import (
	"context"
	"errors"

	"example.com/cluster-observer-mcp/internal/kubernetes"
	"example.com/cluster-observer-mcp/internal/observer"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// ToolListTargets is the endpoint-free target discovery tool.
	ToolListTargets = "observer_list_targets"
	// ToolGetClusterHealth is the bounded Kubernetes health summary tool.
	ToolGetClusterHealth = "kubernetes_get_cluster_health"
	// ToolListUnhealthyWorkloads is the bounded unhealthy controller tool.
	ToolListUnhealthyWorkloads = "kubernetes_list_unhealthy_workloads"
)

var (
	errTargetNotConfigured = errors.New("target is not configured")
	errObservationFailed   = errors.New("observation failed")
)

// KubernetesSource is the narrow source surface required by the MCP tools.
type KubernetesSource interface {
	Target() observer.Target
	ClusterHealth(context.Context) (observer.ClusterHealthOutput, error)
	ListUnhealthyWorkloads(
		context.Context,
		observer.ListUnhealthyWorkloadsInput,
	) (observer.ListUnhealthyWorkloadsOutput, error)
}

// New constructs a transport-independent MCP server.
func New(version string, sources []KubernetesSource) (*mcp.Server, error) {
	targets := make([]observer.Target, 0, len(sources))
	sourceByID := make(map[string]KubernetesSource, len(sources))
	for _, source := range sources {
		if source == nil {
			return nil, errors.New("Kubernetes source must not be nil")
		}
		target := source.Target()
		targets = append(targets, target)
		sourceByID[target.ID] = source
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
		source, ok := sourceByID[input.Target]
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
		source, ok := sourceByID[normalized.Target]
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
	case errors.Is(err, observer.ErrResultTooLarge):
		return observer.ErrResultTooLarge
	case errors.Is(err, kubernetes.ErrSourceTimeout):
		return kubernetes.ErrSourceTimeout
	case errors.Is(err, kubernetes.ErrSourceUnavailable):
		return kubernetes.ErrSourceUnavailable
	case errors.Is(err, kubernetes.ErrSourceRejected):
		return kubernetes.ErrSourceRejected
	case errors.Is(err, kubernetes.ErrInvalidSourceResponse):
		return kubernetes.ErrInvalidSourceResponse
	case errors.Is(err, kubernetes.ErrUnknownScope):
		return kubernetes.ErrUnknownScope
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
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"target": identifierSchema("Opaque configured target identifier."),
		},
		"required":             []string{"target"},
		"additionalProperties": false,
	}
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
