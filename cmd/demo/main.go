package main

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"log"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/image/colornames"
	"rafaelmartins.com/p/streamdeck"
)

func main() {
	log.Println("=== Stream Deck Plus Demo ===")
	log.Println("Press Ctrl+C to exit")

	// Enumerate all connected devices
	devices, err := streamdeck.Enumerate()
	if err != nil {
		log.Fatalf("Failed to enumerate devices: %v", err)
	}

	if len(devices) == 0 {
		log.Fatal("No Stream Deck devices found!")
	}

	fmt.Printf("\nFound %d device(s):\n", len(devices))
	for i, d := range devices {
		fmt.Printf("  %d: %s (serial: %s)\n", i+1, d.GetModelName(), d.GetSerialNumber())
	}

	// Use the first device
	device := devices[0]

	if err := device.Open(); err != nil {
		log.Fatalf("Failed to open device: %v", err)
	}
	defer func() {
		log.Println("Closing device...")
		device.Close()
	}()

	// Print device info
	fmt.Printf("\nDevice Info:\n")
	fmt.Printf("  Model: %s\n", device.GetModelName())
	fmt.Printf("  Serial: %s\n", device.GetSerialNumber())
	if fw, err := device.GetFirmwareVersion(); err == nil {
		fmt.Printf("  Firmware: %s\n", fw)
	}
	fmt.Printf("  Keys: %d\n", device.GetKeyCount())
	fmt.Printf("  Dials: %d\n", device.GetDialCount())
	fmt.Printf("  Touch Strip: %v\n", device.GetTouchStripSupported())
	fmt.Println()

	// Set brightness
	device.SetBrightness(80)

	// Setup keys with colors
	setupKeys(device)

	// Setup dials
	setupDials(device)

	// Setup touch strip
	setupTouchStrip(device)

	// Listen for events
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	errChan := make(chan error, 1)
	go func() {
		if err := device.Listen(errChan); err != nil {
			errChan <- err
		}
	}()

	log.Println("Ready! Try pressing buttons, rotating dials, or touching the strip...")

	for {
		select {
		case <-ctx.Done():
			return
		case <-sigChan:
			log.Println("\nReceived interrupt signal")
			cancel()
			return
		case err := <-errChan:
			if err != nil {
				log.Printf("Error: %v", err)
			}
		}
	}
}

func setupKeys(device *streamdeck.Device) {
	colors := []color.Color{
		colornames.Red,
		colornames.Orange,
		colornames.Yellow,
		colornames.Green,
		colornames.Cyan,
		colornames.Blue,
		colornames.Purple,
		colornames.Magenta,
	}

	device.ForEachKey(func(key streamdeck.KeyID) error {
		idx := int(key - streamdeck.KEY_1)
		if idx >= len(colors) {
			return nil
		}

		c := colors[idx]
		device.SetKeyColor(key, c)

		return device.AddKeyHandler(key, func(d *streamdeck.Device, k *streamdeck.Key) error {
			log.Printf("Key %s pressed!", k)

			// Flash white
			d.SetKeyColor(key, color.White)

			// Wait for release and measure duration
			duration := k.WaitForRelease()
			log.Printf("Key %s released after %v", k, duration)

			// Restore color
			return d.SetKeyColor(key, c)
		})
	})

	log.Println("Keys configured with rainbow colors")
}

func setupDials(device *streamdeck.Device) {
	if device.GetDialCount() == 0 {
		log.Println("No dials on this device")
		return
	}

	device.ForEachDial(func(dial streamdeck.DialID) error {
		// Handle rotation
		device.AddDialRotateHandler(dial, func(d *streamdeck.Device, di *streamdeck.Dial, delta int8) error {
			direction := "clockwise"
			if delta < 0 {
				direction = "counter-clockwise"
			}
			log.Printf("Dial %s rotated %s (delta: %d)", di, direction, delta)
			return nil
		})

		// Handle press
		return device.AddDialSwitchHandler(dial, func(d *streamdeck.Device, di *streamdeck.Dial) error {
			log.Printf("Dial %s pressed!", di)
			duration := di.WaitForRelease()
			log.Printf("Dial %s released after %v", di, duration)
			return nil
		})
	})

	log.Println("Dials configured")
}

func setupTouchStrip(device *streamdeck.Device) {
	if !device.GetTouchStripSupported() {
		log.Println("No touch strip on this device")
		return
	}

	// Set a gradient on the touch strip
	rect, err := device.GetTouchStripImageRectangle()
	if err != nil {
		log.Printf("Failed to get touch strip size: %v", err)
		return
	}

	img := createGradient(rect, colornames.Blueviolet, colornames.Orangered)
	device.SetTouchStripImage(img)

	// Handle touch
	device.AddTouchStripTouchHandler(func(d *streamdeck.Device, typ streamdeck.TouchStripTouchType, p image.Point) error {
		touchType := "short"
		if typ == streamdeck.TOUCH_STRIP_TOUCH_TYPE_LONG {
			touchType = "long"
		}
		log.Printf("Touch strip %s touch at (%d, %d)", touchType, p.X, p.Y)
		return nil
	})

	// Handle swipe
	device.AddTouchStripSwipeHandler(func(d *streamdeck.Device, origin image.Point, dest image.Point) error {
		direction := "right"
		if dest.X < origin.X {
			direction = "left"
		}
		log.Printf("Touch strip swiped %s: (%d,%d) -> (%d,%d)", direction, origin.X, origin.Y, dest.X, dest.Y)
		return nil
	})

	log.Println("Touch strip configured with gradient")
}

func createGradient(rect image.Rectangle, start, end color.RGBA) image.Image {
	img := image.NewRGBA(rect)

	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			t := float64(x-rect.Min.X) / float64(rect.Dx())

			r := float64(start.R)*(1-t) + float64(end.R)*t
			g := float64(start.G)*(1-t) + float64(end.G)*t
			b := float64(start.B)*(1-t) + float64(end.B)*t

			img.Set(x, y, color.RGBA{
				R: uint8(r),
				G: uint8(g),
				B: uint8(b),
				A: 255,
			})
		}
	}

	return img
}
