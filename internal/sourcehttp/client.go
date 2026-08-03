// Package sourcehttp implements the shared bounded HTTPS request boundary used
// by purpose-built observation adapters. It is not exposed as a generic MCP
// HTTP tool.
package sourcehttp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultRequestTimeout  = 5 * time.Second
	maxConcurrentRequests  = 4
	maxSourceResponseBytes = 2 * 1024 * 1024
	maxBearerTokenBytes    = 16 * 1024
)

var (
	ErrSourceUnavailable     = errors.New("source is unavailable")
	ErrSourceTimeout         = errors.New("source request timed out")
	ErrSourceRejected        = errors.New("source rejected the request")
	ErrCredentialUnavailable = errors.New("source credential is unavailable")
	ErrInvalidSourceResponse = errors.New("source returned an invalid response")
)

// CredentialSource returns the current bearer token immediately before a
// source request. Implementations must support projected token rotation.
type CredentialSource interface {
	BearerToken(context.Context) (string, error)
}

// CredentialSourceFunc adapts a function to CredentialSource.
type CredentialSourceFunc func(context.Context) (string, error)

// BearerToken returns the current bearer token.
func (f CredentialSourceFunc) BearerToken(ctx context.Context) (string, error) {
	return f(ctx)
}

// Config contains the shared private source settings. Callers must not log it.
type Config struct {
	BaseURL        string
	Credential     CredentialSource
	HTTPClient     *http.Client
	RequestTimeout time.Duration
}

// Client performs bounded HTTPS GET requests for purpose-built adapters.
type Client struct {
	baseURL        *url.URL
	credential     CredentialSource
	httpClient     *http.Client
	requestTimeout time.Duration
	requestSlots   chan struct{}
}

// New validates and copies shared source configuration.
func New(config Config) (*Client, error) {
	baseURL, err := url.Parse(config.BaseURL)
	if err != nil || baseURL.Scheme != "https" || baseURL.Host == "" ||
		baseURL.User != nil || (baseURL.Path != "" && baseURL.Path != "/") ||
		baseURL.RawQuery != "" || baseURL.Fragment != "" {
		return nil, errors.New("source base URL must be an HTTPS origin")
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
		return nil, errors.New("source request timeout must be from 1ms to 30s")
	}

	return &Client{
		baseURL:        baseURL,
		credential:     config.Credential,
		httpClient:     httpClient,
		requestTimeout: requestTimeout,
		requestSlots:   make(chan struct{}, maxConcurrentRequests),
	}, nil
}

// Get performs one bounded JSON GET. Paths and queries must be compiled by a
// purpose-built adapter and must never come from MCP input.
func (c *Client) Get(ctx context.Context, path string, query url.Values) ([]byte, error) {
	requestContext, cancel := context.WithTimeout(ctx, c.requestTimeout)
	defer cancel()

	select {
	case c.requestSlots <- struct{}{}:
		defer func() { <-c.requestSlots }()
	case <-requestContext.Done():
		return nil, contextError(requestContext.Err())
	}

	requestURL := *c.baseURL
	requestURL.Path = path
	requestURL.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return nil, ErrSourceUnavailable
	}
	request.Header.Set("Accept", "application/json")
	if c.credential != nil {
		token, err := c.credential.BearerToken(requestContext)
		if err != nil || !validBearerToken(token) {
			return nil, ErrCredentialUnavailable
		}
		request.Header.Set("Authorization", "Bearer "+token)
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		if requestContext.Err() != nil {
			return nil, contextError(requestContext.Err())
		}
		return nil, ErrSourceUnavailable
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("%w: status %d", ErrSourceRejected, response.StatusCode)
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return nil, ErrInvalidSourceResponse
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

func contextError(err error) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrSourceTimeout
	}
	return ErrSourceUnavailable
}

func validBearerToken(token string) bool {
	return token != "" && len(token) <= maxBearerTokenBytes &&
		!strings.ContainsAny(token, " \t\r\n")
}
