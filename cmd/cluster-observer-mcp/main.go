package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/neodymium6/cluster-observer-mcp/internal/config"
	"github.com/neodymium6/cluster-observer-mcp/internal/mcpserver"
)

var version = "dev"

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	os.Exit(run(ctx, os.Args[1:]))
}

func run(ctx context.Context, arguments []string) int {
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

	var sources []mcpserver.KubernetesSource
	if *configPath != "" {
		clients, err := config.Load(*configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cluster-observer-mcp: %v\n", err)
			return 1
		}
		for _, client := range clients {
			sources = append(sources, client)
		}
	}

	server, err := mcpserver.New(version, sources)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cluster-observer-mcp: initialize server failed")
		return 1
	}
	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil {
		fmt.Fprintln(os.Stderr, "cluster-observer-mcp: stdio server failed")
		return 1
	}
	return 0
}
