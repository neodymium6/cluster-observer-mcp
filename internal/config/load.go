// Package config loads bounded private runtime configuration. Configuration
// values are never returned by an MCP tool.
package config

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/neodymium6/cluster-observer-mcp/internal/kubernetes"
	"github.com/neodymium6/cluster-observer-mcp/internal/observer"
)

const (
	maxConfigBytes     = 64 * 1024
	maxCredentialBytes = 16 * 1024
	maxCABundleBytes   = 1024 * 1024
)

// File is the private runtime configuration schema.
type File struct {
	Targets []Target `json:"targets"`
}

// Target configures one purpose-built observation source.
type Target struct {
	ID              string  `json:"id"`
	Kind            string  `json:"kind"`
	Endpoint        string  `json:"endpoint"`
	BearerTokenFile string  `json:"bearerTokenFile,omitempty"`
	CABundleFile    string  `json:"caBundleFile,omitempty"`
	RequestTimeout  string  `json:"requestTimeout,omitempty"`
	Scopes          []Scope `json:"scopes"`
}

// Scope maps a public scope identifier to a private Kubernetes namespace.
type Scope struct {
	ID        string `json:"id"`
	Namespace string `json:"namespace"`
}

// Load opens and strictly decodes a bounded configuration file.
func Load(path string) ([]*kubernetes.Client, error) {
	body, err := readBoundedFile(path, maxConfigBytes)
	if err != nil {
		return nil, errors.New("open runtime configuration failed")
	}

	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var config File
	if err := decoder.Decode(&config); err != nil {
		return nil, errors.New("decode runtime configuration failed")
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return nil, err
	}
	if len(config.Targets) > observer.MaxTargets {
		return nil, errors.New("runtime configuration has too many targets")
	}

	clients := make([]*kubernetes.Client, 0, len(config.Targets))
	for _, target := range config.Targets {
		client, err := buildTarget(target)
		if err != nil {
			return nil, err
		}
		clients = append(clients, client)
	}
	return clients, nil
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("runtime configuration must contain one JSON object")
	}
	return nil
}

func buildTarget(target Target) (*kubernetes.Client, error) {
	if target.Kind != string(observer.TargetKindKubernetes) {
		return nil, errors.New("runtime configuration contains an unsupported target kind")
	}

	credential, err := fileCredentialSource(target.BearerTokenFile)
	if err != nil {
		return nil, err
	}
	httpClient, err := secureHTTPClient(target.CABundleFile)
	if err != nil {
		return nil, err
	}

	timeout := time.Duration(0)
	if target.RequestTimeout != "" {
		timeout, err = time.ParseDuration(target.RequestTimeout)
		if err != nil {
			return nil, errors.New("runtime configuration contains an invalid request timeout")
		}
	}

	scopes := make([]kubernetes.Scope, 0, len(target.Scopes))
	for _, scope := range target.Scopes {
		scopes = append(scopes, kubernetes.Scope{
			ID:        scope.ID,
			Namespace: scope.Namespace,
		})
	}

	client, err := kubernetes.NewClient(kubernetes.Config{
		TargetID:       target.ID,
		BaseURL:        target.Endpoint,
		Credential:     credential,
		Scopes:         scopes,
		HTTPClient:     httpClient,
		RequestTimeout: timeout,
	})
	if err != nil {
		return nil, errors.New("runtime target configuration is invalid")
	}
	return client, nil
}

func fileCredentialSource(path string) (kubernetes.CredentialSource, error) {
	if path == "" {
		return nil, nil
	}
	// Fail closed during startup, but discard the value so each request reads
	// the projected token file again and observes atomic token rotation.
	if _, err := readCredential(path); err != nil {
		return nil, err
	}
	return kubernetes.CredentialSourceFunc(func(context.Context) (string, error) {
		return readCredential(path)
	}), nil
}

func readCredential(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	body, err := readBoundedFile(path, maxCredentialBytes)
	if err != nil {
		return "", errors.New("read source credential failed")
	}
	token := strings.TrimSpace(string(body))
	if token == "" || strings.ContainsAny(token, " \t\r\n") {
		return "", errors.New("source credential is invalid")
	}
	return token, nil
}

func secureHTTPClient(caBundlePath string) (*http.Client, error) {
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	if caBundlePath != "" {
		bundle, err := readBoundedFile(caBundlePath, maxCABundleBytes)
		if err != nil {
			return nil, errors.New("read source CA bundle failed")
		}
		if !roots.AppendCertsFromPEM(bundle) {
			return nil, errors.New("source CA bundle is invalid")
		}
	}

	return &http.Client{
		Transport: &http.Transport{
			Proxy: nil,
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
				RootCAs:    roots,
			},
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, nil
}

func readBoundedFile(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	body, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, errors.New("file exceeds limit")
	}
	return body, nil
}
