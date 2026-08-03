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
      version = nixpkgs.lib.removeSuffix "\n" (builtins.readFile ./VERSION);
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
          ./VERSION
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
          inherit version;
          src = source;
          vendorHash = "sha256-hbV/kOuImCWwmxcOdg9bEM8VLrCcn0m5bF4MMmj9lSs=";
          subPackages = [ "cmd/cluster-observer-mcp" ];
          env.CGO_ENABLED = "0";
          ldflags = [
            "-s"
            "-w"
            "-X main.version=${version}"
          ];
          preCheck = "go vet ./...";
        };
      containerBinaryFor =
        system:
        let
          pkgs = pkgsFor.${system};
        in
        pkgs.runCommand "cluster-observer-mcp-container-binary"
          {
            nativeBuildInputs = [ pkgs.removeReferencesTo ];
          }
          ''
            install -Dm755 \
              ${goPackageFor system}/bin/cluster-observer-mcp \
              "$out/bin/cluster-observer-mcp"
            # The Nix Go toolchain records optional fallback data paths. This
            # server uses numeric ports, explicit content types, and UTC audit
            # timestamps, so the scratch image does not need those closures.
            remove-references-to \
              -t ${pkgs.mailcap} \
              -t ${pkgs.iana-etc} \
              -t ${pkgs.tzdata} \
              "$out/bin/cluster-observer-mcp"
          '';
      ociImageFor =
        system:
        let
          pkgs = pkgsFor.${system};
        in
        pkgs.dockerTools.buildLayeredImage {
          name = "cluster-observer-mcp";
          tag = version;
          created = "1970-01-01T00:00:01Z";
          contents = [ ];
          extraCommands = ''
            mkdir -p bin etc/ssl/certs
            cp ${containerBinaryFor system}/bin/cluster-observer-mcp \
              bin/cluster-observer-mcp
            cp ${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt \
              etc/ssl/certs/ca-bundle.crt
            chmod 0555 bin/cluster-observer-mcp
            chmod 0444 etc/ssl/certs/ca-bundle.crt
          '';
          config = {
            Entrypoint = [ "/bin/cluster-observer-mcp" ];
            User = "65532:65532";
            Env = [ "SSL_CERT_FILE=/etc/ssl/certs/ca-bundle.crt" ];
            Labels = {
              "org.opencontainers.image.title" = "Cluster Observer MCP";
              "org.opencontainers.image.description" =
                "Read-only MCP server for bounded infrastructure observations";
              "org.opencontainers.image.source" = "https://github.com/neodymium6/cluster-observer-mcp";
              "org.opencontainers.image.licenses" = "Apache-2.0";
              "org.opencontainers.image.version" = version;
            };
          };
        };
      ociImageCheckFor =
        system:
        let
          pkgs = pkgsFor.${system};
          image = ociImageFor system;
        in
        pkgs.runCommand "cluster-observer-mcp-oci-image-check"
          {
            nativeBuildInputs = [ pkgs.jq ];
          }
          ''
            image_root="$TMPDIR/image"
            rootfs="$TMPDIR/rootfs"
            mkdir -p "$image_root" "$rootfs"
            tar -xzf ${image} -C "$image_root"

            config_name=$(jq -r '.[0].Config' "$image_root/manifest.json")
            layer_name=$(jq -r '.[0].Layers[0]' "$image_root/manifest.json")
            test "$(jq '.[0].Layers | length' "$image_root/manifest.json")" -eq 1

            jq -e '
              .architecture == "${pkgs.go.GOARCH}" and
              .os == "linux" and
              .config.Entrypoint == ["/bin/cluster-observer-mcp"] and
              .config.User == "65532:65532" and
              .config.Env == ["SSL_CERT_FILE=/etc/ssl/certs/ca-bundle.crt"] and
              .config.Cmd == null and
              .config.ExposedPorts == null and
              .config.Labels["org.opencontainers.image.licenses"] == "Apache-2.0" and
              .config.Labels["org.opencontainers.image.source"] ==
                "https://github.com/neodymium6/cluster-observer-mcp"
            ' "$image_root/$config_name"

            tar -xf "$image_root/$layer_name" -C "$rootfs"
            test "$(find "$rootfs" -type f | wc -l)" -eq 2
            test -x "$rootfs/bin/cluster-observer-mcp"
            test -r "$rootfs/etc/ssl/certs/ca-bundle.crt"
            test ! -e "$rootfs/bin/sh"
            test ! -e "$rootfs/bin/bash"
            test "$($rootfs/bin/cluster-observer-mcp --version)" = "${version}"
            touch "$out"
          '';
    in
    {
      devShells = forAllSystems (
        system:
        let
          pkgs = pkgsFor.${system};
        in
        {
          default = pkgs.mkShell {
            packages =
              with pkgs;
              [
                actionlint
                git
                gh
                gitleaks
                go
                jq
                just
                markdownlint-cli2
                nixfmt-tree
                pre-commit
              ]
              ++ lib.optionals stdenv.isLinux [
                skopeo
                syft
              ];
          };
        }
      );

      packages = forAllSystems (
        system:
        let
          pkgs = pkgsFor.${system};
        in
        {
          default = goPackageFor system;
        }
        // pkgs.lib.optionalAttrs pkgs.stdenv.isLinux {
          oci-image = ociImageFor system;
        }
      );

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
                actionlint .github/workflows/*.yml
                markdownlint-cli2 AGENTS.md DESIGN.md README.md SECURITY.md docs/**/*.md
                touch "$out"
              '';
        }
        // pkgs.lib.optionalAttrs pkgs.stdenv.isLinux {
          oci-image = ociImageCheckFor system;
        }
      );

      formatter = forAllSystems (system: pkgsFor.${system}.nixfmt-tree);
    };
}
