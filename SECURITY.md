# Security Policy

## Supported versions

The project is in its bootstrap phase and has no supported release. Do not use
it with production or sensitive infrastructure.

## Security boundary

Cluster Observer MCP is intended to expose only purpose-built read-only
observations. Generic command execution, arbitrary API forwarding, and
infrastructure mutation are out of scope.

Runtime deployments must use source-specific read-only credentials, restrict
network access to approved endpoints, and keep credentials outside this
repository. The application must redact sensitive fields and enforce bounded
inputs, timeouts, concurrency, and response sizes.

Tool-call audit events use a bounded schema and must not contain raw arguments,
responses, errors, endpoints, namespaces, or credentials. See
[the audit event schema](docs/audit-events.md).

## Reporting a vulnerability

Use the private security-advisory feature of the repository hosting this
project. Do not include credentials, private infrastructure output, or personal
data in a public issue.
