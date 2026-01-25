package main

import (
	"context"
	"image"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/phinze/gatolab/internal/coordinator"
	"github.com/phinze/gatolab/internal/module"
	"github.com/phinze/gatolab/internal/modules/nowplaying"
	"rafaelmartins.com/p/streamdeck"
)

func main() {
	log.Println("=== Stream Deck Now Playing ===")
	log.Println("Press Ctrl+C to exit")

	// Check if media-control is available
	if _, err := exec.LookPath("media-control"); err != nil {
		log.Fatal("media-control not found. Install with: brew tap ungive/media-control && brew install media-control")
	}

	// Get Stream Deck
	device, err := streamdeck.GetDevice("")
	if err != nil {
		log.Fatalf("Failed to get Stream Deck: %v", err)
	}

	if err := device.Open(); err != nil {
		log.Fatalf("Failed to open device: %v", err)
	}
	defer device.Close()

	log.Printf("Connected to: %s", device.GetModelName())

	// Set brightness
	device.SetBrightness(80)

	// Clear all keys first
	device.ForEachKey(func(key streamdeck.KeyID) error {
		return device.ClearKey(key)
	})

	// Create coordinator
	coord := coordinator.New(device)

	// Create and register nowplaying module
	np := nowplaying.New(device)
	coord.RegisterModule(np, module.Resources{
		Keys:      []module.KeyID{module.Key5, module.Key6},
		StripRect: image.Rect(0, 0, 400, 100),
		Dials:     []module.DialID{module.Dial1, module.Dial2},
	})

	// Setup signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Run coordinator in background
	errChan := make(chan error, 1)
	go func() {
		errChan <- coord.Start(ctx)
	}()

	log.Println("Ready! Media controls on left keys, now playing on left half of strip")

	// Wait for shutdown signal or error
	select {
	case <-sigChan:
		log.Println("\nShutting down...")
		cancel()
	case err := <-errChan:
		if err != nil {
			log.Printf("Coordinator error: %v", err)
		}
	}

	coord.Stop()
}
