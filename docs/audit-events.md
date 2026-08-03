# Audit events

Cluster Observer MCP writes one compact JSON event to stderr after every MCP
tool call. Tool discovery and health probes do not produce tool-call events.
Schema version 1 is transport-independent and is used by both stdio and
Streamable HTTP modes.

An example successful event is:

```json
{"timestamp":"2026-08-03T12:00:00Z","schemaVersion":1,"tool":"kubernetes_list_unhealthy_workloads","target":"cluster-a","scope":"system","durationMs":12,"outcome":"success","itemCount":2,"truncated":false}
```

Every event contains:

- `timestamp`: completion time in UTC;
- `schemaVersion`: currently `1`;
- `tool`: one of the six allowlisted tool names, or `unknown` for an
  unsupported tool name;
- `durationMs`: non-negative elapsed milliseconds; and
- `outcome`: one bounded category listed below.

The validated opaque `target` and `scope` identifiers are included when they
apply. They are omitted for input-validation failures and unsupported tool
names. Result metadata is tool-specific:

- `observer_list_targets` includes `itemCount`;
- `kubernetes_get_cluster_health` includes `partial`;
- `kubernetes_list_unhealthy_workloads` includes `itemCount` and `truncated`;
- `monitoring_list_active_alerts` includes `itemCount` and `truncated`;
- `monitoring_get_scrape_health` includes `itemCount` and `partial`; and
- `flux_list_unhealthy_reconciliations` includes `itemCount` and `truncated`.

Version 1 outcomes are:

- `success`;
- `validation_error`;
- `target_not_configured`;
- `scope_not_configured`;
- `source_timeout`;
- `source_unavailable`;
- `source_rejected`;
- `credential_unavailable`;
- `invalid_source_response`;
- `result_too_large`;
- `canceled`;
- `unsupported_tool`; and
- `internal_error`.

Events never contain credentials, credential paths, endpoints, Kubernetes
namespaces, unrestricted arguments, request or response bodies, source object
fields, Prometheus labels, Alertmanager annotations, raw tool names, or raw
error text. The `unknown` value prevents an unsupported caller-controlled tool
name from becoming log content.
