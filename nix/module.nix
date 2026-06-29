self:
{
  config,
  lib,
  pkgs,
  ...
}:

with lib;
let
  cfg = config.services.belowdeck;

  # Generate config.yaml from settings attrset
  configFile = pkgs.writeText "belowdeck-config.yaml" (builtins.toJSON cfg.settings);
in
{
  options.services.belowdeck = {
    enable = mkEnableOption "Belowdeck Stream Deck Plus daemon";

    package = mkOption {
      type = types.package;
      default = self.packages.${pkgs.stdenv.hostPlatform.system}.default;
      defaultText = literalExpression "belowdeck.packages.\${system}.default";
      description = "The belowdeck package to use.";
    };

    user = mkOption {
      type = types.str;
      default = "phinze";
      description = "User account that will run the daemon.";
    };

    mediaControlPath = mkOption {
      type = types.str;
      default = "/opt/homebrew/bin/media-control";
      description = "Path to the media-control binary (Homebrew-only dependency).";
    };

    settings = mkOption {
      type = types.attrs;
      default = { };
      description = ''
        Non-secret configuration written to config.yaml.
        Secrets (API keys, tokens) are stored in macOS Keychain
        via `belowdeck setup`.
      '';
      example = literalExpression ''
        {
          weather = { lat = "42.3601"; lon = "-71.0589"; };
          homeassistant = {
            server = "https://ha.example.com/";
            ring_light_entity = "light.ring_light";
            office_light_entity = "light.office";
          };
        }
      '';
    };
  };

  config = mkIf cfg.enable {
    environment.systemPackages = [ cfg.package ];

    # Copy the binary to a stable path so macOS TCC (Input Monitoring)
    # doesn't create a new privacy entry on every nix rebuild.
    # TCC tracks permissions by binary path, and each rebuild produces
    # a new /nix/store/<hash> path.
    #
    # The codesign step replaces the linker-signed signature with a
    # proper ad-hoc one. taskgated rejects linker-signed binaries that
    # carry com.apple.provenance (set automatically when copying out of
    # /nix/store), causing SIGKILL at exec.
    system.activationScripts.postActivation.text = ''
      install -d /usr/local/bin
      cp -f ${cfg.package}/bin/belowdeck /usr/local/bin/belowdeck
      /usr/bin/codesign --force --sign - /usr/local/bin/belowdeck
    '';

    launchd.user.agents.belowdeck = {
      path = [
        "/usr/bin"
        "/bin"
        "/usr/sbin"
        "/sbin"
        (builtins.dirOf cfg.mediaControlPath)
        "/etc/profiles/per-user/${cfg.user}/bin"
      ];
      serviceConfig = {
        ProgramArguments = [
          "/usr/local/bin/belowdeck"
        ];
        EnvironmentVariables = {
          BELOWDECK_CONFIG = "${configFile}";
        };
        KeepAlive = true;
        RunAtLoad = true;
        StandardOutPath = "/tmp/belowdeck.log";
        StandardErrorPath = "/tmp/belowdeck.log";
      };
    };
  };
}
