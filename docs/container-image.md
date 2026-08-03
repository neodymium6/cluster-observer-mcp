# Container image

The flake builds a minimal Linux OCI-compatible image archive from the same
static Go package used by the default build. The image contains only:

- `/bin/cluster-observer-mcp`; and
- the public CA certificate bundle used for HTTPS source verification.

It has no shell, package manager, runtime configuration, credential, deployment
overlay, or environment-specific address. The runtime identity is the numeric
non-root user and group `65532:65532`.

Build the archive on Linux with:

```bash
nix build .#oci-image
```

The resulting `result` symlink points to a Docker archive that OCI tooling such
as Skopeo can copy into a local daemon or registry. Repository checks never
publish it. The tag-gated release workflow publishes native Linux amd64 and
arm64 images, per-platform SPDX SBOMs, and signed attestations according to
[ADR 0003](adr/0003-release-policy.md).

Released deployments must use the immutable manifest digest reported by the
GitHub prerelease:

```text
ghcr.io/neodymium6/cluster-observer-mcp@sha256:<digest>
```

The repository does not publish `latest`.

The entrypoint is `/bin/cluster-observer-mcp`. A private deployment overlay
must provide the mode, private configuration path, and projected volumes. For
the accepted Hermes sidecar design, the arguments are equivalent to:

```text
serve-http --config /path/to/private-config.json --port 8080
```

The public image deliberately defines no exposed port, default private path,
Kubernetes object, Service, or ingress. The private overlay should use a
read-only root filesystem, drop all Linux capabilities, prevent privilege
escalation, and configure the fixed exec probes documented in the
[README](../README.md).
