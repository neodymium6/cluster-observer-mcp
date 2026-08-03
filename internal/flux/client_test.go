package flux

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/neodymium6/cluster-observer-mcp/internal/observer"
)

func TestObservationUsesFixedGETPathsAndRedactsConditions(t *testing.T) {
	t.Parallel()

	requested := make(map[string]int)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.RawQuery != "limit=500" {
			t.Errorf("request = %s %s", r.Method, r.URL.String())
		}
		requested[r.URL.Path]++
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/apis/kustomize.toolkit.fluxcd.io/v1/namespaces/fixture-system/kustomizations":
			_, _ = w.Write([]byte(`{
  "metadata": {},
  "items": [
    {
      "metadata": {"name": "platform"},
      "spec": {"path": "./private/path"},
      "status": {"conditions": [{
        "type": "Ready",
        "status": "False",
        "reason": "HealthCheckFailed",
        "message": "private condition diagnostic",
        "lastTransitionTime": "2026-08-03T11:50:00Z"
      }]}
    },
    {
      "metadata": {"name": "ready"},
      "status": {"conditions": [{"type": "Ready", "status": "True"}]}
    }
  ]
}`))
		case "/apis/helm.toolkit.fluxcd.io/v2/namespaces/fixture-system/helmreleases":
			_, _ = w.Write([]byte(`{
  "metadata": {"continue": "private-pagination-token"},
  "items": [{
    "metadata": {"name": "database"},
    "status": {"conditions": [{
      "type": "Ready",
      "status": "False",
      "reason": "InstallFailed",
      "message": "private Helm diagnostic"
    }]}
  }]
}`))
		case "/apis/source.toolkit.fluxcd.io/v1/namespaces/fixture-system/gitrepositories":
			_, _ = w.Write([]byte(`{
  "metadata": {},
  "items": [{
    "metadata": {"name": "source"},
    "status": {"conditions": []}
  }]
}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	client := newFixtureClient(t, server)
	client.now = func() time.Time {
		return time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	}
	result, err := client.ListUnhealthyReconciliations(
		context.Background(),
		observer.ListUnhealthyReconciliationsInput{Target: "flux-a", Scope: "system", Limit: 3},
	)
	if err != nil {
		t.Fatalf("ListUnhealthyReconciliations() error = %v", err)
	}
	if len(result.Reconciliations) != 3 || !result.Truncated {
		t.Fatalf("ListUnhealthyReconciliations() = %#v", result)
	}
	if result.Reconciliations[0].Kind != observer.ReconciliationKindGitRepository ||
		result.Reconciliations[0].Reason != observer.ReconciliationReasonUnknown ||
		result.Reconciliations[1].Kind != observer.ReconciliationKindHelmRelease ||
		result.Reconciliations[1].Reason != observer.ReconciliationReasonUnknown ||
		result.Reconciliations[2].Kind != observer.ReconciliationKindKustomization ||
		result.Reconciliations[2].Reason != observer.ReconciliationReasonHealthCheckFailed {
		t.Fatalf("reconciliations = %#v", result.Reconciliations)
	}

	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	for _, private := range []string{
		"private-pagination-token", "./private/path", "private condition",
		"private Helm", "InstallFailed",
	} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("structured output contains source-only value %q", private)
		}
	}
	if len(requested) != 3 {
		t.Fatalf("requested paths = %v", requested)
	}
}

func TestUnknownScopeFailsBeforeSourceRequest(t *testing.T) {
	t.Parallel()

	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests++
	}))
	t.Cleanup(server.Close)
	client := newFixtureClient(t, server)
	_, err := client.ListUnhealthyReconciliations(
		context.Background(),
		observer.ListUnhealthyReconciliationsInput{Target: "flux-a", Scope: "unknown"},
	)
	if !errors.Is(err, ErrUnknownScope) || requests != 0 {
		t.Fatalf("result error = %v, requests = %d", err, requests)
	}
}

func TestFluxConfigurationFailsClosed(t *testing.T) {
	t.Parallel()

	tests := []Config{
		{TargetID: "flux-a", BaseURL: "http://example.invalid", Scopes: fixtureScopes()},
		{TargetID: "flux-a", BaseURL: "https://example.invalid", Scopes: nil},
		{
			TargetID: "flux-a",
			BaseURL:  "https://example.invalid",
			Scopes:   []Scope{{ID: "system", Namespace: "invalid/namespace"}},
		},
		{
			TargetID: "flux-a",
			BaseURL:  "https://example.invalid",
			Scopes: []Scope{
				{ID: "system", Namespace: "fixture-system"},
				{ID: "apps", Namespace: "fixture-system"},
			},
		},
	}
	for _, config := range tests {
		if _, err := NewClient(config); err == nil {
			t.Fatal("NewClient() unexpectedly succeeded")
		}
	}
}

func newFixtureClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	client, err := NewClient(Config{
		TargetID:   "flux-a",
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
		Scopes:     fixtureScopes(),
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return client
}

func fixtureScopes() []Scope {
	return []Scope{{ID: "system", Namespace: "fixture-system"}}
}
