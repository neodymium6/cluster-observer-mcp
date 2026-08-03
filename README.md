# Cluster Observer MCP

Cluster Observer MCP is a read-only Model Context Protocol server for exposing
small, explicitly designed infrastructure observations to AI assistants.

## Status

The initial read-only stdio server and deterministic fake Kubernetes adapter
are implemented. The project has no supported release. Do not deploy it or
grant it infrastructure credentials yet. See
[ADR 0001](docs/adr/0001-initial-implementation.md) for the initial
implementation choices and
[ADR 0002](docs/adr/0002-hermes-sidecar-streamable-http.md) for the accepted
Hermes sidecar design.

## Intended boundary

The project will:

- expose an allowlist of structured read-only tools;
- use least-privilege, read-only credentials for each data source;
- redact sensitive fields and bound response sizes;
- keep deployment-specific configuration outside the public repository; and
- fail closed when a tool, field, or target is not explicitly allowed.

The project will not provide generic shell, SSH, `kubectl`, SQL, or arbitrary
HTTP passthrough access. It will not create, update, or delete infrastructure.

See [DESIGN.md](DESIGN.md) for the initial architecture and
[SECURITY.md](SECURITY.md) for the security boundary.

## Development

Enter the pinned development environment:

```bash
nix develop
```

Install Git hooks after cloning:

```bash
just init
```

Run the repository checks:

```bash
just check
```

Build the stdio server:

```bash
go build ./cmd/cluster-observer-mcp
```

The server starts with no targets when `--config` is omitted. A private runtime
configuration can be selected explicitly:

```bash
cluster-observer-mcp --config /path/to/private-config.json
```

See [the reserved example](examples/config.example.json) for the configuration
schema. Real endpoints, namespaces, credential files, and CA bundles belong in
the operator's private infrastructure repository. Source credentials are read
from a separate bounded file and are never accepted in MCP tool input.

The initial tools are:

- `observer_list_targets`;
- `kubernetes_get_cluster_health`; and
- `kubernetes_list_unhealthy_workloads`.

All tools return structured, bounded observations. They do not expose raw
Kubernetes objects, caller-controlled API paths, selectors, or URLs.

The next implementation stage will add a stateless Streamable HTTP endpoint
bound only to `127.0.0.1` for a same-Pod Hermes sidecar integration. It is not
implemented yet; stdio remains the only available transport in the current
binary.

The Go module is published as
`github.com/neodymium6/cluster-observer-mcp`.

Direnv users can approve the included `.envrc` with `direnv allow`.

## Public and private configuration

Generic schemas, examples using reserved values, tests, and reusable runtime
code belong here. Real endpoints, cluster identities, credentials, encrypted
Secrets, and deployment overlays belong in a separate private infrastructure
repository.

## License

Licensed under the Apache License 2.0. See [LICENSE](LICENSE).
