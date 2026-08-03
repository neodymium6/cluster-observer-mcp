package httptransport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"
)

// ProbeKind selects one fixed loopback health endpoint.
type ProbeKind string

const (
	ProbeLiveness  ProbeKind = "liveness"
	ProbeReadiness ProbeKind = "readiness"
	ProbeStartup   ProbeKind = "startup"
)

var ErrProbeFailed = errors.New("HTTP health probe failed")

// Probe contacts a fixed loopback health endpoint. It deliberately accepts no
// hostname, URL, or path.
func Probe(ctx context.Context, port int, kind ProbeKind) error {
	if port < 1 || port > 65535 {
		return ErrInvalidPort
	}
	path := ""
	switch kind {
	case ProbeLiveness:
		path = "/livez"
	case ProbeReadiness, ProbeStartup:
		path = "/readyz"
	default:
		return ErrProbeFailed
	}

	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		"http://"+loopbackAddress+":"+strconv.Itoa(port)+path,
		nil,
	)
	if err != nil {
		return ErrProbeFailed
	}
	client := &http.Client{
		Transport: &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout: 2 * time.Second,
	}
	response, err := client.Do(request)
	if err != nil {
		return ErrProbeFailed
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4))
	if err != nil || response.StatusCode != http.StatusOK || string(body) != "ok\n" {
		return ErrProbeFailed
	}
	return nil
}
