# gatolab

A modular Stream Deck Plus application for macOS, currently tailored to my personal setup. The architecture is designed to be extensible, and I may generalize it into a configurable tool in the future.

## Modules

- **Now Playing** - Media controls with album art, play/pause, track navigation, and volume dial
- **Weather** - Current conditions and temperature via OpenWeatherMap
- **Home Assistant** - Smart home control (currently: ring light toggle and brightness)
- **GitHub** - Notifications display (work in progress)

## Hardware

Elgato Stream Deck Plus: 8 LCD keys (72x72px), 4 rotary dials, touch strip (800x100px).

## Setup

### Dependencies

```bash
# Required for media controls
brew tap ungive/media-control && brew install media-control
```

### Configuration

Copy the example environment file and fill in your values:

```bash
cp .env.local.example .env.local
```

See `.env.local.example` for required variables and where to obtain API keys.

### Running

```bash
go build -o ./bin/nowplaying ./cmd/nowplaying && ./bin/nowplaying
```

Note: Only one application can control the Stream Deck at a time. Quit the Elgato software before running.

## Resources

- [rafaelmartins.com/p/streamdeck](https://rafaelmartins.com/p/streamdeck) - Go library with dial/strip support
- [Lucide](https://lucide.dev/) - Icon set used for UI elements
