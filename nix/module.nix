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
      if [ ! -f /usr/local/bin/.belowdeck-source ] ||
        [ "$(cat /usr/local/bin/.belowdeck-source)" != "${cfg.package}" ]; then
        echo "belowdeck: installing ${cfg.package}" >&2
        cp -f ${cfg.package}/bin/belowdeck /usr/local/bin/belowdeck
        /usr/bin/codesign --force --sign - /usr/local/bin/belowdeck
        echo "${cfg.package}" > /usr/local/bin/.belowdeck-source

        # launchd caches the executable's code identity when the agent is
        # registered. After the binary changes underneath it, respawns fail
        # with EX_CONFIG (78) until the agent is re-registered; kickstart is
        # not enough, only a full bootout + bootstrap re-evaluates identity.
        # bootstrap can transiently fail while the bootout drains, so retry.
        uid=$(/usr/bin/id -u ${cfg.user})
        plist="/Users/${cfg.user}/Library/LaunchAgents/org.nixos.belowdeck.plist"
        if [ -f "$plist" ]; then
          /bin/launchctl bootout "gui/$uid/org.nixos.belowdeck" 2>/dev/null || true
          for _ in 1 2 3 4 5; do
            if /bin/launchctl bootstrap "gui/$uid" "$plist" 2>/dev/null; then
              echo "belowdeck: agent restarted" >&2
              break
            fi
            sleep 1
          done
        fi
      fi
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
