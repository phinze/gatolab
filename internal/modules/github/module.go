// Package github provides a Stream Deck module for GitHub PR stats.
package github

import (
	"context"
	"image"
	"log"
	"sync"
	"time"

	"github.com/phinze/gatolab/internal/module"
	"golang.org/x/image/font"
	"rafaelmartins.com/p/streamdeck"
)

// Module implements the GitHub PR stats module.
type Module struct {
	module.BaseModule

	device  *streamdeck.Device
	client  *Client
	enabled bool

	// State
	mu    sync.RWMutex
	stats PRStats

	// Fonts
	labelFace  font.Face
	numberFace font.Face

	// Resources
	resources module.Resources
}

// New creates a new GitHub module.
func New(device *streamdeck.Device) *Module {
	return &Module{
		BaseModule: module.NewBaseModule("github"),
		device:     device,
	}
}

// ID returns the module identifier.
func (m *Module) ID() string {
	return "github"
}

// Init initializes the module.
func (m *Module) Init(ctx context.Context, res module.Resources) error {
	if err := m.BaseModule.Init(ctx, res); err != nil {
		return err
	}

	m.resources = res

	// Create API client (uses gh CLI token)
	client, err := NewClient()
	if err != nil {
		log.Printf("GitHub module disabled: %v", err)
		m.enabled = false
		return nil
	}
	m.client = client
	m.enabled = true

	// Initialize fonts
	if err := m.initFonts(); err != nil {
		return err
	}

	// Start polling
	go m.pollStats(ctx)

	log.Println("GitHub module initialized")
	return nil
}

// Stop shuts down the module.
func (m *Module) Stop() error {
	return m.BaseModule.Stop()
}

// pollStats periodically fetches PR stats from GitHub.
func (m *Module) pollStats(ctx context.Context) {
	// Initial fetch
	m.fetchStats(ctx)

	// Poll every 2 minutes (to avoid rate limits)
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.fetchStats(ctx)
		}
	}
}

// fetchStats fetches the current PR stats.
func (m *Module) fetchStats(ctx context.Context) {
	stats, err := m.client.GetMyPRStats(ctx)
	if err != nil {
		log.Printf("Failed to fetch GitHub PR stats: %v", err)
		return
	}

	m.mu.Lock()
	m.stats = stats
	m.mu.Unlock()
}

// getStats returns the current PR stats.
func (m *Module) getStats() PRStats {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.stats
}

// RenderKeys returns images for the module's keys.
func (m *Module) RenderKeys() map[module.KeyID]image.Image {
	if !m.enabled {
		return nil
	}

	keys := make(map[module.KeyID]image.Image)

	// Key 0: PR stats overview
	if len(m.resources.Keys) > 0 {
		keys[m.resources.Keys[0]] = m.renderPRStatsButton()
	}

	return keys
}

// RenderStrip returns the touch strip image.
func (m *Module) RenderStrip() image.Image {
	return nil
}

// HandleKey processes key events.
func (m *Module) HandleKey(id module.KeyID, event module.KeyEvent) error {
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
