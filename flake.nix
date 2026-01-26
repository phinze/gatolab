{
  description = "Go development environment for Stream Deck Plus experiments";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
      in
      {
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
    );
}
