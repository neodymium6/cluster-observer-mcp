package config

import (
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

	clients, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(clients) != 1 {
		t.Fatalf("Load() client count = %d, want 1", len(clients))
	}
	target := clients[0].Target()
	if target.ID != "cluster-a" || target.Kind != observer.TargetKindKubernetes {
		t.Fatalf("Load() public target = %#v", target)
	}
	encoded := strings.Join([]string{target.ID, string(target.Kind)}, " ")
	if strings.Contains(encoded, "example.invalid") || strings.Contains(encoded, "fake-value") {
		t.Fatalf("public target disclosed private configuration: %q", encoded)
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
