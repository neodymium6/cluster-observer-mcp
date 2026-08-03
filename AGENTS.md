# Project Guidance

This repository is intended for public release. Keep source, tests, examples,
documentation, and history environment-neutral.

## Safety and privacy

- Never commit credentials, private keys, access tokens, real internal domains,
  private IP addresses, cluster names, hardware identifiers, or personal
  contact details.
- Use RFC 5737 addresses, `example.com`, `example.invalid`, and clearly fake
  identifiers in examples and tests.
- Keep real deployment overlays, encrypted Secrets, kubeconfigs, and endpoint
  inventories in the operator's private infrastructure repository.
- Do not deploy this project or connect it to live infrastructure without
  explicit approval for the exact environment and revision.

## Product boundary

- Preserve the read-only observer boundary.
- Do not add generic shell, SSH, `kubectl`, SQL, or arbitrary HTTP passthrough
  tools.
- Expose only explicitly designed, bounded, structured observations.
- Treat returned infrastructure data as sensitive and apply redaction and
  response-size limits before returning it to an MCP client.
- Do not assume an implementation language, MCP SDK, transport, or deployment
  target until the choice is documented.

## Development

- Run `just check` before committing.
- Use Conventional Commit messages.
- Write code, comments, commits, and repository documentation in English.
