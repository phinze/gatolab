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

	"github.com/phinze/belowdeck/internal/coordinator"
	"github.com/phinze/belowdeck/internal/module"
	"github.com/phinze/belowdeck/internal/modules/github"
	"github.com/phinze/belowdeck/internal/modules/homeassistant"
	"github.com/phinze/belowdeck/internal/modules/nowplaying"
	"github.com/phinze/belowdeck/internal/modules/weather"
	"github.com/prashantgupta24/mac-sleep-notifier/notifier"
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

	// Start sleep/wake notifier
	sleepCh := notifier.GetInstance().Start()
	wakeCh := make(chan struct{}, 1)
	go func() {
		for activity := range sleepCh {
			if activity.Type == notifier.Awake {
				log.Println("System wake detected")
				select {
				case wakeCh <- struct{}{}:
				default:
				}
			}
		}
	}()

	// Main device loop - wait for device, run, repeat on disconnect
	for {
		device := waitForDevice(ctx)
		if device == nil {
			// Context cancelled
			break
		}

		runWithDevice(ctx, device, wakeCh)

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
	device, err := streamdeck.GetDevice("")
	if err != nil {
		log.Printf("GetDevice error: %v", err)
	} else {
		if err := device.Open(); err != nil {
			log.Printf("Device found but Open failed: %v", err)
		} else {
			return device
		}
	}

	log.Println("Waiting for device...")

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(2 * time.Second):
		}

		device, err := streamdeck.GetDevice("")
		if err != nil {
			// Only log occasionally to avoid spam
			continue
		}
		if err := device.Open(); err != nil {
			log.Printf("Device found but Open failed: %v", err)
			continue
		}
		log.Println("Device connected!")
		return device
	}
}

// runWithDevice runs the coordinator with the given device until disconnect, wake, or context cancel.
func runWithDevice(ctx context.Context, device *streamdeck.Device, wakeCh <-chan struct{}) {
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

	ha := homeassistant.New(device)
	coord.RegisterModule(ha, module.Resources{
		Keys:  []module.KeyID{module.Key1, module.Key2},
		Dials: []module.DialID{module.Dial4},
	})

	gh := github.New(device)
	coord.RegisterModule(gh, module.Resources{
		Keys: []module.KeyID{module.Key3, module.Key4},
	})

	// Run coordinator with a child context so we can stop it independently
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()

	errChan := make(chan error, 1)
	go func() {
		errChan <- coord.Start(runCtx)
	}()

	log.Println("Ready! Media on left, weather on right")

	// Wait for parent context cancel, device error, or system wake
	select {
	case <-ctx.Done():
		log.Println("Shutting down...")
	case err := <-errChan:
		if err != nil {
			log.Printf("Device disconnected: %v", err)
		}
	case <-wakeCh:
		log.Println("Reconnecting device after wake...")
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

	// Close device - need to wait for this on wake to avoid race condition
	// where we try to reopen before close completes
	closeDone := make(chan struct{})
	go func() {
		device.Close()
		close(closeDone)
	}()

	// If parent context is cancelled (shutdown signal), force exit
	// since device.Close() may block indefinitely
	select {
	case <-ctx.Done():
		log.Println("Exiting...")
		os.Exit(0)
	case <-closeDone:
		// Device closed cleanly
	case <-time.After(3 * time.Second):
		// Device close timed out - on wake, give it a bit more time
		// then proceed anyway (might need to wait for device to reappear)
		log.Println("Device close timed out")
	}
}
