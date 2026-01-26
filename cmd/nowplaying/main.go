package main

import (
	"context"
	"image"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/phinze/gatolab/internal/coordinator"
	"github.com/phinze/gatolab/internal/module"
	"github.com/phinze/gatolab/internal/modules/nowplaying"
	"github.com/phinze/gatolab/internal/modules/weather"
	"rafaelmartins.com/p/streamdeck"
)

func main() {
	log.Println("=== Stream Deck Daemon ===")
	log.Println("Press Ctrl+C to exit")

	// Check if media-control is available
	if _, err := exec.LookPath("media-control"); err != nil {
		log.Fatal("media-control not found. Install with: brew tap ungive/media-control && brew install media-control")
	}

	// Setup signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("\nReceived shutdown signal")
		cancel()
	}()

	// Main device loop - wait for device, run, repeat on disconnect
	for {
		device := waitForDevice(ctx)
		if device == nil {
			// Context cancelled
			break
		}

		runWithDevice(ctx, device)

		// Check if we should exit or wait for reconnect
		select {
		case <-ctx.Done():
			log.Println("Exiting...")
			return
		default:
			log.Println("Waiting for device reconnect...")
		}
	}
}

// waitForDevice polls for a Stream Deck device until one is available.
// Uses polling since macOS doesn't have a simple USB hotplug event API.
func waitForDevice(ctx context.Context) *streamdeck.Device {
	// First, try to get an already-connected device
	if device, err := streamdeck.GetDevice(""); err == nil {
		if err := device.Open(); err == nil {
			return device
		}
	}

	log.Println("No device found, waiting...")

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(2 * time.Second):
		}

		if device, err := streamdeck.GetDevice(""); err == nil {
			if err := device.Open(); err == nil {
				log.Println("Device connected!")
				return device
			}
		}
	}
}

// runWithDevice runs the coordinator with the given device until disconnect or context cancel.
func runWithDevice(ctx context.Context, device *streamdeck.Device) {
	defer device.Close()

	log.Printf("Connected to: %s", device.GetModelName())

	// Set brightness and clear keys
	device.SetBrightness(80)
	device.ForEachKey(func(key streamdeck.KeyID) error {
		return device.ClearKey(key)
	})

	// Create coordinator and modules fresh for each connection
	coord := coordinator.New(device)

	np := nowplaying.New(device)
	coord.RegisterModule(np, module.Resources{
		Keys:      []module.KeyID{module.Key5, module.Key6},
		StripRect: image.Rect(0, 0, 400, 100),
		Dials:     []module.DialID{module.Dial1, module.Dial2},
	})

	w := weather.New(device)
	coord.RegisterModule(w, module.Resources{
		StripRect: image.Rect(400, 0, 800, 100),
	})

	// Run coordinator with a child context so we can stop it independently
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()

	errChan := make(chan error, 1)
	go func() {
		errChan <- coord.Start(runCtx)
	}()

	log.Println("Ready! Media on left, weather on right")

	// Wait for parent context cancel or device error
	select {
	case <-ctx.Done():
		log.Println("Shutting down...")
	case err := <-errChan:
		if err != nil {
			log.Printf("Device disconnected: %v", err)
		}
	}

	// Stop coordinator with timeout
	runCancel()

	done := make(chan struct{})
	go func() {
		coord.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		log.Println("Cleanup timed out")
	}
}
