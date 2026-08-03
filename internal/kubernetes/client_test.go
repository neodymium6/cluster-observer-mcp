package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/neodymium6/cluster-observer-mcp/internal/observer"
)

func TestObservationsUseOnlyFixedGETRequests(t *testing.T) {
	t.Parallel()

	fixtures := fixtureResponses(t)
	requested := make(map[string]int)
	var requestedMu sync.Mutex
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("request method = %s, want GET", r.Method)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if got := r.URL.Query().Get("limit"); got != resourceListLimit {
			t.Errorf("request limit = %q, want %q", got, resourceListLimit)
		}
		if len(r.URL.Query()) != 1 {
			t.Errorf("unexpected query: %v", r.URL.Query())
		}

		body, ok := fixtures[r.URL.Path]
		if !ok {
			t.Errorf("unexpected request path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		requestedMu.Lock()
		requested[r.URL.Path]++
		requestedMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)

	client := newFixtureClient(t, server)
	client.now = func() time.Time {
		return time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	}

	health, err := client.ClusterHealth(context.Background())
	if err != nil {
		t.Fatalf("ClusterHealth() error = %v", err)
	}
	if health.Status != observer.HealthStatusDegraded {
		t.Fatalf("ClusterHealth().Status = %q", health.Status)
	}
	if health.Nodes != (observer.NodeHealth{Total: 2, Ready: 1}) {
		t.Fatalf("ClusterHealth().Nodes = %#v", health.Nodes)
	}
	wantWorkloads := observer.WorkloadSummary{Total: 4, Ready: 2, Unhealthy: 2}
	if health.Workloads != wantWorkloads {
		t.Fatalf("ClusterHealth().Workloads = %#v, want %#v", health.Workloads, wantWorkloads)
	}
	if health.Partial {
		t.Fatal("ClusterHealth().Partial = true")
	}

	workloads, err := client.ListUnhealthyWorkloads(
		context.Background(),
		observer.ListUnhealthyWorkloadsInput{Target: "cluster-a", Limit: 1},
	)
	if err != nil {
		t.Fatalf("ListUnhealthyWorkloads() error = %v", err)
	}
	if len(workloads.Workloads) != 1 || workloads.Workloads[0].Name != "api" {
		t.Fatalf("ListUnhealthyWorkloads().Workloads = %#v", workloads.Workloads)
	}
	if !workloads.Truncated {
		t.Fatal("ListUnhealthyWorkloads().Truncated = false")
	}
	if got := workloads.Workloads[0].Reasons; len(got) != 1 ||
		got[0] != observer.WorkloadReasonProgressDeadline {
		t.Fatalf("workload reasons = %v", got)
	}

	encoded, err := json.Marshal(struct {
		Health    observer.ClusterHealthOutput
		Workloads observer.ListUnhealthyWorkloadsOutput
	}{health, workloads})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	for _, sensitive := range []string{
		"fixture-hardware",
		"must-not-be-returned",
		"private source diagnostic",
		"SourceSpecificPrivateReason",
	} {
		if strings.Contains(string(encoded), sensitive) {
			t.Fatalf("structured output contains source-only text %q", sensitive)
		}
	}

	requestedMu.Lock()
	defer requestedMu.Unlock()
	if len(requested) != len(fixtures) {
		t.Fatalf("requested paths = %v, want every fixture path", requested)
	}
}

func TestListUnhealthyWorkloadsRejectsUnknownScopeBeforeRequest(t *testing.T) {
	t.Parallel()

	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests++
	}))
	t.Cleanup(server.Close)
	client := newFixtureClient(t, server)

	_, err := client.ListUnhealthyWorkloads(
		context.Background(),
		observer.ListUnhealthyWorkloadsInput{
			Target: "cluster-a",
			Scope:  "unknown",
		},
	)
	if !errors.Is(err, ErrUnknownScope) {
		t.Fatalf("ListUnhealthyWorkloads() error = %v, want ErrUnknownScope", err)
	}
	if requests != 0 {
		t.Fatalf("source request count = %d, want 0", requests)
	}
}

func TestSourceErrorsDoNotReturnBodyOrEndpoint(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("private response diagnostic"))
	}))
	t.Cleanup(server.Close)
	client := newFixtureClient(t, server)

	_, err := client.ClusterHealth(context.Background())
	if !errors.Is(err, ErrSourceRejected) {
		t.Fatalf("ClusterHealth() error = %v, want ErrSourceRejected", err)
	}
	if strings.Contains(err.Error(), "private response diagnostic") ||
		strings.Contains(err.Error(), server.URL) {
		t.Fatalf("source error disclosed private data: %v", err)
	}
}

func TestSourceCredentialIsReadForEveryRequest(t *testing.T) {
	t.Parallel()

	var authorizations []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorizations = append(authorizations, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"metadata":{},"items":[]}`))
	}))
	t.Cleanup(server.Close)

	tokens := []string{"clearly-fake-token-one", "clearly-fake-token-two"}
	nextToken := 0
	client, err := NewClient(Config{
		TargetID:   "cluster-a",
		BaseURL:    server.URL,
		Scopes:     fixtureScopes(),
		HTTPClient: server.Client(),
		Credential: CredentialSourceFunc(func(context.Context) (string, error) {
			token := tokens[nextToken]
			nextToken++
			return token, nil
		}),
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	for range tokens {
		if _, err := client.get(context.Background(), "/api/v1/nodes"); err != nil {
			t.Fatalf("get() error = %v", err)
		}
	}
	want := []string{"Bearer " + tokens[0], "Bearer " + tokens[1]}
	if !slices.Equal(authorizations, want) {
		t.Fatalf("Authorization headers = %q, want rotated fake values", authorizations)
	}
}

func TestSourceCredentialErrorsAreRedacted(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("source request must not be sent without a valid credential")
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(Config{
		TargetID:   "cluster-a",
		BaseURL:    server.URL,
		Scopes:     fixtureScopes(),
		HTTPClient: server.Client(),
		Credential: CredentialSourceFunc(func(context.Context) (string, error) {
			return "", errors.New("private credential path and diagnostic")
		}),
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	_, err = client.get(context.Background(), "/api/v1/nodes")
	if !errors.Is(err, ErrCredentialUnavailable) {
		t.Fatalf("get() error = %v, want ErrCredentialUnavailable", err)
	}
	if strings.Contains(err.Error(), "private credential") {
		t.Fatalf("credential error disclosed private detail: %v", err)
	}
}

func TestSourceResponseLimitsAndTimeout(t *testing.T) {
	t.Parallel()

	t.Run("oversized", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(strings.Repeat("x", maxSourceResponseBytes+1)))
		}))
		t.Cleanup(server.Close)
		client := newFixtureClient(t, server)

		_, err := client.ClusterHealth(context.Background())
		if !errors.Is(err, ErrInvalidSourceResponse) {
			t.Fatalf("ClusterHealth() error = %v, want ErrInvalidSourceResponse", err)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		}))
		t.Cleanup(server.Close)
		client := newFixtureClient(t, server)
		client.requestTimeout = time.Millisecond

		_, err := client.ClusterHealth(context.Background())
		if !errors.Is(err, ErrSourceTimeout) {
			t.Fatalf("ClusterHealth() error = %v, want ErrSourceTimeout", err)
		}
	})
}

func TestSourceConcurrencyIsBounded(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{}, maxConcurrentRequests+1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseAll := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseAll)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		entered <- struct{}{}
		<-release
		_, _ = w.Write([]byte(`{"metadata":{},"items":[]}`))
	}))
	t.Cleanup(server.Close)
	client := newFixtureClient(t, server)
	client.requestTimeout = 2 * time.Second

	errors := make(chan error, maxConcurrentRequests+1)
	for range maxConcurrentRequests + 1 {
		go func() {
			_, err := client.get(context.Background(), "/api/v1/nodes")
			errors <- err
		}()
	}

	for range maxConcurrentRequests {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for bounded requests")
		}
	}
	select {
	case <-entered:
		t.Fatal("more than four source requests ran concurrently")
	case <-time.After(50 * time.Millisecond):
	}

	releaseAll()
	for range maxConcurrentRequests + 1 {
		if err := <-errors; err != nil {
			t.Fatalf("source request error = %v", err)
		}
	}
}

func TestNewClientRejectsUnsafeConfiguration(t *testing.T) {
	t.Parallel()

	tests := []Config{
		{TargetID: "cluster-a", BaseURL: "http://example.invalid", Scopes: fixtureScopes()},
		{TargetID: "cluster-a", BaseURL: "https://example.invalid/api", Scopes: fixtureScopes()},
		{TargetID: "cluster-a", BaseURL: "https://example.invalid?path=value", Scopes: fixtureScopes()},
		{
			TargetID: "cluster-a",
			BaseURL:  "https://example.invalid",
			Scopes:   []Scope{{ID: "scope-a", Namespace: "invalid/namespace"}},
		},
	}

	for i, config := range tests {
		if _, err := NewClient(config); err == nil {
			t.Fatalf("NewClient(test %d) unexpectedly succeeded", i)
		}
	}
}

func newFixtureClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	client, err := NewClient(Config{
		TargetID:   "cluster-a",
		BaseURL:    server.URL,
		Scopes:     fixtureScopes(),
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return client
}

func fixtureScopes() []Scope {
	return []Scope{{ID: "system", Namespace: "fixture-system"}}
}

func fixtureResponses(t *testing.T) map[string][]byte {
	t.Helper()
	files := map[string]string{
		"/api/v1/nodes": "nodes.json",
		"/apis/apps/v1/namespaces/fixture-system/deployments":  "deployments.json",
		"/apis/apps/v1/namespaces/fixture-system/statefulsets": "statefulsets.json",
		"/apis/apps/v1/namespaces/fixture-system/daemonsets":   "daemonsets.json",
	}
	responses := make(map[string][]byte, len(files))
	for path, filename := range files {
		body, err := os.ReadFile("testdata/" + filename)
		if err != nil {
			t.Fatalf("read fixture %s: %v", filename, err)
		}
		responses[path] = body
	}
	return responses
}

func ExampleClient() {
	client, err := NewClient(Config{
		TargetID: "cluster-a",
		BaseURL:  "https://kubernetes.example.invalid",
		Scopes: []Scope{
			{ID: "system", Namespace: "fixture-system"},
		},
	})
	fmt.Println(client != nil, err)
	// Output: true <nil>
}
