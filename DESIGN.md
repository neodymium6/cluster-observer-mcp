# Design

Status: bootstrap; implementation decisions pending.

## Objective

Provide AI assistants with a narrow, auditable view of infrastructure state
without granting them infrastructure mutation capabilities or general-purpose
command execution.

## Proposed data flow

```text
MCP client
    |
    v
Cluster Observer MCP
    |
    +-- tool allowlist and input validation
    +-- source-specific read-only adapters
    +-- field redaction and response limits
    |
    v
approved read-only APIs
```

Every MCP tool must map to a purpose-built observer operation. Tool inputs must
select only preconfigured targets and bounded parameters. A caller-supplied
command, URL, query language, resource path, or credential is outside the
design.

## Security properties

- Read-only behavior is enforced by both application logic and source-specific
  least-privilege credentials.
- Unknown tools, targets, fields, and parameters fail closed.
- Responses exclude credentials, tokens, raw configuration blobs, and
  unbounded logs.
- Each request has time, concurrency, and response-size limits.
- Audit events identify the tool and configured target without recording
  secrets or unrestricted response bodies.
- The server does not receive a general cluster-admin credential, host shell,
  container runtime socket, or host filesystem mount.

## Public repository boundary

This repository contains reusable code, schemas, tests, and reserved example
values. A private infrastructure repository owns:

- real target inventories and endpoint addresses;
- runtime credentials and encrypted Secrets;
- deployment manifests and network policy overlays; and
- environment-specific tool enablement.

The public repository must remain useful without embedding a specific home,
organization, cluster, network, or account identity.

## Initial delivery stages

1. Select the implementation language, MCP SDK, and transport.
2. Define the first small set of tool schemas and their redaction contracts.
3. Implement adapters against deterministic fake servers and fixtures.
4. Add runtime hardening, bounded concurrency, and structured audit events.
5. Publish a container image and generic deployment example.
6. Integrate a pinned release from a separate private infrastructure overlay.

## Open decisions

- Implementation language and supported platforms.
- MCP SDK and transport used for the first release.
- The first infrastructure source and exact tool schemas.
- Runtime authentication between the MCP client and server.
- Metrics, health checks, and audit-event schema.
- Release signing and software bill of materials generation.
