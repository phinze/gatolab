// Package github provides a Stream Deck module for GitHub PR stats.
package github

import (
	"context"
	"image"
	"log"
	"os/exec"
	"sync"
	"time"

	"github.com/phinze/belowdeck/internal/module"
	"golang.org/x/image/font"
	"rafaelmartins.com/p/streamdeck"
)

// OverlayType indicates which overlay is currently active.
type OverlayType int

const (
	OverlayNone OverlayType = iota
	OverlayMyPRs
	OverlayReviewRequested
)

// Module implements the GitHub PR stats module.
type Module struct {
	module.BaseModule

	device  *streamdeck.Device
	client  *Client
	enabled bool

	// State for my PRs (Key3)
	mu     sync.RWMutex
	stats  PRStats
	prList []PRInfo

	// State for review-requested PRs (Key4)
	reviewStats  ReviewStats
	reviewPRList []PRInfo

	// Overlay state
	overlayType   OverlayType
	overlayExpiry time.Time

	// Fonts
	labelFace      font.Face
	numberFace     font.Face
	overlayFace    font.Face
	stripTitleFace font.Face
	stripLabelFace font.Face

	// Resources
	resources module.Resources

	// Context for fetching
	ctx context.Context
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
	m.ctx = ctx

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

// fetchStats fetches the current PR stats for both my PRs and review-requested PRs.
func (m *Module) fetchStats(ctx context.Context) {
	// Fetch my PR stats
	stats, err := m.client.GetMyPRStats(ctx)
	if err != nil {
		log.Printf("Failed to fetch GitHub PR stats: %v", err)
		return
	}

	// Also fetch PR list for overlay (includes CI status)
	prList, err := m.client.GetMyPRList(ctx)
	if err != nil {
		log.Printf("Failed to fetch GitHub PR list: %v", err)
		// Continue with stats even if list fails
	}

	// Count CI failures from PR list
	for _, pr := range prList {
		if pr.CI == CIStatusFailed {
			stats.CIFailed++
		}
	}

	// Fetch review-requested stats
	reviewStats, err := m.client.GetReviewRequestedStats(ctx)
	if err != nil {
		log.Printf("Failed to fetch review-requested stats: %v", err)
		// Continue with partial data
	}

	// Fetch review-requested PR list
	reviewPRList, err := m.client.GetReviewRequestedPRList(ctx)
	if err != nil {
		log.Printf("Failed to fetch review-requested PR list: %v", err)
		// Continue with partial data
	}

	m.mu.Lock()
	m.stats = stats
	if prList != nil {
		m.prList = prList
	}
	m.reviewStats = reviewStats
	if reviewPRList != nil {
		m.reviewPRList = reviewPRList
	}
	m.mu.Unlock()
}

// getStats returns the current PR stats.
func (m *Module) getStats() PRStats {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.stats
}

// getPRList returns the current PR list.
func (m *Module) getPRList() []PRInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.prList
}

// getReviewStats returns the current review-requested stats.
func (m *Module) getReviewStats() ReviewStats {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.reviewStats
}

// getReviewPRList returns the current review-requested PR list.
func (m *Module) getReviewPRList() []PRInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.reviewPRList
}

// RenderKeys returns images for the module's keys.
func (m *Module) RenderKeys() map[module.KeyID]image.Image {
	if !m.enabled {
		return nil
	}

	keys := make(map[module.KeyID]image.Image)

	// Key 0 (Key3): My PR stats overview (outbox)
	if len(m.resources.Keys) > 0 {
		keys[m.resources.Keys[0]] = m.renderPRStatsButton()
	}

	// Key 1 (Key4): Review-requested PRs (inbox)
	if len(m.resources.Keys) > 1 {
		keys[m.resources.Keys[1]] = m.renderReviewRequestedButton()
	}

	return keys
}

// RenderStrip returns the touch strip image.
func (m *Module) RenderStrip() image.Image {
	return nil
}

// HandleKey processes key events.
func (m *Module) HandleKey(id module.KeyID, event module.KeyEvent) error {
	// Only trigger on press (not release)
	if !event.Pressed {
		return nil
	}

	// Determine which overlay to show based on which key was pressed
	m.mu.Lock()
	if len(m.resources.Keys) > 1 && id == m.resources.Keys[1] {
		// Key4 pressed - show review-requested overlay
		m.overlayType = OverlayReviewRequested
	} else {
		// Key3 pressed - show my PRs overlay
		m.overlayType = OverlayMyPRs
	}
	m.overlayExpiry = time.Now().Add(5 * time.Second)
	m.mu.Unlock()

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

// HandleOverlayKey processes key events when the overlay is active.
func (m *Module) HandleOverlayKey(id module.KeyID, event module.KeyEvent) error {
	// Only trigger on press (not release)
	if !event.Pressed {
		return nil
	}

	// Key8 (bottom right) dismisses overlay
	if id == module.Key8 {
		m.mu.Lock()
		m.overlayType = OverlayNone
		m.mu.Unlock()
		return nil
	}

	// Get the appropriate PR list based on overlay type
	m.mu.RLock()
	overlayType := m.overlayType
	m.mu.RUnlock()

	var prList []PRInfo
	if overlayType == OverlayReviewRequested {
		prList = m.getReviewPRList()
	} else {
		prList = m.getPRList()
	}

	// Map key to PR index (Key1-Key7 map to PRs 0-6)
	keyIndex := int(id) - 1 // Key1=1, so subtract 1 for 0-indexed
	if keyIndex >= 0 && keyIndex < len(prList) {
		pr := prList[keyIndex]
		if pr.URL != "" {
			m.openURL(pr.URL)
		}
	}

	return nil
}

// HandleOverlayStripTouch processes touch strip events when the overlay is active.
func (m *Module) HandleOverlayStripTouch(event module.TouchStripEvent) error {
	// Only handle taps (short or long)
	if event.Type != module.TouchTap && event.Type != module.TouchLongTap {
		return nil
	}

	// Get the appropriate PR list based on overlay type
	m.mu.RLock()
	overlayType := m.overlayType
	m.mu.RUnlock()

	var prList []PRInfo
	if overlayType == OverlayReviewRequested {
		prList = m.getReviewPRList()
	} else {
		prList = m.getPRList()
	}

	if len(prList) == 0 {
		return nil
	}

	// Strip is 800px wide, divided into 4 sections of 200px each
	const prWidth = 200
	prIndex := event.Point.X / prWidth
	if prIndex >= 0 && prIndex < len(prList) && prIndex < 4 {
		pr := prList[prIndex]
		if pr.URL != "" {
			m.openURL(pr.URL)
		}
	}

	return nil
}

// openURL opens a URL in the default browser.
func (m *Module) openURL(url string) {
	if err := exec.Command("open", url).Start(); err != nil {
		log.Printf("Failed to open URL %s: %v", url, err)
	}
}

// IsOverlayActive returns true if the PR list overlay is visible.
func (m *Module) IsOverlayActive() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.overlayType == OverlayNone {
		return false
	}

	// Check if overlay has expired
	if time.Now().After(m.overlayExpiry) {
		// Need to acquire write lock to update
		m.mu.RUnlock()
		m.mu.Lock()
		m.overlayType = OverlayNone
		m.mu.Unlock()
		m.mu.RLock()
		return false
	}

	return true
}

// RenderOverlayKeys returns images for all 8 keys showing PR list.
func (m *Module) RenderOverlayKeys() map[module.KeyID]image.Image {
	keys := make(map[module.KeyID]image.Image)

	// Get the appropriate PR list based on overlay type
	m.mu.RLock()
	overlayType := m.overlayType
	m.mu.RUnlock()

	var prList []PRInfo
	if overlayType == OverlayReviewRequested {
		prList = m.getReviewPRList()
	} else {
		prList = m.getPRList()
	}

	// Render up to 7 PRs on Keys 1-7, Key8 is the back button
	prKeys := []module.KeyID{
		module.Key1, module.Key2, module.Key3, module.Key4,
		module.Key5, module.Key6, module.Key7,
	}

	for i, keyID := range prKeys {
		if i < len(prList) {
			keys[keyID] = m.renderPRKey(prList[i])
		} else {
			keys[keyID] = m.renderEmptyKey()
		}
	}

	// Key8 is the back button
	keys[module.Key8] = m.renderBackKey()

	return keys
}

// RenderOverlayStrip returns the touch strip image for the overlay.
func (m *Module) RenderOverlayStrip() image.Image {
	// Get the appropriate PR list based on overlay type
	m.mu.RLock()
	overlayType := m.overlayType
	m.mu.RUnlock()

	var prList []PRInfo
	if overlayType == OverlayReviewRequested {
		prList = m.getReviewPRList()
	} else {
		prList = m.getPRList()
	}

	return m.renderOverlayStripWithPRs(prList)
}
