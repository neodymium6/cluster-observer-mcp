// Package flux implements bounded readiness observations against fixed Flux
// custom-resource API paths.
package flux

import (
	"errors"
	"net/http"
	"regexp"
	"slices"
	"time"

	"github.com/neodymium6/cluster-observer-mcp/internal/observer"
	"github.com/neodymium6/cluster-observer-mcp/internal/sourcehttp"
)

var namespacePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// ErrUnknownScope indicates a scope outside the configured allowlist.
var ErrUnknownScope = errors.New("Flux scope is not configured")

// Scope maps a public opaque identifier to one allowed Kubernetes namespace.
type Scope struct {
	ID        string
	Namespace string
}

// Config contains private Flux source settings. Callers must not log it.
type Config struct {
	TargetID       string
	BaseURL        string
	Credential     sourcehttp.CredentialSource
	Scopes         []Scope
	HTTPClient     *http.Client
	RequestTimeout time.Duration
}

// Client performs purpose-built Flux observations.
type Client struct {
	targetID string
	http     *sourcehttp.Client
	scopes   map[string]string
	now      func() time.Time
}

// NewClient validates and copies Flux source configuration.
func NewClient(config Config) (*Client, error) {
	if err := observer.ValidateIdentifier("target", config.TargetID); err != nil {
		return nil, err
	}
	if len(config.Scopes) == 0 || len(config.Scopes) > observer.MaxTargets {
		return nil, errors.New("Flux target must have from 1 to 32 scopes")
	}
	scopes := make(map[string]string, len(config.Scopes))
	seenNamespaces := make(map[string]struct{}, len(config.Scopes))
	for _, scope := range config.Scopes {
		if err := observer.ValidateIdentifier("scope", scope.ID); err != nil {
			return nil, err
		}
		if !namespacePattern.MatchString(scope.Namespace) {
			return nil, errors.New("Flux namespace is invalid")
		}
		if _, exists := scopes[scope.ID]; exists {
			return nil, errors.New("Flux scope identifier is duplicated")
		}
		if _, exists := seenNamespaces[scope.Namespace]; exists {
			return nil, errors.New("Flux namespace is duplicated")
		}
		scopes[scope.ID] = scope.Namespace
		seenNamespaces[scope.Namespace] = struct{}{}
	}

	httpClient, err := sourcehttp.New(sourcehttp.Config{
		BaseURL:        config.BaseURL,
		Credential:     config.Credential,
		HTTPClient:     config.HTTPClient,
		RequestTimeout: config.RequestTimeout,
	})
	if err != nil {
		return nil, err
	}
	return &Client{
		targetID: config.TargetID,
		http:     httpClient,
		scopes:   scopes,
		now:      time.Now,
	}, nil
}

// Target returns the endpoint-free public Flux target descriptor.
func (c *Client) Target() observer.Target {
	return observer.Target{
		ID:   c.targetID,
		Kind: observer.TargetKindFlux,
		Capabilities: []observer.Capability{
			observer.CapabilityFluxUnhealthyReconciliations,
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
