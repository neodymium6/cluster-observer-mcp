# ADR 0002: Connect Hermes through a sidecar HTTP transport

- Status: Accepted
- Date: 2026-08-03

## Context

The stdio transport is suitable when an MCP client launches the observer as a
child process. It cannot be used directly by Hermes when the observer runs in a
different container. Running the observer as a separate Pod and Kubernetes
Service would require a remote authentication scheme, TLS termination, service
discovery, and a larger network-policy surface.

Hermes supports MCP servers configured with a URL over HTTP and Streamable
HTTP. Containers in one Kubernetes Pod share a network namespace and can
communicate over loopback. A sidecar can therefore provide an HTTP MCP endpoint
without making that endpoint reachable through the Pod IP or a Service.

The observer needs Kubernetes credentials, while the Hermes process must not
receive them. A compromised Hermes process may invoke the bounded observer
tools, but it must not be able to use the underlying Kubernetes API credential
directly.

## Decision

Hermes and Cluster Observer MCP will run as containers in the same Pod:

```text
Hermes Pod
├── hermes-agent
│   ├── no Kubernetes credential mount
│   └── MCP URL: http://127.0.0.1:8080/mcp
│
└── cluster-observer-mcp
    ├── Streamable HTTP on 127.0.0.1:8080 only
    ├── projected observer ServiceAccount token
    ├── Kubernetes CA bundle and private target configuration
    └── purpose-built get/list observations only
```

The private infrastructure repository will own the Pod overlay, ServiceAccount,
RBAC bindings, token projection, target configuration, and Hermes MCP
configuration. This public repository will own the reusable transport,
health-check command, audit schema, tests, and generic examples with reserved
values.

## MCP transport

The server will retain stdio for local integrations and add one explicit
Streamable HTTP mode with these properties:

- bind to the IPv4 loopback address `127.0.0.1` only;
- use the single endpoint `/mcp`;
- use the official MCP Go SDK with stateless sessions;
- return JSON responses because the tools are synchronous and do not use
  server-initiated requests, notifications, sampling, or stream resumption;
- keep the SDK's localhost and Host-header protection enabled;
- wrap the MCP handler in Go cross-origin protection;
- reject request bodies larger than 64 KiB before MCP decoding;
- set explicit header, request, idle, and graceful-shutdown timeouts; and
- reject configuration that attempts to bind a wildcard or non-loopback
  address.

The initial endpoint does not use TLS or application-level authentication.
Loopback traffic stays inside the Pod network namespace, and Hermes is the
intended caller. Adding a Service, Pod-IP listener, ingress path, or caller
outside the Pod requires a new decision covering authentication, authorization,
TLS, and network policy.

Hermes will configure the observer with a URL and an explicit include-list of
the three observer tools. It will use `127.0.0.1`, not `localhost`, so name
resolution cannot select an address on which the server is not listening.

## Credential isolation

The Pod will set `automountServiceAccountToken: false`. The private overlay will
define a projected, short-lived ServiceAccount token volume and mount it only
in the observer container. The Kubernetes CA bundle and private observer
configuration will also be mounted only in that container.

The Hermes container will not mount:

- the ServiceAccount token;
- the Kubernetes CA bundle;
- the observer target configuration; or
- a kubeconfig.

Projected tokens rotate. The observer must therefore read credentials through
a bounded, rotation-aware credential source when it creates each Kubernetes
request. Holding the token value loaded at process startup is not acceptable.
Credential contents and paths must never appear in MCP errors or audit logs.

Both containers use the Pod's ServiceAccount identity at the Kubernetes object
level, but only the observer receives a usable token. The Pod must not enable a
shared process namespace, and neither container should receive ptrace or other
capabilities that weaken container isolation.

## Kubernetes authorization

RBAC will be generated in the private overlay for the exact configured scopes.
The upper bound is `get` and `list`; the initial adapter should receive only the
verbs it currently uses.

The current Kubernetes adapter requires:

- `list` on `nodes` when cluster node health is enabled;
- `list` on `deployments` in each configured namespace;
- `list` on `statefulsets` in each configured namespace; and
- `list` on `daemonsets` in each configured namespace.

It does not require access to Secrets, ConfigMaps, Pods, logs, events, exec,
port-forwarding, mutation verbs, or arbitrary API resources. Node access is
cluster-scoped and should be omitted when node health is disabled. Workload
permissions should use namespace Roles and RoleBindings wherever possible.

## Health checks

Kubernetes HTTP and TCP probes normally connect to the Pod IP, which cannot
reach a listener bound only to loopback. The OCI image will therefore use the
same observer binary as a bounded exec probe client. It will provide fixed
commands that contact only its loopback health endpoints:

- liveness reports whether the HTTP process can serve requests;
- readiness reports whether configuration was loaded and handlers were
  registered; and
- startup prevents liveness and readiness checks until initialization is
  complete.

The probe command will not accept an arbitrary URL. Source API availability is
reported by tool calls and audit events; a temporary Kubernetes API failure
must not cause a liveness restart loop.

## Audit events

Every tool call will emit one structured JSON event to stderr after completion.
The stable initial fields are:

- timestamp;
- event schema version;
- tool name;
- opaque target identifier when validation succeeded;
- opaque scope identifier when present;
- duration in milliseconds;
- outcome category;
- returned item count where applicable; and
- whether the result was partial or truncated.

Audit events will not contain credentials, endpoints, namespaces, unrestricted
arguments, request or response bodies, Kubernetes object fields, or raw source
errors. HTTP access logs will not record headers or bodies. Error categories
will remain bounded and safe for an MCP client.

## Network implications

All containers in a Pod can reach the loopback MCP endpoint. Loopback is the
communication mechanism, not an authorization boundary between same-Pod
containers. Admission control must prevent unapproved sidecars and ephemeral
containers from being added to this Pod.

Kubernetes NetworkPolicy applies to Pods rather than individual containers. It
can restrict the Pod's external ingress and egress, but it cannot separate
Hermes-to-observer loopback traffic or give the two containers different
network policies. Credential mount isolation and bounded MCP tools provide the
important privilege separation.

No Kubernetes Service or ingress object will select the MCP port. The generic
deployment example will omit both.

## Verification

The implementation stage must add tests for:

- rejection of wildcard and non-loopback listeners;
- a full MCP client exchange over Streamable HTTP;
- Hermes-compatible URL configuration and tool discovery;
- rejection of oversized request bodies and invalid methods;
- Host and cross-origin protection;
- graceful shutdown and timeout behavior;
- liveness, readiness, and startup probe commands;
- credential rotation without restarting the process;
- audit success, bounded errors, cancellation, and truncation; and
- absence of credentials, endpoints, namespaces, and source bodies in logs.

Tests will use loopback listeners, deterministic fake Kubernetes APIs, and
clearly fake credentials. They will not connect to a live cluster or start a
deployment.

## Consequences

Hermes can use the observer through its native HTTP MCP client without gaining
direct access to Kubernetes credentials. The observer remains the only process
that translates the credential into fixed, redacted observations.

The same-Pod design avoids remote MCP authentication and TLS for the first
deployment. It also couples the observer lifecycle to Hermes and deliberately
allows Hermes to invoke all tools enabled for that Pod.

The HTTP transport increases the runtime attack surface compared with stdio.
Loopback binding, stateless sessions, strict HTTP limits, Host and origin
checks, tool schemas, audit events, and credential isolation are required
parts of the design rather than optional deployment hardening.

## Rejected alternatives

- A separate observer Pod and Service was rejected for the initial deployment
  because it requires remote identity, TLS, service authorization, and broader
  network configuration.
- Mounting a kubeconfig or ServiceAccount token in the Hermes container was
  rejected because it bypasses the purpose-built MCP tool boundary.
- Binding the MCP endpoint to the Pod IP or `0.0.0.0` was rejected because it
  makes the endpoint reachable outside the intended same-Pod trust domain.
- A static bearer token loaded once at startup was rejected because projected
  ServiceAccount tokens expire and rotate.
- Generic HTTP, Kubernetes, shell, or command passthrough was rejected because
  it would collapse the observer boundary.

## References

- [Hermes MCP configuration](https://github.com/NousResearch/hermes-agent/blob/main/website/docs/reference/mcp-config-reference.md)
- [MCP Streamable HTTP transport](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports)
- [Kubernetes ServiceAccounts](https://kubernetes.io/docs/concepts/security/service-accounts/)
- [Kubernetes projected volumes](https://kubernetes.io/docs/concepts/storage/projected-volumes/)
- [Kubernetes probes](https://kubernetes.io/docs/concepts/workloads/pods/probes/)
- [Kubernetes networking model](https://kubernetes.io/docs/concepts/services-networking/)
