// Package httptransport serves MCP only on the IPv4 loopback interface.
package httptransport

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// DefaultPort is the sidecar-local MCP port documented by ADR 0002.
	DefaultPort = 8080

	loopbackAddress       = "127.0.0.1"
	maxMCPRequestBytes    = 64 * 1024
	maxHeaderBytes        = 16 * 1024
	readHeaderTimeout     = 5 * time.Second
	requestTimeout        = 35 * time.Second
	idleTimeout           = 30 * time.Second
	gracefulShutdownLimit = 5 * time.Second
)

var (
	// ErrInvalidPort indicates a port outside the TCP range. Port zero is
	// accepted by Listen for deterministic tests only.
	ErrInvalidPort = errors.New("HTTP port is invalid")
	// ErrNonLoopbackListener prevents accidental Pod-IP or wildcard exposure.
	ErrNonLoopbackListener = errors.New("HTTP listener must use 127.0.0.1")
)

// Listen creates an IPv4 listener with no caller-controlled host component.
func Listen(port int) (net.Listener, error) {
	if port < 0 || port > 65535 {
		return nil, ErrInvalidPort
	}
	return net.Listen("tcp4", net.JoinHostPort(loopbackAddress, strconv.Itoa(port)))
}

// Serve runs a hardened HTTP server and shuts it down when ctx is canceled.
// The listener is validated even when it was not created by Listen.
func Serve(ctx context.Context, listener net.Listener, server *mcp.Server) error {
	if err := validateListener(listener); err != nil {
		return err
	}
	if server == nil {
		return errors.New("MCP server must not be nil")
	}

	httpServer := &http.Server{
		Handler:           NewHandler(server),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       requestTimeout,
		WriteTimeout:      requestTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		ErrorLog:          log.New(io.Discard, "", 0),
	}
	serveResult := make(chan error, 1)
	go func() {
		serveResult <- httpServer.Serve(listener)
	}()

	select {
	case err := <-serveResult:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(
			context.Background(),
			gracefulShutdownLimit,
		)
		defer cancel()
		if err := httpServer.Shutdown(shutdownContext); err != nil {
			_ = httpServer.Close()
			return errors.New("HTTP server shutdown failed")
		}
		err := <-serveResult
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

// NewHandler constructs the fixed HTTP surface. Readiness is true because the
// handler is created only after configuration and MCP tool registration.
func NewHandler(server *mcp.Server) http.Handler {
	streamable := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{
			Stateless:    true,
			JSONResponse: true,
		},
	)

	mcpHandler := http.Handler(streamable)
	mcpHandler = boundedBody(mcpHandler)
	mcpHandler = loopbackHostOnly(mcpHandler)
	mcpHandler = http.NewCrossOriginProtection().Handler(mcpHandler)

	mux := http.NewServeMux()
	mux.Handle("/mcp", mcpHandler)
	mux.Handle("/livez", healthHandler())
	mux.Handle("/readyz", healthHandler())
	return mux
}

func validateListener(listener net.Listener) error {
	if listener == nil {
		return ErrNonLoopbackListener
	}
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok || address.IP == nil || !address.IP.Equal(net.ParseIP(loopbackAddress)) {
		return ErrNonLoopbackListener
	}
	return nil
}

func boundedBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			next.ServeHTTP(w, request)
			return
		}
		if request.ContentLength > maxMCPRequestBytes {
			http.Error(w, "request body is too large", http.StatusRequestEntityTooLarge)
			return
		}
		body, err := io.ReadAll(io.LimitReader(request.Body, maxMCPRequestBytes+1))
		if err != nil {
			http.Error(w, "request body is invalid", http.StatusBadRequest)
			return
		}
		_ = request.Body.Close()
		if len(body) > maxMCPRequestBytes {
			http.Error(w, "request body is too large", http.StatusRequestEntityTooLarge)
			return
		}
		request.Body = io.NopCloser(bytes.NewReader(body))
		next.ServeHTTP(w, request)
	})
}

func loopbackHostOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if !validLoopbackHost(request.Host) {
			http.Error(w, "forbidden Host header", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, request)
	})
}

func validLoopbackHost(hostPort string) bool {
	host := hostPort
	if splitHost, _, err := net.SplitHostPort(hostPort); err == nil {
		host = splitHost
	} else if strings.Contains(hostPort, ":") {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func healthHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.WriteString(w, "ok\n")
	})
}
