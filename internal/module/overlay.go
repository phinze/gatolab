package module

import "image"

// OverlayProvider is an interface that modules can implement to provide
// full-screen overlays that temporarily take over the entire display.
type OverlayProvider interface {
	// IsOverlayActive returns true if the module currently has an active overlay.
	IsOverlayActive() bool

	// RenderOverlayKeys returns images for ALL keys when the overlay is active.
	// The returned map should include images for all 8 keys (Key1-Key8).
	RenderOverlayKeys() map[KeyID]image.Image

	// RenderOverlayStrip returns the full touch strip image when the overlay is active.
	RenderOverlayStrip() image.Image

	// HandleOverlayKey processes key events when the overlay is active.
	// This allows the overlay to respond to any key press, not just owned keys.
	HandleOverlayKey(id KeyID, event KeyEvent) error

	// HandleOverlayStripTouch processes touch strip events when the overlay is active.
	HandleOverlayStripTouch(event TouchStripEvent) error
}
