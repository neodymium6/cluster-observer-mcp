// Package monitoring implements bounded Alertmanager and Prometheus
// observations through fixed Kubernetes Service proxy paths.
package monitoring

import (
	"errors"
	"net/http"
	"regexp"
	"time"

	"github.com/neodymium6/cluster-observer-mcp/internal/observer"
	"github.com/neodymium6/cluster-observer-mcp/internal/sourcehttp"
)

var dnsLabelPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
var portNamePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,13}[a-z0-9])?$`)
var alertNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_:.-]{0,127}$`)

// Service identifies one private Kubernetes Service proxy destination.
type Service struct {
	Namespace string
	Name      string
	Port      string
}

// ScrapeIdentity maps an exact private Prometheus series identity to an
// opaque public identifier.
type ScrapeIdentity struct {
	ID       string
	Job      string
	Instance string
}

// AlertComponent maps an exact bounded alert name to an opaque component ID.
type AlertComponent struct {
	AlertName string
	Component string
}

// Config contains private monitoring source settings. Callers must not log it.
type Config struct {
	TargetID       string
	BaseURL        string
	Credential     sourcehttp.CredentialSource
	HTTPClient     *http.Client
	RequestTimeout time.Duration
	Prometheus     Service
	Alertmanager   Service
	Scrapes        []ScrapeIdentity
	Components     []AlertComponent
}

// Client performs purpose-built monitoring observations.
type Client struct {
	targetID         string
	http             *sourcehttp.Client
	prometheusPath   string
	alertmanagerPath string
	scrapes          []ScrapeIdentity
	components       map[string]string
	now              func() time.Time
}

// NewClient validates and copies monitoring source configuration.
func NewClient(config Config) (*Client, error) {
	if err := observer.ValidateIdentifier("target", config.TargetID); err != nil {
		return nil, err
	}
	if err := validateService(config.Prometheus); err != nil {
		return nil, errors.New("Prometheus Service configuration is invalid")
	}
	if err := validateService(config.Alertmanager); err != nil {
		return nil, errors.New("Alertmanager Service configuration is invalid")
	}
	if len(config.Scrapes) == 0 || len(config.Scrapes) > observer.MaxListLimit {
		return nil, errors.New("monitoring target must have from 1 to 50 scrape identities")
	}

	scrapes := make([]ScrapeIdentity, 0, len(config.Scrapes))
	seenIDs := make(map[string]struct{}, len(config.Scrapes))
	seenSeries := make(map[string]struct{}, len(config.Scrapes))
	for _, scrape := range config.Scrapes {
		if err := observer.ValidateIdentifier("scrape", scrape.ID); err != nil {
			return nil, err
		}
		if !validLabelValue(scrape.Job) || (scrape.Instance != "" && !validLabelValue(scrape.Instance)) {
			return nil, errors.New("monitoring scrape label match is invalid")
		}
		key := seriesKey(scrape.Job, scrape.Instance)
		if _, exists := seenIDs[scrape.ID]; exists {
			return nil, errors.New("monitoring scrape identifier is duplicated")
		}
		if _, exists := seenSeries[key]; exists {
			return nil, errors.New("monitoring scrape label match is duplicated")
		}
		seenIDs[scrape.ID] = struct{}{}
		seenSeries[key] = struct{}{}
		scrapes = append(scrapes, scrape)
	}

	if len(config.Components) > observer.MaxListLimit {
		return nil, errors.New("monitoring target has too many alert component mappings")
	}
	components := make(map[string]string, len(config.Components))
	for _, component := range config.Components {
		if !alertNamePattern.MatchString(component.AlertName) {
			return nil, errors.New("monitoring alert name mapping is invalid")
		}
		if err := observer.ValidateIdentifier("component", component.Component); err != nil {
			return nil, err
		}
		if _, exists := components[component.AlertName]; exists {
			return nil, errors.New("monitoring alert name mapping is duplicated")
		}
		components[component.AlertName] = component.Component
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
		targetID:         config.TargetID,
		http:             httpClient,
		prometheusPath:   serviceProxyPath(config.Prometheus),
		alertmanagerPath: serviceProxyPath(config.Alertmanager),
		scrapes:          scrapes,
		components:       components,
		now:              time.Now,
	}, nil
}

// Target returns the endpoint-free public monitoring target descriptor.
func (c *Client) Target() observer.Target {
	return observer.Target{
		ID:   c.targetID,
		Kind: observer.TargetKindMonitoring,
		Capabilities: []observer.Capability{
			observer.CapabilityMonitoringActiveAlerts,
			observer.CapabilityMonitoringScrapeHealth,
		},
	}
}

func validateService(service Service) error {
	if !dnsLabelPattern.MatchString(service.Namespace) ||
		!dnsLabelPattern.MatchString(service.Name) ||
		!portNamePattern.MatchString(service.Port) {
		return errors.New("invalid Service coordinate")
	}
	return nil
}

func serviceProxyPath(service Service) string {
	return "/api/v1/namespaces/" + service.Namespace + "/services/" +
		service.Name + ":" + service.Port + "/proxy"
}

func validLabelValue(value string) bool {
	return value != "" && len(value) <= 256 && !regexp.MustCompile(`[\x00-\x1f\x7f]`).MatchString(value)
}

func seriesKey(job, instance string) string {
	return job + "\x00" + instance
}
