// Package nowplaying provides a Stream Deck module for media playback control and display.
package nowplaying

import (
	"context"
	"image"
	"log"
	"os/exec"
	"sync"

	"github.com/phinze/gatolab/internal/module"
	"golang.org/x/image/font"
	"rafaelmartins.com/p/streamdeck"
)

// Module implements the nowplaying media control module.
type Module struct {
	module.BaseModule

	device *streamdeck.Device

	// State
	liveState     *liveState
	cachedArtwork image.Image
	artworkHash   string
	lastPlaying   bool
	mu            sync.RWMutex

	// Fonts
	titleFace  font.Face
	artistFace font.Face

	// Cancel function for media stream
	streamCancel context.CancelFunc
}

// New creates a new NowPlaying module.
func New(device *streamdeck.Device) *Module {
	return &Module{
		BaseModule: module.NewBaseModule("nowplaying"),
		device:     device,
		liveState:  newLiveState(),
	}
}

// ID returns the module identifier.
func (m *Module) ID() string {
	return "nowplaying"
}

// Init initializes the module.
func (m *Module) Init(ctx context.Context, res module.Resources) error {
	// Call base init
	if err := m.BaseModule.Init(ctx, res); err != nil {
		return err
	}

	// Initialize fonts
	if err := m.initFonts(); err != nil {
		return err
	}

	// Start media stream in background
	streamCtx, cancel := context.WithCancel(ctx)
	m.streamCancel = cancel
	go m.startMediaStream(streamCtx)

	log.Println("NowPlaying module initialized")
	return nil
}

// Stop shuts down the module.
func (m *Module) Stop() error {
	if m.streamCancel != nil {
		m.streamCancel()
	}
	return m.BaseModule.Stop()
}

// RenderKeys returns images for the module's keys.
func (m *Module) RenderKeys() map[module.KeyID]image.Image {
	keyRect, _ := m.device.GetKeyImageRectangle()
	size := keyRect.Dx()

	keys := make(map[module.KeyID]image.Image)

	// Get current state
	np := m.liveState.get()

	// Key 5: Play/Pause icon (changes based on state)
	m.mu.Lock()
	if np.Playing != m.lastPlaying {
		m.lastPlaying = np.Playing
	}
	playing := m.lastPlaying
	m.mu.Unlock()

	if playing {
		keys[module.Key5] = renderSVGIcon(iconPauseSVG, size, colorOrange)
	} else {
		keys[module.Key5] = renderSVGIcon(iconPlaySVG, size, colorLimeGreen)
	}

	// Key 6: Info icon (static)
	keys[module.Key6] = renderSVGIcon(iconInfoSVG, size, colorDeepSkyBlue)

	return keys
}

// RenderStrip returns the touch strip image.
func (m *Module) RenderStrip() image.Image {
	if !m.device.GetTouchStripSupported() {
		return nil
	}

	rect, err := m.device.GetTouchStripImageRectangle()
	if err != nil {
		return nil
	}

	np := m.liveState.get()

	// Update artwork cache if changed
	m.mu.Lock()
	if np.ArtworkData != "" && np.ArtworkData != m.artworkHash {
		if img := decodeArtwork(np.ArtworkData); img != nil {
			m.cachedArtwork = img
			m.artworkHash = np.ArtworkData
			log.Printf("Track: %s - %s", np.Artist, np.Title)
		}
	}
	artwork := m.cachedArtwork
	m.mu.Unlock()

	return m.renderStrip(rect, &np, artwork)
}

// HandleKey processes key events.
func (m *Module) HandleKey(id module.KeyID, event module.KeyEvent) error {
	// Only handle press events
	if !event.Pressed {
		return nil
	}

	switch id {
	case module.Key5:
		log.Println("Key: Toggle play/pause")
		go exec.Command("media-control", "toggle-play-pause").Run()
	case module.Key6:
		np := m.liveState.get()
		log.Printf("Info: %s - %s (%s)", np.Artist, np.Title, np.Album)
	}

	return nil
}

// HandleDial processes dial events.
func (m *Module) HandleDial(id module.DialID, event module.DialEvent) error {
	switch id {
	case module.Dial1:
		switch event.Type {
		case module.DialRotate:
			// Seek 5 seconds per tick
			seekAmount := int64(event.Delta) * 5 * 1000000 // 5 seconds in micros
			log.Printf("Dial: Seeking %+d seconds", event.Delta*5)

			np := m.liveState.get()
			currentPos := getLiveElapsedMicros(&np)

			newPos := currentPos + seekAmount
			if newPos < 0 {
				newPos = 0
			}
			if newPos > np.DurationMicros {
				newPos = np.DurationMicros
			}

			// media-control seek takes seconds
			go exec.Command("media-control", "seek", formatSeekPosition(newPos)).Run()

		case module.DialPress:
			log.Println("Dial: Toggle play/pause")
			go exec.Command("media-control", "toggle-play-pause").Run()
		}

	case module.Dial2:
		if event.Type == module.DialRotate {
			if event.Delta < 0 {
				log.Println("Dial: Previous track")
				go exec.Command("media-control", "previous-track").Run()
			} else {
				log.Println("Dial: Next track")
				go exec.Command("media-control", "next-track").Run()
			}
		}
	}

	return nil
}

// HandleStripTouch processes touch strip events.
func (m *Module) HandleStripTouch(event module.TouchStripEvent) error {
	// Not implemented yet - could add seek by touch
	return nil
}
