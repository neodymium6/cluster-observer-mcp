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
as Skopeo can copy into a local daemon or registry. Publishing the archive is a
separate release action and is not performed by repository checks.

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
