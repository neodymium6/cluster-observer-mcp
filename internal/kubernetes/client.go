// Package kubernetes implements bounded observations against fixed Kubernetes
// API resource paths. It does not expose a generic Kubernetes request surface.
package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"time"

	"github.com/neodymium6/cluster-observer-mcp/internal/observer"
)

const (
	defaultRequestTimeout  = 5 * time.Second
	maxConcurrentRequests  = 4
	maxSourceResponseBytes = 2 * 1024 * 1024
	resourceListLimit      = "500"
)

var namespacePattern = regexp.MustCompile(
	`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`,
)

var (
	// ErrSourceUnavailable replaces network errors that could disclose an
	// endpoint.
	ErrSourceUnavailable = errors.New("Kubernetes source is unavailable")
	// ErrSourceTimeout replaces timeout errors that could disclose an endpoint.
	ErrSourceTimeout = errors.New("Kubernetes source request timed out")
	// ErrSourceRejected indicates a non-success source response.
	ErrSourceRejected = errors.New("Kubernetes source rejected the request")
	// ErrInvalidSourceResponse replaces parsing details and response content.
	ErrInvalidSourceResponse = errors.New("Kubernetes source returned an invalid response")
	// ErrUnknownScope indicates a scope outside the configured allowlist.
	ErrUnknownScope = errors.New("Kubernetes scope is not configured")
)

// Scope maps a public opaque identifier to one allowed Kubernetes namespace.
type Scope struct {
	ID        string
	Namespace string
}

// Config contains private source settings. Callers must not log this value.
type Config struct {
	TargetID       string
	BaseURL        string
	BearerToken    string
	Scopes         []Scope
	HTTPClient     *http.Client
	RequestTimeout time.Duration
}

// Client performs purpose-built, read-only observations.
type Client struct {
	targetID       string
	baseURL        *url.URL
	bearerToken    string
	scopes         map[string]string
	httpClient     *http.Client
	requestTimeout time.Duration
	requestSlots   chan struct{}
	now            func() time.Time
}

// NewClient validates and copies source configuration.
func NewClient(config Config) (*Client, error) {
	if err := observer.ValidateIdentifier("target", config.TargetID); err != nil {
		return nil, err
	}

	baseURL, err := url.Parse(config.BaseURL)
	if err != nil || baseURL.Scheme != "https" || baseURL.Host == "" ||
		baseURL.User != nil || (baseURL.Path != "" && baseURL.Path != "/") ||
		baseURL.RawQuery != "" || baseURL.Fragment != "" {
		return nil, errors.New("Kubernetes base URL must be an HTTPS origin")
	}

	if len(config.Scopes) == 0 || len(config.Scopes) > observer.MaxTargets {
		return nil, errors.New("Kubernetes target must have from 1 to 32 scopes")
	}
	scopes := make(map[string]string, len(config.Scopes))
	seenNamespaces := make(map[string]struct{}, len(config.Scopes))
	for _, scope := range config.Scopes {
		if err := observer.ValidateIdentifier("scope", scope.ID); err != nil {
			return nil, err
		}
		if !namespacePattern.MatchString(scope.Namespace) {
			return nil, errors.New("Kubernetes namespace is invalid")
		}
		if _, exists := scopes[scope.ID]; exists {
			return nil, errors.New("Kubernetes scope identifier is duplicated")
		}
		if _, exists := seenNamespaces[scope.Namespace]; exists {
			return nil, errors.New("Kubernetes namespace is duplicated")
		}
		scopes[scope.ID] = scope.Namespace
		seenNamespaces[scope.Namespace] = struct{}{}
	}

	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	requestTimeout := config.RequestTimeout
	if requestTimeout == 0 {
		requestTimeout = defaultRequestTimeout
	}
	if requestTimeout < time.Millisecond || requestTimeout > 30*time.Second {
		return nil, errors.New("Kubernetes request timeout must be from 1ms to 30s")
	}

	return &Client{
		targetID:       config.TargetID,
		baseURL:        baseURL,
		bearerToken:    config.BearerToken,
		scopes:         scopes,
		httpClient:     httpClient,
		requestTimeout: requestTimeout,
		requestSlots:   make(chan struct{}, maxConcurrentRequests),
		now:            time.Now,
	}, nil
}

// Target returns the endpoint-free public target descriptor.
func (c *Client) Target() observer.Target {
	return observer.Target{
		ID:   c.targetID,
		Kind: observer.TargetKindKubernetes,
		Capabilities: []observer.Capability{
			observer.CapabilityKubernetesClusterHealth,
			observer.CapabilityKubernetesUnhealthyWorkloads,
		},
	}
}

func (c *Client) selectedScopes(scopeID string) ([]Scope, error) {
	if scopeID != "" {
		namespace, ok := c.scopes[scopeID]
		if !ok {
			return nil, ErrUnknownScope
		}
		return []Scope{{ID: scopeID, Namespace: namespace}}, nil
	}

	scopes := make([]Scope, 0, len(c.scopes))
	for id, namespace := range c.scopes {
		scopes = append(scopes, Scope{ID: id, Namespace: namespace})
	}
	slices.SortFunc(scopes, func(a, b Scope) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return scopes, nil
}

func (c *Client) get(ctx context.Context, path string) ([]byte, error) {
	requestContext, cancel := context.WithTimeout(ctx, c.requestTimeout)
	defer cancel()

	select {
	case c.requestSlots <- struct{}{}:
		defer func() { <-c.requestSlots }()
	case <-requestContext.Done():
		if errors.Is(requestContext.Err(), context.DeadlineExceeded) {
			return nil, ErrSourceTimeout
		}
		return nil, ErrSourceUnavailable
	}

	requestURL := *c.baseURL
	requestURL.Path = path
	query := requestURL.Query()
	query.Set("limit", resourceListLimit)
	requestURL.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(
		requestContext,
		http.MethodGet,
		requestURL.String(),
		nil,
	)
	if err != nil {
		return nil, ErrSourceUnavailable
	}
	request.Header.Set("Accept", "application/json")
	if c.bearerToken != "" {
		request.Header.Set("Authorization", "Bearer "+c.bearerToken)
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		if errors.Is(requestContext.Err(), context.DeadlineExceeded) {
			return nil, ErrSourceTimeout
		}
		return nil, ErrSourceUnavailable
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("%w: status %d", ErrSourceRejected, response.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, maxSourceResponseBytes+1))
	if err != nil {
		return nil, ErrSourceUnavailable
	}
	if len(body) > maxSourceResponseBytes {
		return nil, ErrInvalidSourceResponse
	}

	return body, nil
}
