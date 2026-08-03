# ADR 0001: Initial implementation choices

- Status: Accepted
- Date: 2026-08-03

## Context

Cluster Observer MCP needs a small, auditable implementation that can be
distributed without a language runtime and tested without infrastructure
credentials. The first release must preserve the read-only boundary before a
remote transport or deployment is introduced.

## Decision

The server will be implemented in Go using the official
`github.com/modelcontextprotocol/go-sdk` SDK. The initial release will support
the MCP stdio transport only. Tool handlers and observation services will not
depend on the transport so that stateless Streamable HTTP can be evaluated
later without changing their contracts.

The implementation will use a stable MCP SDK release and a finalized protocol
revision. Pre-release SDKs and draft protocol revisions are out of scope.

Kubernetes is the first observation source. The initial tools are:

- `observer_list_targets`, which lists opaque configured target identifiers,
  target kinds, and capabilities without returning endpoints;
- `kubernetes_get_cluster_health`, which returns bounded node and workload
  health totals; and
- `kubernetes_list_unhealthy_workloads`, which returns a bounded list of
  unhealthy workloads from an allowed scope.

All input schemas reject unknown properties. Target and scope identifiers must
resolve through local configuration; they cannot be URLs, API paths, or source
queries. Tool results use structured content, have a 64 KiB encoded-size limit,
and report whether a list was truncated. Workload lists default to 20 entries
and permit at most 50 entries.

The initial tools are annotated as read-only, non-destructive, idempotent, and
closed-world. They do not return raw API responses, manifests, labels,
annotations, environment variables, Secrets, or unrestricted condition
messages.

Tests will use hand-written fixtures and deterministic fake HTTP servers. No
test may require a live API, kubeconfig, credential, or environment-specific
endpoint. Adapter tests must assert that source requests use an allowlisted GET
method and fixed resource paths.

## Consequences

Go provides a single deployable binary, explicit cancellation and timeout
handling, native fuzz and race testing, and deterministic HTTP test servers.
The official Go MCP SDK avoids maintaining a local protocol implementation.

Stdio keeps the first delivery independent of unresolved remote authentication
and network policy decisions. A deployed remote server will require a separate
decision covering authentication, TLS termination, origin and host validation,
rate limits, and deployment topology before Streamable HTTP is enabled.

Source-specific tool names deliberately avoid a generic infrastructure query
surface. Adding another source requires purpose-built tools, redaction rules,
fixtures, and tests rather than extending an arbitrary passthrough operation.

## Rejected alternatives

- TypeScript has a mature MCP SDK, but adds a runtime and a larger dependency
  surface for a small observer binary.
- Rust provides strong compile-time guarantees, but its official MCP SDK has a
  lower support tier than the Go SDK at the time of this decision.
- Starting with Streamable HTTP would require settling authentication and
  deployment security before the observation boundary has been tested.
- A source-neutral query tool would make it easier to expose caller-controlled
  resource paths or filters and would weaken the purpose-built tool boundary.
