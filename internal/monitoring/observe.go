package monitoring

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/neodymium6/cluster-observer-mcp/internal/observer"
	"github.com/neodymium6/cluster-observer-mcp/internal/sourcehttp"
)

type alertmanagerAlert struct {
	Labels struct {
		Name     string `json:"alertname"`
		Severity string `json:"severity"`
	} `json:"labels"`
	StartsAt string `json:"startsAt"`
}

type prometheusResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string             `json:"resultType"`
		Result     []prometheusSample `json:"result"`
	} `json:"data"`
	Warnings []string `json:"warnings"`
}

type prometheusSample struct {
	Metric struct {
		Job      string `json:"job"`
		Instance string `json:"instance"`
	} `json:"metric"`
	Value []json.RawMessage `json:"value"`
}

// ListActiveAlerts returns a bounded redacted view of fixed Alertmanager
// active-alert output.
func (c *Client) ListActiveAlerts(
	ctx context.Context,
	input observer.ListActiveAlertsInput,
) (observer.ListActiveAlertsOutput, error) {
	input, err := input.Normalize()
	if err != nil {
		return observer.ListActiveAlertsOutput{}, err
	}
	if input.Target != c.targetID {
		return observer.ListActiveAlertsOutput{}, errors.New("monitoring target is not configured")
	}

	query := url.Values{
		"active":    {"true"},
		"inhibited": {"false"},
		"silenced":  {"false"},
	}
	body, err := c.http.Get(ctx, c.alertmanagerPath+"/api/v2/alerts", query)
	if err != nil {
		return observer.ListActiveAlertsOutput{}, err
	}
	var sourceAlerts []alertmanagerAlert
	if err := json.Unmarshal(body, &sourceAlerts); err != nil {
		return observer.ListActiveAlertsOutput{}, sourcehttp.ErrInvalidSourceResponse
	}

	result := observer.ListActiveAlertsOutput{
		Target:     c.targetID,
		ObservedAt: c.now().UTC(),
		Alerts:     make([]observer.ActiveAlert, 0, min(len(sourceAlerts), input.Limit)),
	}
	for _, sourceAlert := range sourceAlerts {
		name := sourceAlert.Labels.Name
		if !alertNamePattern.MatchString(name) {
			name = "unknown"
		}
		alert := observer.ActiveAlert{
			Name:      name,
			Severity:  observer.NormalizeAlertSeverity(sourceAlert.Labels.Severity),
			Component: c.components[name],
		}
		if startedAt, err := time.Parse(time.RFC3339Nano, sourceAlert.StartsAt); err == nil {
			startedAt = startedAt.UTC()
			alert.StartedAt = &startedAt
		}
		result.Alerts = append(result.Alerts, alert)
	}
	slices.SortFunc(result.Alerts, compareAlerts)
	if len(result.Alerts) > input.Limit {
		result.Alerts = result.Alerts[:input.Limit]
		result.Truncated = true
	}
	if err := observer.CheckResultSize(result); err != nil {
		return observer.ListActiveAlertsOutput{}, err
	}
	return result, nil
}

// GetScrapeHealth returns configured identities from the fixed Prometheus up
// query without returning raw labels.
func (c *Client) GetScrapeHealth(
	ctx context.Context,
	input observer.GetScrapeHealthInput,
) (observer.GetScrapeHealthOutput, error) {
	if err := input.Validate(); err != nil {
		return observer.GetScrapeHealthOutput{}, err
	}
	if input.Target != c.targetID {
		return observer.GetScrapeHealthOutput{}, errors.New("monitoring target is not configured")
	}

	body, err := c.http.Get(ctx, c.prometheusPath+"/api/v1/query", url.Values{"query": {"up"}})
	if err != nil {
		return observer.GetScrapeHealthOutput{}, err
	}
	var response prometheusResponse
	if err := json.Unmarshal(body, &response); err != nil || response.Status != "success" ||
		response.Data.ResultType != "vector" {
		return observer.GetScrapeHealthOutput{}, sourcehttp.ErrInvalidSourceResponse
	}

	samples := make(map[string]observer.ScrapeHealth, len(response.Data.Result))
	for _, sample := range response.Data.Result {
		key := seriesKey(sample.Metric.Job, sample.Metric.Instance)
		if _, exists := samples[key]; exists {
			return observer.GetScrapeHealthOutput{}, sourcehttp.ErrInvalidSourceResponse
		}
		state, sampledAt, err := parsePrometheusValue(sample.Value)
		if err != nil {
			return observer.GetScrapeHealthOutput{}, sourcehttp.ErrInvalidSourceResponse
		}
		samples[key] = observer.ScrapeHealth{State: state, SampledAt: sampledAt}
	}

	result := observer.GetScrapeHealthOutput{
		Target:     c.targetID,
		ObservedAt: c.now().UTC(),
		Scrapes:    make([]observer.ScrapeHealth, 0, len(c.scrapes)),
		Partial:    len(response.Warnings) > 0,
	}
	for _, configured := range c.scrapes {
		scrape, ok := samples[seriesKey(configured.Job, configured.Instance)]
		if !ok {
			scrape.State = observer.ScrapeStateMissing
		}
		scrape.ID = configured.ID
		result.Scrapes = append(result.Scrapes, scrape)
	}
	slices.SortFunc(result.Scrapes, func(a, b observer.ScrapeHealth) int {
		return strings.Compare(a.ID, b.ID)
	})
	if err := observer.CheckResultSize(result); err != nil {
		return observer.GetScrapeHealthOutput{}, err
	}
	return result, nil
}

func compareAlerts(a, b observer.ActiveAlert) int {
	severityRank := func(severity observer.AlertSeverity) int {
		switch severity {
		case observer.AlertSeverityCritical:
			return 0
		case observer.AlertSeverityWarning:
			return 1
		case observer.AlertSeverityInfo:
			return 2
		default:
			return 3
		}
	}
	if aRank, bRank := severityRank(a.Severity), severityRank(b.Severity); aRank != bRank {
		return aRank - bRank
	}
	if compared := strings.Compare(a.Name, b.Name); compared != 0 {
		return compared
	}
	if a.StartedAt == nil && b.StartedAt != nil {
		return 1
	}
	if a.StartedAt != nil && b.StartedAt == nil {
		return -1
	}
	if a.StartedAt == nil {
		return 0
	}
	return a.StartedAt.Compare(*b.StartedAt)
}

func parsePrometheusValue(value []json.RawMessage) (observer.ScrapeState, *time.Time, error) {
	if len(value) != 2 {
		return "", nil, errors.New("invalid Prometheus sample")
	}
	var timestamp float64
	var sampleText string
	if err := json.Unmarshal(value[0], &timestamp); err != nil || math.IsNaN(timestamp) ||
		math.IsInf(timestamp, 0) || timestamp < 0 || timestamp > 253402300799 {
		return "", nil, errors.New("invalid Prometheus timestamp")
	}
	if err := json.Unmarshal(value[1], &sampleText); err != nil {
		return "", nil, errors.New("invalid Prometheus sample")
	}
	sample, err := strconv.ParseFloat(sampleText, 64)
	if err != nil || math.IsNaN(sample) || math.IsInf(sample, 0) {
		return "", nil, errors.New("invalid Prometheus sample")
	}
	var state observer.ScrapeState
	switch sample {
	case 0:
		state = observer.ScrapeStateDown
	case 1:
		state = observer.ScrapeStateUp
	default:
		return "", nil, errors.New("invalid Prometheus up value")
	}
	seconds, fraction := math.Modf(timestamp)
	sampledAt := time.Unix(int64(seconds), int64(fraction*float64(time.Second))).UTC()
	return state, &sampledAt, nil
}
