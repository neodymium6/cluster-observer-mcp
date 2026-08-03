package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/neodymium6/cluster-observer-mcp/internal/observer"
)

func TestToolsExposeBoundedSchemasAndStructuredContent(t *testing.T) {
	t.Parallel()

	source := &fakeSource{}
	server, err := New("test", []KubernetesSource{source})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	session := connectTestClient(t, server)

	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(listed.Tools) != 3 {
		t.Fatalf("ListTools() count = %d, want 3", len(listed.Tools))
	}
	for _, tool := range listed.Tools {
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint ||
			tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint ||
			!tool.Annotations.IdempotentHint ||
			tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint {
			t.Fatalf("tool %q annotations = %#v", tool.Name, tool.Annotations)
		}
		schema, ok := tool.InputSchema.(map[string]any)
		if !ok {
			t.Fatalf("tool %q input schema type = %T", tool.Name, tool.InputSchema)
		}
		if additional, ok := schema["additionalProperties"].(bool); !ok || additional {
			t.Fatalf("tool %q allows additional properties: %#v", tool.Name, schema)
		}
		if tool.OutputSchema == nil {
			t.Fatalf("tool %q has no output schema", tool.Name)
		}
	}

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      ToolListTargets,
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("CallTool() tool error = %v", result.Content)
	}
	var output observer.ListTargetsOutput
	decodeStructuredContent(t, result.StructuredContent, &output)
	if len(output.Targets) != 1 || output.Targets[0].ID != "cluster-a" {
		t.Fatalf("CallTool() structured output = %#v", output)
	}

	healthResult, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      ToolGetClusterHealth,
		Arguments: map[string]any{"target": "cluster-a"},
	})
	if err != nil || healthResult.IsError {
		t.Fatalf("cluster health result = %#v, error = %v", healthResult, err)
	}
	var health observer.ClusterHealthOutput
	decodeStructuredContent(t, healthResult.StructuredContent, &health)
	if health.Target != "cluster-a" || health.Status != observer.HealthStatusHealthy {
		t.Fatalf("cluster health output = %#v", health)
	}

	workloadResult, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      ToolListUnhealthyWorkloads,
		Arguments: map[string]any{"target": "cluster-a"},
	})
	if err != nil || workloadResult.IsError {
		t.Fatalf("workload result = %#v, error = %v", workloadResult, err)
	}
	var workloads observer.ListUnhealthyWorkloadsOutput
	decodeStructuredContent(t, workloadResult.StructuredContent, &workloads)
	if workloads.Target != "cluster-a" || source.lastWorkloadInput.Limit != observer.DefaultListLimit {
		t.Fatalf("workload output = %#v, input = %#v", workloads, source.lastWorkloadInput)
	}
}

func TestToolInputsFailClosedBeforeSourceCalls(t *testing.T) {
	t.Parallel()

	source := &fakeSource{}
	server, err := New("test", []KubernetesSource{source})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	session := connectTestClient(t, server)

	tests := []struct {
		name      string
		tool      string
		arguments map[string]any
	}{
		{
			name:      "unknown property",
			tool:      ToolGetClusterHealth,
			arguments: map[string]any{"target": "cluster-a", "path": "/api/v1/secrets"},
		},
		{
			name:      "URL target",
			tool:      ToolGetClusterHealth,
			arguments: map[string]any{"target": "https://example.invalid"},
		},
		{
			name:      "excessive limit",
			tool:      ToolListUnhealthyWorkloads,
			arguments: map[string]any{"target": "cluster-a", "limit": 51},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
				Name:      tt.tool,
				Arguments: tt.arguments,
			})
			if err != nil {
				t.Fatalf("CallTool() protocol error = %v", err)
			}
			if !result.IsError {
				t.Fatal("CallTool().IsError = false")
			}
		})
	}

	if source.healthCalls != 0 || source.workloadCalls != 0 {
		t.Fatalf("source calls = health %d, workloads %d", source.healthCalls, source.workloadCalls)
	}
}

func TestToolErrorsRedactUnexpectedSourceDetails(t *testing.T) {
	t.Parallel()

	source := &fakeSource{healthErr: errors.New("private endpoint diagnostic")}
	server, err := New("test", []KubernetesSource{source})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	session := connectTestClient(t, server)

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      ToolGetClusterHealth,
		Arguments: map[string]any{"target": "cluster-a"},
	})
	if err != nil {
		t.Fatalf("CallTool() protocol error = %v", err)
	}
	if !result.IsError {
		t.Fatal("CallTool().IsError = false")
	}
	text := result.Content[0].(*mcp.TextContent).Text
	if text != errObservationFailed.Error() || strings.Contains(text, "private") {
		t.Fatalf("tool error disclosed source detail: %q", text)
	}
}

func TestStdioTransport(t *testing.T) {
	if os.Getenv("CLUSTER_OBSERVER_STDIO_HELPER") == "1" {
		server, err := New("test", nil)
		if err != nil {
			os.Exit(2)
		}
		if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
			os.Exit(3)
		}
		os.Exit(0)
	}

	command := exec.Command(os.Args[0], "-test.run=^TestStdioTransport$")
	command.Env = append(os.Environ(), "CLUSTER_OBSERVER_STDIO_HELPER=1")
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	session, err := client.Connect(context.Background(), &mcp.CommandTransport{Command: command}, nil)
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      ToolListTargets,
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	var output observer.ListTargetsOutput
	decodeStructuredContent(t, result.StructuredContent, &output)
	if len(output.Targets) != 0 {
		t.Fatalf("stdio target list = %#v, want empty", output.Targets)
	}
}

type fakeSource struct {
	healthCalls       int
	workloadCalls     int
	healthErr         error
	lastWorkloadInput observer.ListUnhealthyWorkloadsInput
}

func (*fakeSource) Target() observer.Target {
	return observer.Target{
		ID:   "cluster-a",
		Kind: observer.TargetKindKubernetes,
		Capabilities: []observer.Capability{
			observer.CapabilityKubernetesClusterHealth,
			observer.CapabilityKubernetesUnhealthyWorkloads,
		},
	}
}

func (s *fakeSource) ClusterHealth(context.Context) (observer.ClusterHealthOutput, error) {
	s.healthCalls++
	if s.healthErr != nil {
		return observer.ClusterHealthOutput{}, s.healthErr
	}
	return observer.ClusterHealthOutput{
		Target:     "cluster-a",
		ObservedAt: time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC),
		Status:     observer.HealthStatusHealthy,
		Warnings:   []observer.HealthWarning{},
	}, nil
}

func (s *fakeSource) ListUnhealthyWorkloads(
	_ context.Context,
	input observer.ListUnhealthyWorkloadsInput,
) (observer.ListUnhealthyWorkloadsOutput, error) {
	s.workloadCalls++
	s.lastWorkloadInput = input
	return observer.ListUnhealthyWorkloadsOutput{
		Target:     "cluster-a",
		ObservedAt: time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC),
		Workloads:  []observer.WorkloadHealth{},
	}, nil
}

func connectTestClient(t *testing.T, server *mcp.Server) *mcp.ClientSession {
	t.Helper()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatalf("server.Connect() error = %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	clientSession, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect() error = %v", err)
	}
	t.Cleanup(func() {
		_ = clientSession.Close()
		serverSession.Wait()
	})
	return clientSession
}

func decodeStructuredContent(t *testing.T, value any, output any) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if err := json.Unmarshal(encoded, output); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
}
