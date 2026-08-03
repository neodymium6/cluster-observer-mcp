package monitoring

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
	"github.com/neodymium6/cluster-observer-mcp/internal/sourcehttp"
)

func TestObservationsUseOnlyFixedRequestsAndRedactSourceFields(t *testing.T) {
	t.Parallel()

	requested := make(map[string]int)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("request method = %s, want GET", r.Method)
		}
		requested[r.URL.Path]++
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/namespaces/fixture-monitoring/services/fixture-alerts:http/proxy/api/v2/alerts":
			wantQuery := "active=true&inhibited=false&silenced=false"
			if r.URL.RawQuery != wantQuery {
				t.Errorf("Alertmanager query = %q, want %q", r.URL.RawQuery, wantQuery)
			}
			_, _ = w.Write([]byte(`[
  {
    "labels": {
      "alertname": "StorageDegraded",
      "severity": "critical",
      "instance": "192.0.2.44:9090",
      "private_label": "must-not-be-returned"
    },
    "annotations": {"description": "private source diagnostic"},
    "startsAt": "2026-08-03T11:42:00Z",
    "generatorURL": "https://monitoring.example.invalid/private"
  },
  {
    "labels": {"alertname": "WorkloadSlow", "severity": "notice"},
    "startsAt": "not-a-time"
  }
]`))
		case "/api/v1/namespaces/fixture-monitoring/services/fixture-metrics:http/proxy/api/v1/query":
			if r.URL.RawQuery != "query=up" {
				t.Errorf("Prometheus query = %q, want query=up", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{
  "status": "success",
  "data": {
    "resultType": "vector",
    "result": [
      {
        "metric": {"job": "fixture-api", "instance": "fixture-a:9090", "private": "secret"},
        "value": [1785758400.25, "1"]
      },
      {
        "metric": {"job": "fixture-node", "instance": "fixture-b:9100"},
        "value": [1785758401, "0"]
      },
      {
        "metric": {"job": "unconfigured-private-job", "instance": "192.0.2.50:9999"},
        "value": [1785758402, "1"]
      }
    ]
  },
  "warnings": ["private partial diagnostic"]
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
	alerts, err := client.ListActiveAlerts(context.Background(), observer.ListActiveAlertsInput{
		Target: "monitoring-a",
		Limit:  1,
	})
	if err != nil {
		t.Fatalf("ListActiveAlerts() error = %v", err)
	}
	if len(alerts.Alerts) != 1 || alerts.Alerts[0].Name != "StorageDegraded" ||
		alerts.Alerts[0].Severity != observer.AlertSeverityCritical ||
		alerts.Alerts[0].Component != "storage" || !alerts.Truncated {
		t.Fatalf("ListActiveAlerts() = %#v", alerts)
	}

	scrapes, err := client.GetScrapeHealth(context.Background(), observer.GetScrapeHealthInput{
		Target: "monitoring-a",
	})
	if err != nil {
		t.Fatalf("GetScrapeHealth() error = %v", err)
	}
	if len(scrapes.Scrapes) != 3 || !scrapes.Partial {
		t.Fatalf("GetScrapeHealth() = %#v", scrapes)
	}
	wantStates := map[string]observer.ScrapeState{
		"api":     observer.ScrapeStateUp,
		"missing": observer.ScrapeStateMissing,
		"node":    observer.ScrapeStateDown,
	}
	for _, scrape := range scrapes.Scrapes {
		if scrape.State != wantStates[scrape.ID] {
			t.Errorf("scrape %q state = %q", scrape.ID, scrape.State)
		}
	}

	encoded, err := json.Marshal(struct {
		Alerts  observer.ListActiveAlertsOutput
		Scrapes observer.GetScrapeHealthOutput
	}{alerts, scrapes})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	for _, private := range []string{
		"192.0.2.", "must-not-be-returned", "private source diagnostic",
		"monitoring.example.invalid", "fixture-api", "fixture-node",
		"unconfigured-private-job", "private partial diagnostic",
	} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("structured output contains source-only value %q", private)
		}
	}
	if len(requested) != 2 {
		t.Fatalf("requested paths = %v", requested)
	}
}

func TestInvalidPrometheusSamplesFailClosed(t *testing.T) {
	t.Parallel()

	tests := []string{
		`{"status":"success","data":{"resultType":"matrix","result":[]}}`,
		`{"status":"success","data":{"resultType":"vector","result":[{"metric":{"job":"fixture-api","instance":"fixture-a:9090"},"value":[1,"NaN"]}]}}`,
		`{"status":"success","data":{"resultType":"vector","result":[{"metric":{"job":"fixture-api","instance":"fixture-a:9090"},"value":[1,"2"]}]}}`,
	}
	for _, fixture := range tests {
		fixture := fixture
		t.Run(fixture, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(fixture))
			}))
			t.Cleanup(server.Close)
			client := newFixtureClient(t, server)
			_, err := client.GetScrapeHealth(context.Background(), observer.GetScrapeHealthInput{
				Target: "monitoring-a",
			})
			if !errors.Is(err, sourcehttp.ErrInvalidSourceResponse) {
				t.Fatalf("GetScrapeHealth() error = %v", err)
			}
		})
	}
}

func TestMonitoringConfigurationFailsClosed(t *testing.T) {
	t.Parallel()

	base := Config{
		TargetID:     "monitoring-a",
		BaseURL:      "https://monitoring.example.invalid",
		Prometheus:   Service{Namespace: "fixture-system", Name: "metrics", Port: "http"},
		Alertmanager: Service{Namespace: "fixture-system", Name: "alerts", Port: "http"},
		Scrapes:      []ScrapeIdentity{{ID: "api", Job: "fixture-api", Instance: "fixture-a:9090"}},
	}
	tests := []Config{
		func() Config { value := base; value.Prometheus.Name = "Invalid_Name"; return value }(),
		func() Config { value := base; value.Scrapes = nil; return value }(),
		func() Config {
			value := base
			value.Scrapes = append(value.Scrapes, value.Scrapes[0])
			return value
		}(),
		func() Config {
			value := base
			value.Components = []AlertComponent{{AlertName: "bad name", Component: "api"}}
			return value
		}(),
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
		TargetID:     "monitoring-a",
		BaseURL:      server.URL,
		HTTPClient:   server.Client(),
		Prometheus:   Service{Namespace: "fixture-monitoring", Name: "fixture-metrics", Port: "http"},
		Alertmanager: Service{Namespace: "fixture-monitoring", Name: "fixture-alerts", Port: "http"},
		Scrapes: []ScrapeIdentity{
			{ID: "api", Job: "fixture-api", Instance: "fixture-a:9090"},
			{ID: "node", Job: "fixture-node", Instance: "fixture-b:9100"},
			{ID: "missing", Job: "fixture-missing", Instance: "fixture-c:9090"},
		},
		Components: []AlertComponent{{AlertName: "StorageDegraded", Component: "storage"}},
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return client
}
