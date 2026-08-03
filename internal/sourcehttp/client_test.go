package sourcehttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestGetUsesBoundedJSONGETAndRotatingCredential(t *testing.T) {
	t.Parallel()

	var authorizations []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/fixed/path" ||
			r.URL.Query().Get("fixed") != "value" {
			t.Fatalf("request = %s %s", r.Method, r.URL.String())
		}
		authorizations = append(authorizations, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(server.Close)

	tokens := []string{"clearly-fake-token-one", "clearly-fake-token-two"}
	next := 0
	client, err := New(Config{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
		Credential: CredentialSourceFunc(func(context.Context) (string, error) {
			token := tokens[next]
			next++
			return token, nil
		}),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	for range tokens {
		if _, err := client.Get(context.Background(), "/fixed/path", url.Values{"fixed": {"value"}}); err != nil {
			t.Fatalf("Get() error = %v", err)
		}
	}
	if len(authorizations) != 2 || authorizations[0] == authorizations[1] {
		t.Fatalf("Authorization headers = %q", authorizations)
	}
}

func TestGetRejectsUnsafeResponsesAndConfiguration(t *testing.T) {
	t.Parallel()

	for _, baseURL := range []string{
		"http://example.invalid",
		"https://example.invalid/path",
		"https://example.invalid?query=value",
	} {
		if _, err := New(Config{BaseURL: baseURL}); err == nil {
			t.Fatalf("New(%q) unexpectedly succeeded", baseURL)
		}
	}

	tests := []struct {
		name    string
		handler http.HandlerFunc
		want    error
	}{
		{
			name: "non JSON",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				_, _ = w.Write([]byte("private source body"))
			},
			want: ErrInvalidSourceResponse,
		},
		{
			name: "rejected",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte("private source body"))
			},
			want: ErrSourceRejected,
		},
		{
			name: "oversized",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(strings.Repeat("x", maxSourceResponseBytes+1)))
			},
			want: ErrInvalidSourceResponse,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewTLSServer(tt.handler)
			t.Cleanup(server.Close)
			client, err := New(Config{BaseURL: server.URL, HTTPClient: server.Client()})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			_, err = client.Get(context.Background(), "/fixed", nil)
			if !errors.Is(err, tt.want) || strings.Contains(err.Error(), "private") ||
				strings.Contains(err.Error(), server.URL) {
				t.Fatalf("Get() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestGetPreservesCancellationAndTimeout(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		w.Header().Set("Content-Type", "application/json")
	}))
	t.Cleanup(server.Close)
	client, err := New(Config{
		BaseURL:        server.URL,
		HTTPClient:     server.Client(),
		RequestTimeout: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := client.Get(context.Background(), "/fixed", nil); !errors.Is(err, ErrSourceTimeout) {
		t.Fatalf("Get(timeout) error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Get(ctx, "/fixed", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("Get(canceled) error = %v", err)
	}
}
