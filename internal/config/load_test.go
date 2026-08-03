package config

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neodymium6/cluster-observer-mcp/internal/observer"
)

func TestLoad(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	credentialPath := filepath.Join(directory, "fixture-credential")
	writeTestFile(t, credentialPath, "clearly-fake-value\n")
	configPath := filepath.Join(directory, "config.json")
	writeTestFile(t, configPath, `{
  "targets": [
    {
      "id": "cluster-a",
      "kind": "kubernetes",
      "endpoint": "https://kubernetes.example.invalid",
      "bearerTokenFile": "`+credentialPath+`",
      "requestTimeout": "3s",
      "scopes": [
        {"id": "system", "namespace": "fixture-system"}
      ]
    }
  ]
}`)

	sources, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(sources.Kubernetes) != 1 {
		t.Fatalf("Load() Kubernetes client count = %d, want 1", len(sources.Kubernetes))
	}
	target := sources.Kubernetes[0].Target()
	if target.ID != "cluster-a" || target.Kind != observer.TargetKindKubernetes {
		t.Fatalf("Load() public target = %#v", target)
	}
	encoded := strings.Join([]string{target.ID, string(target.Kind)}, " ")
	if strings.Contains(encoded, "example.invalid") || strings.Contains(encoded, "fake-value") {
		t.Fatalf("public target disclosed private configuration: %q", encoded)
	}
}

func TestLoadMonitoringAndFluxTargets(t *testing.T) {
	t.Parallel()

	configPath := filepath.Join(t.TempDir(), "config.json")
	writeTestFile(t, configPath, `{
  "targets": [
    {
      "id": "monitoring-a",
      "kind": "monitoring",
      "endpoint": "https://kubernetes.example.invalid",
      "prometheus": {
        "namespace": "fixture-monitoring",
        "name": "fixture-metrics",
        "port": "http"
      },
      "alertmanager": {
        "namespace": "fixture-monitoring",
        "name": "fixture-alerts",
        "port": "http"
      },
      "scrapes": [
        {"id": "api", "job": "fixture-private-job", "instance": "fixture-a:9090"}
      ],
      "alertComponents": [
        {"alertName": "FixtureDegraded", "component": "platform"}
      ]
    },
    {
      "id": "flux-a",
      "kind": "flux",
      "endpoint": "https://kubernetes.example.invalid",
      "scopes": [
        {"id": "system", "namespace": "fixture-system"}
      ]
    }
  ]
}`)

	sources, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(sources.Monitoring) != 1 || len(sources.Flux) != 1 ||
		len(sources.Kubernetes) != 0 {
		t.Fatalf("Load() source counts = Kubernetes %d, monitoring %d, Flux %d",
			len(sources.Kubernetes), len(sources.Monitoring), len(sources.Flux))
	}
	publicTargets := []observer.Target{sources.Monitoring[0].Target(), sources.Flux[0].Target()}
	encoded := ""
	for _, target := range publicTargets {
		encoded += target.ID + string(target.Kind)
	}
	for _, private := range []string{
		"example.invalid", "fixture-monitoring", "fixture-private-job", "fixture-system",
	} {
		if strings.Contains(encoded, private) {
			t.Fatalf("public targets disclosed private configuration %q", private)
		}
	}
}

func TestLoadFailsClosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
	}{
		{name: "unknown field", content: `{"targets":[],"url":"https://example.invalid"}`},
		{name: "multiple objects", content: `{"targets":[]} {"targets":[]}`},
		{name: "HTTP endpoint", content: `{
  "targets": [{
    "id": "cluster-a",
    "kind": "kubernetes",
    "endpoint": "http://kubernetes.example.invalid",
    "scopes": [{"id": "system", "namespace": "fixture-system"}]
  }]
}`},
		{name: "oversized", content: strings.Repeat(" ", maxConfigBytes+1)},
		{name: "monitoring fields on Kubernetes", content: `{
  "targets": [{
    "id": "cluster-a",
    "kind": "kubernetes",
    "endpoint": "https://kubernetes.example.invalid",
    "scopes": [{"id": "system", "namespace": "fixture-system"}],
    "prometheus": {"namespace": "fixture-system", "name": "metrics", "port": "http"}
  }]
}`},
		{name: "monitoring missing services", content: `{
  "targets": [{
    "id": "monitoring-a",
    "kind": "monitoring",
    "endpoint": "https://kubernetes.example.invalid",
    "scrapes": [{"id": "api", "job": "fixture-job"}]
  }]
}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "private-config-name.json")
			writeTestFile(t, path, tt.content)
			_, err := Load(path)
			if err == nil {
				t.Fatal("Load() unexpectedly succeeded")
			}
			if strings.Contains(err.Error(), path) || strings.Contains(err.Error(), "example.invalid") {
				t.Fatalf("Load() error disclosed configuration: %v", err)
			}
		})
	}
}

func TestReadCredentialRejectsWhitespaceAndOversize(t *testing.T) {
	t.Parallel()

	for name, content := range map[string]string{
		"embedded whitespace": "clearly fake value",
		"oversized":           strings.Repeat("x", maxCredentialBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "credential")
			writeTestFile(t, path, content)
			if _, err := readCredential(path); err == nil {
				t.Fatal("readCredential() unexpectedly succeeded")
			}
		})
	}
}

func TestFileCredentialSourceObservesRotation(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "projected-token")
	writeTestFile(t, path, "clearly-fake-token-one\n")
	source, err := fileCredentialSource(path)
	if err != nil {
		t.Fatalf("fileCredentialSource() error = %v", err)
	}
	first, err := source.BearerToken(context.Background())
	if err != nil {
		t.Fatalf("BearerToken(first) error = %v", err)
	}

	writeTestFile(t, path, "clearly-fake-token-two\n")
	second, err := source.BearerToken(context.Background())
	if err != nil {
		t.Fatalf("BearerToken(second) error = %v", err)
	}
	if first != "clearly-fake-token-one" || second != "clearly-fake-token-two" {
		t.Fatalf("BearerToken() values did not follow rotation")
	}
}

func TestSecureHTTPClientDisablesProxyAndRedirects(t *testing.T) {
	t.Parallel()

	client, err := secureHTTPClient("")
	if err != nil {
		t.Fatalf("secureHTTPClient() error = %v", err)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("HTTP transport type = %T", client.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("HTTP transport uses an environment proxy")
	}
	if transport.TLSClientConfig == nil ||
		transport.TLSClientConfig.MinVersion != tls.VersionTLS12 {
		t.Fatalf("TLS configuration = %#v", transport.TLSClientConfig)
	}
	if err := client.CheckRedirect(nil, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("CheckRedirect() error = %v", err)
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
}
