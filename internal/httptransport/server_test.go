package httptransport

import (
	"context"
	"errors"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/neodymium6/cluster-observer-mcp/internal/mcpserver"
)

func TestStreamableHTTPExchange(t *testing.T) {
	t.Parallel()

	address := startTestServer(t)
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "fixture-hermes-client",
		Version: "0.0.0-test",
	}, nil)
	session, err := client.Connect(
		context.Background(),
		&mcp.StreamableClientTransport{
			Endpoint:             "http://" + address + "/mcp",
			HTTPClient:           loopbackTestClient(),
			MaxRetries:           -1,
			DisableStandaloneSSE: true,
		},
		nil,
	)
	if err != nil {
		t.Fatalf("Client.Connect() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	var names []string
	for _, tool := range result.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	want := []string{
		mcpserver.ToolListUnhealthyReconciliations,
		mcpserver.ToolGetClusterHealth,
		mcpserver.ToolGetScrapeHealth,
		mcpserver.ToolListActiveAlerts,
		mcpserver.ToolListTargets,
		mcpserver.ToolListUnhealthyWorkloads,
	}
	slices.Sort(want)
	if !slices.Equal(names, want) {
		t.Fatalf("ListTools() names = %v, want %v", names, want)
	}

	call, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      mcpserver.ToolListTargets,
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if call.IsError {
		t.Fatal("CallTool().IsError = true")
	}
}

func TestHTTPProtections(t *testing.T) {
	t.Parallel()

	address := startTestServer(t)
	endpoint := "http://" + address + "/mcp"

	t.Run("non-loopback Host", func(t *testing.T) {
		request := mcpRequest(t, http.MethodPost, endpoint, `{}`)
		request.Host = "observer.example.invalid"
		response, err := loopbackTestClient().Do(request)
		if err != nil {
			t.Fatalf("Do() error = %v", err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", response.StatusCode)
		}
	})

	t.Run("cross origin", func(t *testing.T) {
		request := mcpRequest(t, http.MethodPost, endpoint, `{}`)
		request.Header.Set("Origin", "https://caller.example.invalid")
		response, err := loopbackTestClient().Do(request)
		if err != nil {
			t.Fatalf("Do() error = %v", err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", response.StatusCode)
		}
	})

	t.Run("oversized body", func(t *testing.T) {
		request := mcpRequest(
			t,
			http.MethodPost,
			endpoint,
			strings.Repeat("x", maxMCPRequestBytes+1),
		)
		request.ContentLength = -1
		response, err := loopbackTestClient().Do(request)
		if err != nil {
			t.Fatalf("Do() error = %v", err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d, want 413", response.StatusCode)
		}
	})

	t.Run("invalid method", func(t *testing.T) {
		request := mcpRequest(t, http.MethodPut, endpoint, "")
		response, err := loopbackTestClient().Do(request)
		if err != nil {
			t.Fatalf("Do() error = %v", err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want 405", response.StatusCode)
		}
	})
}

func TestFixedHealthProbes(t *testing.T) {
	t.Parallel()

	address := startTestServer(t)
	port := portFromAddress(t, address)
	for _, kind := range []ProbeKind{ProbeLiveness, ProbeReadiness, ProbeStartup} {
		if err := Probe(context.Background(), port, kind); err != nil {
			t.Fatalf("Probe(%q) error = %v", kind, err)
		}
	}
	if err := Probe(context.Background(), port, ProbeKind("arbitrary")); !errors.Is(err, ErrProbeFailed) {
		t.Fatalf("Probe(arbitrary) error = %v, want ErrProbeFailed", err)
	}
}

func TestServeRejectsNonLoopbackListener(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	server, err := mcpserver.New("0.0.0-test", nil)
	if err != nil {
		t.Fatalf("mcpserver.New() error = %v", err)
	}
	if err := Serve(context.Background(), listener, server); !errors.Is(err, ErrNonLoopbackListener) {
		t.Fatalf("Serve() error = %v, want ErrNonLoopbackListener", err)
	}
}

func TestServeShutsDownOnCancellation(t *testing.T) {
	t.Parallel()

	listener, err := Listen(0)
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	server, err := mcpserver.New("0.0.0-test", nil)
	if err != nil {
		t.Fatalf("mcpserver.New() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- Serve(ctx, listener, server) }()
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("Serve() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve() did not shut down after cancellation")
	}
}

func startTestServer(t *testing.T) string {
	t.Helper()
	listener, err := Listen(0)
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	server, err := mcpserver.New("0.0.0-test", nil)
	if err != nil {
		_ = listener.Close()
		t.Fatalf("mcpserver.New() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- Serve(ctx, listener, server) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-result:
			if err != nil {
				t.Errorf("Serve() cleanup error = %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("Serve() cleanup timed out")
		}
	})
	return listener.Addr().String()
}

func loopbackTestClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout: 5 * time.Second,
	}
}

func mcpRequest(t *testing.T, method, endpoint, body string) *http.Request {
	t.Helper()
	request, err := http.NewRequest(method, endpoint, strings.NewReader(body))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	return request
}

func portFromAddress(t *testing.T, address string) int {
	t.Helper()
	_, portText, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatalf("net.SplitHostPort() error = %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("strconv.Atoi() error = %v", err)
	}
	return port
}
