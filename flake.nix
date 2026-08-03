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
              just
              markdownlint-cli2
              nixfmt-tree
              pre-commit
            ];
          };
        }
      );

      checks = forAllSystems (
        system:
        let
          pkgs = pkgsFor.${system};
          source = pkgs.lib.fileset.toSource {
            root = ./.;
            fileset = pkgs.lib.fileset.unions [
              ./.github
              ./.markdownlint.yaml
              ./AGENTS.md
              ./DESIGN.md
              ./README.md
              ./SECURITY.md
            ];
          };
        in
        {
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
                markdownlint-cli2 AGENTS.md DESIGN.md README.md SECURITY.md
                touch "$out"
              '';
        }
      );

      formatter = forAllSystems (system: pkgsFor.${system}.nixfmt-tree);
    };
}
