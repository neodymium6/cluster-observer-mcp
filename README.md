# Cluster Observer MCP

Cluster Observer MCP is a read-only Model Context Protocol server for exposing
small, explicitly designed infrastructure observations to AI assistants.

## Status

The initial read-only server, deterministic fake Kubernetes adapter, and
loopback-only Streamable HTTP sidecar transport are implemented. The first
alpha release is intended for integration testing, not unattended production
use. Do not deploy it or grant it infrastructure credentials without an
explicit review of the private overlay. See
[ADR 0001](docs/adr/0001-initial-implementation.md) for the initial
implementation choices and
[ADR 0002](docs/adr/0002-hermes-sidecar-streamable-http.md) for the accepted
Hermes sidecar design and
[ADR 0003](docs/adr/0003-release-policy.md) for the release policy.

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

Build the server:

```bash
go build ./cmd/cluster-observer-mcp
```

On Linux, build the minimal non-root container archive with:

```bash
nix build .#oci-image
```

See [the container image notes](docs/container-image.md) for its contents and
runtime boundary. Repository checks build but never publish this archive.

The server starts with no targets when `--config` is omitted. A private runtime
configuration can be selected explicitly:

```bash
cluster-observer-mcp --config /path/to/private-config.json
```

For the same-Pod Hermes sidecar mode, start the stateless Streamable HTTP
transport explicitly:

```bash
cluster-observer-mcp serve-http \
  --config /path/to/private-config.json \
  --port 8080
```

The listener is always `127.0.0.1`; no flag can select a wildcard, Pod IP, or
hostname. Hermes should use `http://127.0.0.1:8080/mcp` and an explicit
include-list containing only the three tools below. The endpoint uses same-Pod
loopback without TLS or application authentication and must not be published
through a Service or ingress.

Kubernetes exec probes use fixed commands that cannot accept a URL or path:

```bash
cluster-observer-mcp probe-startup --port 8080
cluster-observer-mcp probe-liveness --port 8080
cluster-observer-mcp probe-readiness --port 8080
```

See [the reserved example](examples/config.example.json) for the configuration
schema. Real endpoints, namespaces, credential files, and CA bundles belong in
the operator's private infrastructure repository. Source credentials are read
from a separate bounded file immediately before each source request so
projected token rotation does not require a restart. They are never accepted
in MCP tool input.

The initial tools are:

- `observer_list_targets`;
- `kubernetes_get_cluster_health`; and
- `kubernetes_list_unhealthy_workloads`.

All tools return structured, bounded observations. They do not expose raw
Kubernetes objects, caller-controlled API paths, selectors, or URLs.

Both stdio and Streamable HTTP expose the same transport-independent,
purpose-built tool catalog. Each tool call emits a bounded JSON audit event to
stderr; see [the versioned audit schema](docs/audit-events.md).

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
