# Roadmap

## Vision

A modular Stream Deck Plus application that combines multiple utilities into a cohesive dashboard - media controls, calendar, home automation, weather, and team presence - with a clean architecture that allows features to coexist and evolve independently.

## Current State

**Now Playing Module** (implemented)
- 2 keys (play/pause, info) on bottom-left
- Left half of touch strip (album art, title, artist, progress)
- Dials 1+2 for seek and prev/next

**Weather Module** (implemented)
- Right half of touch strip (icon, temp, feels like, high/low, precipitation forecast)
- OpenWeatherMap One Call 3.0 API with minutely precipitation data
- No keys or dials used

**Daemon Mode** (implemented)
- Persistent process that waits for device connection
- Automatic reconnection on device disconnect
- Automatic reconnection after system sleep/wake

**Available Resources**
- 6 keys (top row + bottom-right)
- Dials 3+4

## Planned Features

### MeetingBar Equivalent
Calendar integration showing next meeting with countdown and quick-join.
- **Keys**: 1-2 (join meeting, dismiss/snooze)
- **Strip**: Could show next meeting info
- **Data**: macOS Calendar (EventKit) or Google Calendar API

### Home Assistant Integration
Control smart home devices, especially office setup.
- **Keys**: 2-3 (Office Time scene, ring light toggle, etc.)
- **Strip**: Temperature display
- **Dials**: D3/D4 for light brightness
- **Data**: HA REST API or WebSocket (already on Tailscale)

### Weather ✅
Quick glance at current conditions and forecast.
- **Strip**: Right half (400-800px) with icon, temp, feels like, high/low, precipitation
- **Data**: OpenWeatherMap One Call 3.0 API

### Around Integration
Team presence and status from custom Around app.
- **Keys**: 1-2 (status toggle, presence indicator)
- **Strip**: Could show team presence
- **Data**: Custom API (TBD based on Around's capabilities)

## Architecture

### Phase 1: Module System Foundation ✅

Define a clean module interface that allows features to:
- Declare resource requirements (keys, strip regions, dials)
- Handle their own lifecycle (init, update, cleanup)
- Render to allocated screen regions
- Respond to input events (key press, dial rotate/press)

**Implemented in `internal/module/`:**
- `resources.go` - `KeyID`, `DialID`, `Resources` types with helper methods
- `events.go` - `DialEvent`, `KeyEvent`, `TouchStripEvent` structured event types
- `module.go` - `Module` interface definition
- `base.go` - `BaseModule` with default no-op implementations for embedding
- `convert.go` - Conversion helpers to/from streamdeck library types

```go
type Module interface {
    ID() string
    Init(ctx context.Context, resources Resources) error
    Stop() error

    // Rendering
    RenderKeys() map[KeyID]image.Image
    RenderStrip() image.Image  // Module's strip segment

    // Input handling
    HandleKey(id KeyID, event KeyEvent) error
    HandleDial(id DialID, event DialEvent) error
    HandleStripTouch(event TouchStripEvent) error
}

type Resources struct {
    Keys      []KeyID
    StripRect image.Rectangle
    Dials     []DialID
}
```

### Phase 2: Layout Coordinator ✅

A central coordinator that:
- Manages module lifecycle
- Allocates resources to modules
- Composites strip segments into full strip image
- Routes input events to appropriate modules

**Implemented in `internal/coordinator/`:**
- Module registration with resource allocation
- Event routing (keys, dials, touch strip)
- Strip compositing from multiple modules
- Render loop with periodic updates

### Phase 3: Configuration

YAML/JSON config for layout customization:
```yaml
layout:
  modules:
    - id: nowplaying
      keys: [5, 6]
      strip: { x: 0, width: 400 }
      dials: [1, 2]
    - id: calendar
      keys: [1, 2]
      strip: { x: 400, width: 200 }
    - id: homeassistant
      keys: [3, 4, 7, 8]
      strip: { x: 600, width: 200 }
      dials: [3, 4]
```

### Phase 4: Advanced Features

- **Multi-page**: Navigate between different layouts/pages
- **Pop-overs**: Temporary overlays (e.g., meeting join confirmation)
- **Notifications**: Cross-module alerts and updates

## Implementation Order

1. ✅ **Module interface** - Define the contract
2. ✅ **Refactor now-playing** - Convert to first module
3. ✅ **Layout coordinator** - Basic resource allocation
4. ✅ **Weather module** - OpenWeatherMap One Call 3.0 API
5. **Home Assistant module** - High value, familiar API
6. **Calendar module** - More complex, EventKit or Google API
7. **Multi-page support** - As we run out of space
8. **Around module** - Depends on API availability
9. **Pop-overs/overlays** - Polish feature

## Technical Notes

### Libraries in Use
- `rafaelmartins.com/p/streamdeck` - Stream Deck Plus support (dials, touch strip)
- `github.com/srwiley/oksvg` - SVG icon rendering
- `media-control` CLI - macOS now-playing info
- `github.com/prashantgupta24/mac-sleep-notifier` - macOS sleep/wake detection

### Potential Libraries
- Home Assistant: REST API or `github.com/home-assistant/home-assistant-go` (if exists)
- Calendar: `github.com/emersion/go-ical` or direct EventKit via cgo
- Weather: Simple HTTP to OpenWeatherMap

### Stream Deck Plus Specs
- 8 LCD keys (72x72 pixels each)
- 4 rotary encoders with push
- Touch strip (800x100 pixels)
- USB-C HID device
