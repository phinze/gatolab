{
  description = "Belowdeck - modular Stream Deck Plus daemon";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs =
    {
      self,
      nixpkgs,
      flake-utils,
    }:
    flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
        belowdeck = pkgs.buildGoModule {
          pname = "belowdeck";
          version = "0.1.0-${builtins.substring 0 12 (self.lastModifiedDate or "19700101000000")}";

          src = builtins.path {
            path = ./.;
            name = "belowdeck-source";
          };

          vendorHash = "sha256-XfH/h6y2oSJm/+x/Bjiu0Aq/azmELXeCK6qcOQWrbhw=";

          # mac-sleep-notifier and usbhid use cgo
          env.CGO_ENABLED = "1";

          # Only build the main daemon binary
          subPackages = [ "cmd/belowdeck" ];

          meta = with pkgs.lib; {
            description = "Modular Stream Deck Plus daemon";
            homepage = "https://github.com/phinze/belowdeck";
            mainProgram = "belowdeck";
          };
        };
      in
      {
        packages = {
          default = belowdeck;
          belowdeck = belowdeck;
        };

        apps.default = flake-utils.lib.mkApp {
          drv = belowdeck;
        };

        devShells.default = pkgs.mkShell {
          buildInputs = with pkgs; [
            go
            gopls
            gotools
            go-tools
            delve

            # For USB HID access on macOS
            pkg-config
            libusb1

            # Home Assistant CLI
            home-assistant-cli
          ];

          shellHook = ''
            export GOPATH="$PWD/.go"
            export PATH="$GOPATH/bin:$PATH"

            # Load env vars from .env.local if present
            if [ -f .env.local ]; then
              set -a
              source .env.local
              set +a
            fi
          '';
        };
      }
    )
    // {
      darwinModules.default = import ./nix/module.nix self;
    };
}
