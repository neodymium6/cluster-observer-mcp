# ADR 0003: Publish immutable prerelease images from version tags

- Status: Accepted
- Date: 2026-08-03

## Context

The Hermes sidecar needs a reproducible OCI image that a private deployment
overlay can select without copying source or relying on a mutable tag. The
public repository must publish artifacts without adding deployment credentials,
environment-specific configuration, or access to live infrastructure.

A release also needs enough supply-chain evidence to identify its source and
contents. Publishing from a developer workstation would make permissions,
platform coverage, and evidence inconsistent.

## Decision

The repository publishes prereleases from Git tags matching `v*`. A release tag
must exactly equal `v` followed by the contents of `VERSION`. Release tags are
immutable and point to a commit already checked by the normal branch CI.

The tag workflow runs `just ci`, then builds the Linux amd64 and arm64 images
natively with the locked Nix inputs. It publishes architecture-specific tags
and combines them into one OCI manifest list at:

```text
ghcr.io/neodymium6/cluster-observer-mcp:<version>
```

The workflow does not publish `latest`. Deployments must select the manifest
list by digest, not by tag. The version tag is a human-readable discovery aid;
the digest is the deployment identity.

Each architecture gets an SPDX JSON SBOM generated from its final image
archive. The SBOM is attached to the GitHub prerelease and signed as a GitHub
artifact attestation against the architecture manifest. The final multi-platform
manifest receives a signed build-provenance attestation. GitHub-hosted OIDC and
Sigstore provide the signing identity; the project does not keep a private
signing key.

The release workflow has no infrastructure credential and cannot deploy. Its
write permissions are limited to GitHub Releases, GHCR, and attestations. Action
dependencies are pinned by full commit SHA. The repository's normal CI remains
read-only.

The first complete prerelease is `v0.1.0-alpha.3`. Alpha releases are suitable
for integration testing of the bounded observer interface, not for an
unattended production rollout. The earlier `v0.1.0-alpha.1` tag published no
artifacts. The `v0.1.0-alpha.2` attempt published only architecture staging
images, not a release manifest or GitHub Release. Both tags remain immutable as
records of the failed release attempts.

## Publication procedure

1. Update `VERSION` and release-facing documentation in a reviewed commit.
2. Run `just check` and push the commit to `main`.
3. Confirm branch CI succeeds.
4. Create and push the matching annotated Git tag.
5. Confirm the tag workflow publishes both platforms, attestations, and the
   GitHub prerelease.
6. Confirm the GHCR package is public and anonymously readable.
7. Copy the reported manifest digest into a private, declarative deployment
   overlay. Do not use a mutable tag there.

Publishing a Git tag is an explicit release action. Deleting or moving a
release tag is not an accepted correction mechanism; a correction receives a
new version.

## Verification

The workflow verifies that the final manifest contains exactly `linux/amd64`
and `linux/arm64`. It records the immutable image reference in the workflow
summary and attaches the same reference to the GitHub prerelease.

Consumers can verify provenance with the GitHub CLI and the repository owner:

```bash
gh attestation verify \
  oci://ghcr.io/neodymium6/cluster-observer-mcp@sha256:<digest> \
  --repo neodymium6/cluster-observer-mcp
```

The placeholder is documentation only. Private overlays must use the complete
digest emitted by a successful release workflow.

## Consequences

Releases are reproducible from a public commit and do not depend on a local
container daemon. Native builds avoid emulation differences, at the cost of
using two GitHub-hosted runner architectures.

Architecture tags remain available for troubleshooting but are not deployment
references. SBOMs describe each platform separately. The final manifest's
provenance establishes the workflow and source identity shared by both.

The initial package may require a one-time GitHub visibility change after its
first publication. That action makes the package public and must be followed by
an anonymous pull check before a private overlay is allowed to reference it.

## Rejected alternatives

- Publishing only amd64 was rejected because the sidecar should not constrain
  the Pod's node architecture.
- Publishing from a workstation was rejected because it weakens provenance and
  requires a long-lived registry credential.
- Deploying by a mutable version tag or `latest` was rejected because the
  selected bytes could change without a declarative overlay change.
- Storing a Cosign private key in repository secrets was rejected because
  GitHub's OIDC-backed attestations provide a keyless source identity.
- Building foreign architectures through emulation was rejected for the first
  release because native hosted runners are available.
