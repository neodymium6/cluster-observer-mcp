set shell := ["bash", "-euo", "pipefail", "-c"]

# Show available recipes.
default:
  @just --list

# Initialize Git when needed and install repository hooks.
init:
  if [ ! -d .git ]; then git init -b main; fi
  pre-commit install --install-hooks

# Format repository-owned files.
fmt:
  nix fmt -- flake.nix

# Run all checks available for the current bootstrap stage.
check:
  pre-commit run --all-files
  nix flake check .
  nix flake check --no-build --all-systems .

# CI alias.
ci: check

# Update pinned development-environment inputs.
update:
  nix flake update
