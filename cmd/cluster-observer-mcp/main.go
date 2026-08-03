package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/neodymium6/cluster-observer-mcp/internal/config"
	"github.com/neodymium6/cluster-observer-mcp/internal/httptransport"
	"github.com/neodymium6/cluster-observer-mcp/internal/mcpserver"
)

var version = "dev"

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	os.Exit(run(ctx, os.Args[1:]))
}

func run(ctx context.Context, arguments []string) int {
	if len(arguments) > 0 {
		switch arguments[0] {
		case "serve-http":
			return runHTTP(ctx, arguments[1:])
		case "probe-liveness":
			return runProbe(ctx, arguments[1:], httptransport.ProbeLiveness)
		case "probe-readiness":
			return runProbe(ctx, arguments[1:], httptransport.ProbeReadiness)
		case "probe-startup":
			return runProbe(ctx, arguments[1:], httptransport.ProbeStartup)
		}
	}
	return runStdio(ctx, arguments)
}

func runStdio(ctx context.Context, arguments []string) int {
	flags := flag.NewFlagSet("cluster-observer-mcp", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	configPath := flags.String("config", "", "path to private runtime configuration")
	showVersion := flags.Bool("version", false, "print version and exit")
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "cluster-observer-mcp: positional arguments are not supported")
		return 2
	}
	if *showVersion {
		fmt.Fprintln(os.Stdout, version)
		return 0
	}

	server, err := loadServer(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cluster-observer-mcp: %v\n", err)
		return 1
	}
	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil {
		fmt.Fprintln(os.Stderr, "cluster-observer-mcp: stdio server failed")
		return 1
	}
	return 0
}

func runHTTP(ctx context.Context, arguments []string) int {
	flags := flag.NewFlagSet("cluster-observer-mcp serve-http", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	configPath := flags.String("config", "", "path to private runtime configuration")
	port := flags.Int("port", httptransport.DefaultPort, "IPv4 loopback port")
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	if flags.NArg() != 0 || *configPath == "" || *port < 1 || *port > 65535 {
		fmt.Fprintln(os.Stderr, "cluster-observer-mcp: serve-http requires a config and valid port")
		return 2
	}
	server, err := loadServer(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cluster-observer-mcp: %v\n", err)
		return 1
	}
	listener, err := httptransport.Listen(*port)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cluster-observer-mcp: create HTTP listener failed")
		return 1
	}
	if err := httptransport.Serve(ctx, listener, server); err != nil {
		fmt.Fprintln(os.Stderr, "cluster-observer-mcp: HTTP server failed")
		return 1
	}
	return 0
}

func runProbe(ctx context.Context, arguments []string, kind httptransport.ProbeKind) int {
	flags := flag.NewFlagSet("cluster-observer-mcp probe", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	port := flags.Int("port", httptransport.DefaultPort, "IPv4 loopback port")
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	if flags.NArg() != 0 || *port < 1 || *port > 65535 {
		fmt.Fprintln(os.Stderr, "cluster-observer-mcp: probe requires a valid port")
		return 2
	}
	probeContext, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := httptransport.Probe(probeContext, *port, kind); err != nil {
		fmt.Fprintln(os.Stderr, "cluster-observer-mcp: health probe failed")
		return 1
	}
	return 0
}

func loadServer(configPath string) (*mcp.Server, error) {
	var sources []mcpserver.KubernetesSource
	if configPath != "" {
		clients, err := config.Load(configPath)
		if err != nil {
			return nil, err
		}
		for _, client := range clients {
			sources = append(sources, client)
		}
	}
	server, err := mcpserver.New(version, sources)
	if err != nil {
		return nil, errors.New("initialize server failed")
	}
	return server, nil
}
