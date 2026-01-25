// Package coordinator manages module lifecycle and routes events to modules.
package coordinator

import (
	"context"
	"image"
	"image/draw"
	"sync"
	"time"

	"github.com/phinze/gatolab/internal/module"
	"rafaelmartins.com/p/streamdeck"
)

// Coordinator manages the lifecycle of modules and routes events to them.
type Coordinator struct {
	device  *streamdeck.Device
	modules []module.Module

	// Resource tracking
	moduleResources map[module.Module]module.Resources

	// Ownership maps for event routing
	keyOwners  map[module.KeyID]module.Module
	dialOwners map[module.DialID]module.Module

	// Strip compositing
	stripRect image.Rectangle

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// State tracking
	mu sync.RWMutex
}

// New creates a new Coordinator for the given device.
func New(device *streamdeck.Device) *Coordinator {
	return &Coordinator{
		device:          device,
		modules:         make([]module.Module, 0),
		moduleResources: make(map[module.Module]module.Resources),
		keyOwners:       make(map[module.KeyID]module.Module),
		dialOwners:      make(map[module.DialID]module.Module),
	}
}

// RegisterModule registers a module with its allocated resources.
// Must be called before Start.
func (c *Coordinator) RegisterModule(m module.Module, res module.Resources) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Store resources for this module
	c.moduleResources[m] = res

	// Build ownership maps
	for _, key := range res.Keys {
		c.keyOwners[key] = m
	}
	for _, dial := range res.Dials {
		c.dialOwners[dial] = m
	}

	// Track module
	c.modules = append(c.modules, m)

	return nil
}

// Start initializes all modules and begins the event/render loop.
func (c *Coordinator) Start(ctx context.Context) error {
	c.ctx, c.cancel = context.WithCancel(ctx)

	// Get full strip rectangle for compositing
	if c.device.GetTouchStripSupported() {
		rect, err := c.device.GetTouchStripImageRectangle()
		if err == nil {
			c.stripRect = rect
		}
	}

	// Initialize all modules
	for _, m := range c.modules {
		// Find resources for this module
		res := c.resourcesForModule(m)
		if err := m.Init(c.ctx, res); err != nil {
			return err
		}
	}

	// Setup event handlers
	c.setupEventHandlers()

	// Start device listener (not in WaitGroup - closed by device.Close())
	errChan := make(chan error, 1)
	go func() {
		if err := c.device.Listen(errChan); err != nil {
			select {
			case errChan <- err:
			default:
			}
		}
	}()

	// Start render loop
	c.wg.Add(1)
	go c.renderLoop()

	// Wait for context cancellation
	<-c.ctx.Done()

	return nil
}

// Stop gracefully shuts down all modules.
func (c *Coordinator) Stop() error {
	if c.cancel != nil {
		c.cancel()
	}

	// Stop all modules
	for _, m := range c.modules {
		m.Stop()
	}

	c.wg.Wait()
	return nil
}

// resourcesForModule returns the stored resources for a module.
func (c *Coordinator) resourcesForModule(m module.Module) module.Resources {
	return c.moduleResources[m]
}

// setupEventHandlers registers device event handlers that route to modules.
func (c *Coordinator) setupEventHandlers() {
	// Key handlers
	for keyID, m := range c.keyOwners {
		key := keyID
		mod := m
		c.device.AddKeyHandler(key.ToStreamdeck(), func(d *streamdeck.Device, k *streamdeck.Key) error {
			// Create press event
			event := module.KeyEvent{Pressed: true}
			if err := mod.HandleKey(key, event); err != nil {
				return err
			}

			// Wait for release and create release event
			duration := k.WaitForRelease()
			event = module.KeyEvent{Pressed: false, Duration: duration}
			return mod.HandleKey(key, event)
		})
	}

	// Dial rotation handlers
	for dialID, m := range c.dialOwners {
		dial := dialID
		mod := m
		c.device.AddDialRotateHandler(dial.ToStreamdeck(), func(d *streamdeck.Device, di *streamdeck.Dial, delta int8) error {
			event := module.DialEvent{
				Type:  module.DialRotate,
				Delta: delta,
			}
			return mod.HandleDial(dial, event)
		})
	}

	// Dial press handlers
	for dialID, m := range c.dialOwners {
		dial := dialID
		mod := m
		c.device.AddDialSwitchHandler(dial.ToStreamdeck(), func(d *streamdeck.Device, di *streamdeck.Dial) error {
			// Create press event
			event := module.DialEvent{Type: module.DialPress}
			if err := mod.HandleDial(dial, event); err != nil {
				return err
			}

			// Wait for release and create release event
			duration := di.WaitForRelease()
			event = module.DialEvent{Type: module.DialRelease, Duration: duration}
			return mod.HandleDial(dial, event)
		})
	}

	// Touch strip handler - route based on X coordinate
	if c.device.GetTouchStripSupported() {
		c.device.AddTouchStripTouchHandler(func(d *streamdeck.Device, touchType streamdeck.TouchStripTouchType, point image.Point) error {
			event := module.TouchStripEventFromTap(touchType, point)
			return c.routeStripEvent(event)
		})

		c.device.AddTouchStripSwipeHandler(func(d *streamdeck.Device, origin, dest image.Point) error {
			event := module.TouchStripEventFromSwipe(origin, dest)
			return c.routeStripEvent(event)
		})
	}
}

// routeStripEvent finds the owning module for a strip event and dispatches it.
func (c *Coordinator) routeStripEvent(event module.TouchStripEvent) error {
	// For now, route to first module that has a strip region
	// Future: check which module's strip rect contains the event point
	for _, m := range c.modules {
		res := c.resourcesForModule(m)
		if res.HasStrip() {
			return m.HandleStripTouch(event)
		}
	}
	return nil
}

// renderLoop runs the periodic render cycle.
func (c *Coordinator) renderLoop() {
	defer c.wg.Done()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	// Initial render
	c.renderKeys()
	c.renderStrip()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.renderKeys()
			c.renderStrip()
		}
	}
}

// renderKeys collects key images from all modules and applies them to the device.
func (c *Coordinator) renderKeys() {
	for _, m := range c.modules {
		keyImages := m.RenderKeys()
		for keyID, img := range keyImages {
			if img != nil {
				c.device.SetKeyImage(keyID.ToStreamdeck(), img)
			}
		}
	}
}

// renderStrip composites strip images from all modules and applies to the device.
func (c *Coordinator) renderStrip() {
	if c.stripRect.Empty() {
		return
	}

	// Create composite strip image
	composite := image.NewRGBA(c.stripRect)

	// Collect and composite each module's strip output
	for _, m := range c.modules {
		res := c.resourcesForModule(m)
		if !res.HasStrip() {
			continue
		}

		stripImg := m.RenderStrip()
		if stripImg == nil {
			continue
		}

		// Draw module's strip at its allocated region
		// For now, we draw at 0,0 - in future, we'd use res.StripRect offset
		draw.Draw(composite, stripImg.Bounds(), stripImg, image.Point{}, draw.Over)
	}

	c.device.SetTouchStripImage(composite)
}

// Device returns the underlying streamdeck device.
// Modules can use this to query device capabilities like key size.
func (c *Coordinator) Device() *streamdeck.Device {
	return c.device
}
