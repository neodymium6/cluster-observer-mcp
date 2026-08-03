# Cluster Observer MCP

Cluster Observer MCP is a read-only Model Context Protocol server for exposing
small, explicitly designed infrastructure observations to AI assistants.

## Status

Initial implementation decisions are documented, but the server is not yet
implemented. Do not deploy this repository or grant it infrastructure
credentials yet. See
[ADR 0001](docs/adr/0001-initial-implementation.md) for the accepted Go,
official MCP Go SDK, stdio, and initial Kubernetes tool decisions.

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

Direnv users can approve the included `.envrc` with `direnv allow`.

## Public and private configuration

Generic schemas, examples using reserved values, tests, and reusable runtime
code belong here. Real endpoints, cluster identities, credentials, encrypted
Secrets, and deployment overlays belong in a separate private infrastructure
repository.

## License

Licensed under the Apache License 2.0. See [LICENSE](LICENSE).
