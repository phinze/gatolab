// Package homeassistant provides a Stream Deck module for Home Assistant control.
package homeassistant

import (
	"context"
	"fmt"
	"image"
	"log"
	"os"
	"sync"

	"github.com/phinze/gatolab/internal/module"
	"golang.org/x/image/font"
	"rafaelmartins.com/p/streamdeck"
)

// Config holds the Home Assistant module configuration.
type Config struct {
	URL   string
	Token string
}

// Module implements the Home Assistant control module.
type Module struct {
	module.BaseModule

	device  *streamdeck.Device
	config  Config
	client  *Client
	enabled bool

	// State
	mu sync.RWMutex

	// Fonts
	labelFace font.Face

	// Resources
	resources module.Resources
}

// New creates a new Home Assistant module.
func New(device *streamdeck.Device) *Module {
	return &Module{
		BaseModule: module.NewBaseModule("homeassistant"),
		device:     device,
	}
}

// ID returns the module identifier.
func (m *Module) ID() string {
	return "homeassistant"
}

// Init initializes the module.
func (m *Module) Init(ctx context.Context, res module.Resources) error {
	// Call base init
	if err := m.BaseModule.Init(ctx, res); err != nil {
		return err
	}

	m.resources = res

	// Load config from environment (optional - module disabled if not configured)
	config, err := loadConfig()
	if err != nil {
		log.Printf("Home Assistant module disabled: %v", err)
		m.enabled = false
		return nil
	}
	m.config = config
	m.enabled = true

	// Create API client
	m.client = NewClient(m.config.URL, m.config.Token)

	// Initialize fonts
	if err := m.initFonts(); err != nil {
		return err
	}

	log.Printf("Home Assistant module initialized (url=%s)", m.config.URL)
	return nil
}

// Stop shuts down the module.
func (m *Module) Stop() error {
	return m.BaseModule.Stop()
}

// loadConfig loads configuration from environment variables.
func loadConfig() (Config, error) {
	url := os.Getenv("HASS_SERVER")
	if url == "" {
		return Config{}, fmt.Errorf("HASS_SERVER environment variable not set")
	}

	token := os.Getenv("HASS_TOKEN")
	if token == "" {
		return Config{}, fmt.Errorf("HASS_TOKEN environment variable not set")
	}

	return Config{
		URL:   url,
		Token: token,
	}, nil
}

// RenderKeys returns images for the module's keys.
func (m *Module) RenderKeys() map[module.KeyID]image.Image {
	if !m.enabled {
		return nil
	}

	keys := make(map[module.KeyID]image.Image)

	// Render Office Time button on first allocated key
	if len(m.resources.Keys) > 0 {
		keys[m.resources.Keys[0]] = m.renderOfficeTimeButton()
	}

	return keys
}

// RenderStrip returns the touch strip image.
func (m *Module) RenderStrip() image.Image {
	return nil
}

// HandleKey processes key events.
func (m *Module) HandleKey(id module.KeyID, event module.KeyEvent) error {
	if !m.enabled {
		return nil
	}

	// Only trigger on key press, not release
	if !event.Pressed {
		return nil
	}

	// Handle Office Time button (first key)
	if len(m.resources.Keys) > 0 && id == m.resources.Keys[0] {
		return m.executeOfficeTime()
	}

	return nil
}

// executeOfficeTime runs the Office Time script.
func (m *Module) executeOfficeTime() error {
	log.Println("Executing Office Time script...")

	err := m.client.CallService(context.Background(), "script", "turn_on", map[string]any{
		"entity_id": "script.office_time",
	})
	if err != nil {
		log.Printf("Failed to execute Office Time: %v", err)
		return err
	}

	log.Println("Office Time script executed successfully")
	return nil
}

// HandleDial processes dial events.
func (m *Module) HandleDial(id module.DialID, event module.DialEvent) error {
	return nil
}

// HandleStripTouch processes touch strip events.
func (m *Module) HandleStripTouch(event module.TouchStripEvent) error {
	return nil
}
