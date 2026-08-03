# ADR 0004: Add bounded monitoring and Flux observations

- Status: Accepted
- Date: 2026-08-03

## Context

Kubernetes node and controller readiness answers whether the core cluster is
healthy, but it does not cover active monitoring alerts, failed scrape targets,
or failed GitOps reconciliation. Those observations are enough for an MCP
client to identify most operational failures without receiving a generic
query, log, or command interface.

The intended sidecar deployment already gives the observer a projected
Kubernetes ServiceAccount token. Giving the Pod direct network access to
Prometheus or Alertmanager would also give the Hermes container that access,
because Kubernetes NetworkPolicy applies to the whole Pod rather than to an
individual container. Kubernetes supports proxying requests to a named Service
through its API, where RBAC can bound access to the `services/proxy`
subresource.

## Decision

The public tool catalog adds three purpose-built observations:

- `monitoring_list_active_alerts` returns a bounded list of currently active,
  non-silenced, non-inhibited alerts;
- `monitoring_get_scrape_health` returns the state of explicitly configured
  scrape identities from the fixed Prometheus `up` metric; and
- `flux_list_unhealthy_reconciliations` returns bounded non-ready Flux
  Kustomizations, HelmReleases, and GitRepositories.

The existing tools remain unchanged. Document search, arbitrary PromQL,
arbitrary Alertmanager filters, arbitrary Kubernetes resources, logs, and raw
API responses are outside this decision.

### Target model

`monitoring` and `flux` are separate public target kinds. Each target has an
opaque identifier and only the capabilities implemented for that kind:

- `monitoring.active-alerts`;
- `monitoring.scrape-health`; and
- `flux.unhealthy-reconciliations`.

Separate target identifiers avoid implying that a Prometheus installation, an
Alertmanager installation, and a Kubernetes cluster are always one deployment.
They may still share the same Kubernetes API endpoint and rotating credential
in private configuration.

### Monitoring requests

The monitoring adapter accepts a Kubernetes API origin and configured Service
coordinates. It constructs only these request shapes:

```text
GET /api/v1/namespaces/{namespace}/services/{alertmanager}:{port}/proxy/api/v2/alerts
    ?active=true&silenced=false&inhibited=false

GET /api/v1/namespaces/{namespace}/services/{prometheus}:{port}/proxy/api/v1/query
    ?query=up
```

The namespace, Service name, and named port are operator configuration, never
tool input or output. Redirects are rejected. The adapter accepts only JSON,
applies source response, timeout, concurrency, and output-size limits, and
replaces source failures with bounded error categories.

Active alert output contains only the bounded alert name, normalized severity,
configured component identity when available, and start time. Raw labels,
annotations, generator URLs, fingerprints, receiver details, and source text
are omitted. Invalid or oversized alert names are replaced with `unknown`
rather than copied into the result.

The scrape-health query is compiled into the adapter. Private configuration
maps an exact Prometheus `job` and optional `instance` label pair to an opaque
public scrape identity. The tool returns only that identity, a normalized
`up`, `down`, or `missing` state, and the sample time. Raw series labels and
label values are never returned. Missing configured identities are reported as
`missing` so a failed service discovery path does not look healthy. Series that
do not exactly match a configured identity are discarded before duplicate and
sample-value validation. More than one source series matching a configured
identity is ambiguous and fails closed as an invalid source response.

### Flux requests

The Flux adapter maps opaque scopes to Kubernetes namespaces and constructs
only these namespaced list requests with a fixed server-side limit:

```text
GET /apis/kustomize.toolkit.fluxcd.io/v1/namespaces/{namespace}/kustomizations
GET /apis/helm.toolkit.fluxcd.io/v2/namespaces/{namespace}/helmreleases
GET /apis/source.toolkit.fluxcd.io/v1/namespaces/{namespace}/gitrepositories
```

Only objects whose Ready condition is not `True` are returned. Each result
contains the opaque scope, fixed resource kind, bounded Kubernetes object name,
normalized readiness state and reason, and a valid Ready-condition transition
time when present. Condition messages, specifications, URLs, inventory,
revisions, labels, and annotations are omitted.

### Tool schemas

Every input schema rejects unknown properties. Alert and Flux lists default to
20 entries and allow at most 50 entries.

```text
monitoring_list_active_alerts({target, limit?})
monitoring_get_scrape_health({target})
flux_list_unhealthy_reconciliations({target, scope?, limit?})
```

Every output includes the opaque target and observation time. List outputs
include `truncated`. The scrape-health output is bounded by the configured
identity limit and reports whether the source response was partial.

### Public and private boundary

The public repository owns fixed API paths, query shapes, schemas,
normalization, redaction, limits, source adapters, and fake fixtures. Private
operator configuration owns:

- Kubernetes API endpoints, CA bundles, and credential paths;
- Prometheus and Alertmanager namespaces, Service names, and named ports;
- mappings from Prometheus label values to opaque scrape identities;
- mappings from alert names to optional opaque component identities;
- Flux namespaces and opaque scope identifiers; and
- target and capability enablement.

Expected node, storage, guest, or application counts also remain private. The
public core does not encode one operator's topology or alert inventory.

### Authorization and networking

The intended sidecar overlay may grant `get` on the exact named Prometheus and
Alertmanager `services/proxy` subresources and `list` on the selected Flux
resources in selected namespaces. The exact Kubernetes authorization behavior
for named Service ports must be validated with a dry-run or
SubjectAccessReview before deployment.

The private overlay should keep Pod egress limited to DNS and the Kubernetes
API. It should not add direct Prometheus or Alertmanager egress merely for
these tools. No live RBAC, NetworkPolicy, or deployment change is part of the
public implementation.

## Verification

Tests will use deterministic TLS fake servers and hand-written fixtures. They
must verify:

- fixed GET methods, paths, query parameters, and source response limits;
- rejection of caller-controlled paths, PromQL, labels, and URLs by schema;
- projected credential rotation and redacted source errors;
- omission of raw alert labels, annotations, URLs, and messages;
- exact scrape-label-to-opaque-ID mapping, including down and missing states;
- Flux Ready-condition handling and normalized reasons;
- stable ordering, list limits, truncation, and encoded output limits; and
- bounded audit events for all three tools.

No test or repository check connects to a live Kubernetes, Prometheus,
Alertmanager, or Flux API.

## Consequences

Hermes can answer what is unhealthy, when it became unhealthy, which bounded
component is affected, and whether the observation source itself is reporting.
Most Proxmox, Ceph, storage, backup, and application failures can be surfaced
through existing alerts without adding environment-specific implementations to
the public core.

The tools report health evidence rather than complete root-cause diagnostics.
If real incidents show that another detail is necessary, it must be added as a
new purpose-built observation with its own schema and redaction contract.

Service proxy access expands the observer ServiceAccount's permissions. It
does not give Hermes the token, but a compromised Hermes process can invoke
every enabled observer tool over loopback. The response boundary therefore
remains the primary authorization boundary exposed to Hermes.

## Rejected alternatives

- Direct Pod egress to Prometheus and Alertmanager was rejected because
  NetworkPolicy cannot grant that egress only to the sidecar container.
- Generic PromQL and arbitrary HTTP tools were rejected because they can
  expose unbounded or sensitive data and make RBAC ineffective at the MCP
  boundary.
- Returning all Prometheus labels or Alertmanager annotations was rejected
  because they commonly contain endpoints, identifiers, and source-authored
  text.
- Generic Flux object retrieval was rejected because it would expose raw
  specifications and turn the observer into a Kubernetes passthrough.
- Operations-document search was excluded because it is not required for the
  initial health-observation workflow.

## References

- [Kubernetes Service API proxy](https://kubernetes.io/docs/reference/kubernetes-api/core/service-v1/)
- [Kubernetes RBAC](https://kubernetes.io/docs/reference/access-authn-authz/rbac/)
- [Kubernetes NetworkPolicy](https://kubernetes.io/docs/concepts/services-networking/network-policies/)
- [Prometheus HTTP API](https://prometheus.io/docs/prometheus/latest/querying/api/)
- [Alertmanager API](https://github.com/prometheus/alertmanager/blob/main/api/v2/openapi.yaml)
- [Flux custom resources](https://fluxcd.io/flux/components/)
