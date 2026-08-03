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

	"github.com/neodymium6/cluster-observer-mcp/internal/flux"
	"github.com/neodymium6/cluster-observer-mcp/internal/kubernetes"
	"github.com/neodymium6/cluster-observer-mcp/internal/monitoring"
	"github.com/neodymium6/cluster-observer-mcp/internal/observer"
	"github.com/neodymium6/cluster-observer-mcp/internal/sourcehttp"
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
	ID              string           `json:"id"`
	Kind            string           `json:"kind"`
	Endpoint        string           `json:"endpoint"`
	BearerTokenFile string           `json:"bearerTokenFile,omitempty"`
	CABundleFile    string           `json:"caBundleFile,omitempty"`
	RequestTimeout  string           `json:"requestTimeout,omitempty"`
	Scopes          []Scope          `json:"scopes,omitempty"`
	Prometheus      *Service         `json:"prometheus,omitempty"`
	Alertmanager    *Service         `json:"alertmanager,omitempty"`
	Scrapes         []Scrape         `json:"scrapes,omitempty"`
	AlertComponents []AlertComponent `json:"alertComponents,omitempty"`
}

// Scope maps a public scope identifier to a private Kubernetes namespace.
type Scope struct {
	ID        string `json:"id"`
	Namespace string `json:"namespace"`
}

// Service configures one fixed Kubernetes Service proxy destination.
type Service struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Port      string `json:"port"`
}

// Scrape maps exact private Prometheus label values to an opaque public ID.
type Scrape struct {
	ID       string `json:"id"`
	Job      string `json:"job"`
	Instance string `json:"instance,omitempty"`
}

// AlertComponent maps an exact alert name to an opaque component ID.
type AlertComponent struct {
	AlertName string `json:"alertName"`
	Component string `json:"component"`
}

// Sources contains every configured purpose-built source family.
type Sources struct {
	Kubernetes []*kubernetes.Client
	Monitoring []*monitoring.Client
	Flux       []*flux.Client
}

// Load opens and strictly decodes a bounded configuration file.
func Load(path string) (Sources, error) {
	body, err := readBoundedFile(path, maxConfigBytes)
	if err != nil {
		return Sources{}, errors.New("open runtime configuration failed")
	}

	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var config File
	if err := decoder.Decode(&config); err != nil {
		return Sources{}, errors.New("decode runtime configuration failed")
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return Sources{}, err
	}
	if len(config.Targets) > observer.MaxTargets {
		return Sources{}, errors.New("runtime configuration has too many targets")
	}

	sources := Sources{}
	for _, target := range config.Targets {
		switch target.Kind {
		case string(observer.TargetKindKubernetes):
			client, err := buildKubernetesTarget(target)
			if err != nil {
				return Sources{}, err
			}
			sources.Kubernetes = append(sources.Kubernetes, client)
		case string(observer.TargetKindMonitoring):
			client, err := buildMonitoringTarget(target)
			if err != nil {
				return Sources{}, err
			}
			sources.Monitoring = append(sources.Monitoring, client)
		case string(observer.TargetKindFlux):
			client, err := buildFluxTarget(target)
			if err != nil {
				return Sources{}, err
			}
			sources.Flux = append(sources.Flux, client)
		default:
			return Sources{}, errors.New("runtime configuration contains an unsupported target kind")
		}
	}
	return sources, nil
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("runtime configuration must contain one JSON object")
	}
	return nil
}

func buildKubernetesTarget(target Target) (*kubernetes.Client, error) {
	if target.Prometheus != nil || target.Alertmanager != nil || len(target.Scrapes) != 0 ||
		len(target.AlertComponents) != 0 {
		return nil, errors.New("runtime target configuration is invalid")
	}
	credential, httpClient, timeout, err := buildSharedTarget(target)
	if err != nil {
		return nil, err
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

func buildMonitoringTarget(target Target) (*monitoring.Client, error) {
	if len(target.Scopes) != 0 || target.Prometheus == nil || target.Alertmanager == nil {
		return nil, errors.New("runtime target configuration is invalid")
	}
	credential, httpClient, timeout, err := buildSharedTarget(target)
	if err != nil {
		return nil, err
	}
	scrapes := make([]monitoring.ScrapeIdentity, 0, len(target.Scrapes))
	for _, scrape := range target.Scrapes {
		scrapes = append(scrapes, monitoring.ScrapeIdentity{
			ID: scrape.ID, Job: scrape.Job, Instance: scrape.Instance,
		})
	}
	components := make([]monitoring.AlertComponent, 0, len(target.AlertComponents))
	for _, component := range target.AlertComponents {
		components = append(components, monitoring.AlertComponent{
			AlertName: component.AlertName, Component: component.Component,
		})
	}
	client, err := monitoring.NewClient(monitoring.Config{
		TargetID:       target.ID,
		BaseURL:        target.Endpoint,
		Credential:     credential,
		HTTPClient:     httpClient,
		RequestTimeout: timeout,
		Prometheus: monitoring.Service{
			Namespace: target.Prometheus.Namespace,
			Name:      target.Prometheus.Name,
			Port:      target.Prometheus.Port,
		},
		Alertmanager: monitoring.Service{
			Namespace: target.Alertmanager.Namespace,
			Name:      target.Alertmanager.Name,
			Port:      target.Alertmanager.Port,
		},
		Scrapes:    scrapes,
		Components: components,
	})
	if err != nil {
		return nil, errors.New("runtime target configuration is invalid")
	}
	return client, nil
}

func buildFluxTarget(target Target) (*flux.Client, error) {
	if target.Prometheus != nil || target.Alertmanager != nil || len(target.Scrapes) != 0 ||
		len(target.AlertComponents) != 0 {
		return nil, errors.New("runtime target configuration is invalid")
	}
	credential, httpClient, timeout, err := buildSharedTarget(target)
	if err != nil {
		return nil, err
	}
	scopes := make([]flux.Scope, 0, len(target.Scopes))
	for _, scope := range target.Scopes {
		scopes = append(scopes, flux.Scope{ID: scope.ID, Namespace: scope.Namespace})
	}
	client, err := flux.NewClient(flux.Config{
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

func buildSharedTarget(
	target Target,
) (sourcehttp.CredentialSource, *http.Client, time.Duration, error) {
	credential, err := fileCredentialSource(target.BearerTokenFile)
	if err != nil {
		return nil, nil, 0, err
	}
	httpClient, err := secureHTTPClient(target.CABundleFile)
	if err != nil {
		return nil, nil, 0, err
	}
	timeout := time.Duration(0)
	if target.RequestTimeout != "" {
		timeout, err = time.ParseDuration(target.RequestTimeout)
		if err != nil {
			return nil, nil, 0, errors.New("runtime configuration contains an invalid request timeout")
		}
	}
	return credential, httpClient, timeout, nil
}

func fileCredentialSource(path string) (sourcehttp.CredentialSource, error) {
	if path == "" {
		return nil, nil
	}
	// Fail closed during startup, but discard the value so each request reads
	// the projected token file again and observes atomic token rotation.
	if _, err := readCredential(path); err != nil {
		return nil, err
	}
	return sourcehttp.CredentialSourceFunc(func(context.Context) (string, error) {
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
