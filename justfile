set shell := ["bash", "-euo", "pipefail", "-c"]

export CGO_ENABLED := "0"

# Show available recipes.
default:
  @just --list

# Initialize Git when needed and install repository hooks.
init:
  if [ ! -d .git ]; then git init -b main; fi
  pre-commit install --install-hooks

# Format repository-owned files.
fmt:
  gofmt -w cmd internal
  nix fmt -- flake.nix

# Run all checks available for the current bootstrap stage.
check:
  pre-commit run --all-files
  test -z "$(gofmt -l cmd internal)"
  go test ./...
  CGO_ENABLED=1 go test -race ./...
  go vet ./...
  nix flake check .
  nix flake check --no-build --all-systems .

# CI alias.
ci: check

# Fuzz the public normalization boundary for a short local session.
fuzz:
  go test -fuzz=Fuzz -fuzztime=10s ./internal/observer

# Update pinned development-environment inputs.
update:
  nix flake update
