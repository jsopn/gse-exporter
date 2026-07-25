{
  description = "Prometheus exporter for the Georgian State Electrosystem grid API";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs =
    { self, nixpkgs }:
    let
      version = "0.1.0";
      systems = [
        "aarch64-darwin"
        "x86_64-darwin"
        "aarch64-linux"
        "x86_64-linux"
      ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
    in
    {
      packages = forAllSystems (pkgs: rec {
        default = gse-exporter;
        gse-exporter = pkgs.buildGoModule {
          pname = "gse-exporter";
          inherit version;
          src = ./.;
          vendorHash = "sha256-REEJCTpSx2Kt5J93n6vS3nnpl+StPx26nBCzOgV/PDw=";
          ldflags = [
            "-s"
            "-w"
            "-X"
            "main.version=${version}"
          ];
          meta = {
            description = "Prometheus exporter for the Georgian State Electrosystem grid API";
            homepage = "https://github.com/jsopn/gse-exporter";
            mainProgram = "gse-exporter";
          };
        };
      });

      apps = forAllSystems (pkgs: {
        default = {
          type = "app";
          program = "${self.packages.${pkgs.system}.gse-exporter}/bin/gse-exporter";
        };
      });

      devShells = forAllSystems (pkgs: rec {
        ci = pkgs.mkShell {
          packages = [
            pkgs.go
            pkgs.just
            pkgs.kubernetes-helm
          ];
        };
        default = ci.overrideAttrs (old: {
          nativeBuildInputs = old.nativeBuildInputs ++ [
            pkgs.gopls
            pkgs.gotools
            pkgs.kubectl
          ];
        });
      });

      formatter = forAllSystems (pkgs: pkgs.nixfmt-tree);
    };
}
