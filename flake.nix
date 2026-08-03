{
  description = "Development environment for a read-only cluster observer MCP server";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs =
    { nixpkgs, ... }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "aarch64-darwin"
      ];
      forAllSystems = nixpkgs.lib.genAttrs systems;
      pkgsFor = forAllSystems (system: import nixpkgs { inherit system; });
      source = nixpkgs.lib.fileset.toSource {
        root = ./.;
        fileset = nixpkgs.lib.fileset.unions [
          ./.github
          ./.markdownlint.yaml
          ./AGENTS.md
          ./DESIGN.md
          ./LICENSE
          ./README.md
          ./SECURITY.md
          ./cmd
          ./docs
          ./examples
          ./go.mod
          ./go.sum
          ./internal
        ];
      };
      goPackageFor =
        system:
        let
          pkgs = pkgsFor.${system};
        in
        pkgs.buildGoModule {
          pname = "cluster-observer-mcp";
          version = "0.1.0-dev";
          src = source;
          vendorHash = "sha256-hbV/kOuImCWwmxcOdg9bEM8VLrCcn0m5bF4MMmj9lSs=";
          subPackages = [ "cmd/cluster-observer-mcp" ];
          env.CGO_ENABLED = "0";
          ldflags = [
            "-s"
            "-w"
            "-X main.version=0.1.0-dev"
          ];
          preCheck = "go vet ./...";
        };
    in
    {
      devShells = forAllSystems (
        system:
        let
          pkgs = pkgsFor.${system};
        in
        {
          default = pkgs.mkShell {
            packages = with pkgs; [
              actionlint
              git
              gitleaks
              go
              just
              markdownlint-cli2
              nixfmt-tree
              pre-commit
            ];
          };
        }
      );

      packages = forAllSystems (system: {
        default = goPackageFor system;
      });

      checks = forAllSystems (
        system:
        let
          pkgs = pkgsFor.${system};
        in
        {
          go-build = goPackageFor system;
          repository-metadata =
            pkgs.runCommand "cluster-observer-mcp-repository-metadata"
              {
                nativeBuildInputs = [
                  pkgs.actionlint
                  pkgs.markdownlint-cli2
                ];
              }
              ''
                cd ${source}
                actionlint .github/workflows/ci.yml
                markdownlint-cli2 AGENTS.md DESIGN.md README.md SECURITY.md docs/**/*.md
                touch "$out"
              '';
        }
      );

      formatter = forAllSystems (system: pkgsFor.${system}.nixfmt-tree);
    };
}
