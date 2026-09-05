package main

import (
	"context"
	"fmt"
	"image"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/phinze/belowdeck/internal/config"
	"github.com/phinze/belowdeck/internal/coordinator"
	"github.com/phinze/belowdeck/internal/device"
	"github.com/phinze/belowdeck/internal/module"
	"github.com/phinze/belowdeck/internal/modules/github"
	"github.com/phinze/belowdeck/internal/modules/homeassistant"
	"github.com/phinze/belowdeck/internal/modules/nowplaying"
	"github.com/phinze/belowdeck/internal/modules/weather"
	"github.com/phinze/belowdeck/internal/usbwatch"
	"github.com/prashantgupta24/mac-sleep-notifier/notifier"
	"github.com/spf13/cobra"
	"rafaelmartins.com/p/streamdeck"
)

func runDaemon(cmd *cobra.Command, args []string) error {
	log.Println("=== Stream Deck Daemon ===")
	log.Println("Press Ctrl+C to exit")

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Printf("Warning: config load: %v", err)
	}

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

	// Start sleep/wake notifier and run device loop
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

	// Start event-driven USB device watcher (fires callback on device arrival)
	deviceArrivedCh := usbwatch.Watch(ctx, 0x0fd9)

	// Main device loop - wait for device, run, repeat on disconnect
	for {
		dev := waitForHardwareDevice(ctx, wakeCh, deviceArrivedCh)
		if dev == nil {
			// Context cancelled
			break
		}

		// Check context before starting - avoid race where device connects after shutdown requested
		select {
		case <-ctx.Done():
			log.Println("Exiting...")
			dev.Close()
			return nil
		default:
		}

		// Drain any stale wake signals that accumulated while waiting for device.
		// Without this, a wake signal from before device enumeration would
		// immediately trigger a teardown in runWithDevice.
	drainWake:
		for {
			select {
			case <-wakeCh:
				log.Println("Draining stale wake signal")
			default:
				break drainWake
			}
		}

		// Brief stabilization delay - USB device enumeration may not be complete
		// even after GetDevice succeeds. Give the device a moment to fully initialize.
		time.Sleep(500 * time.Millisecond)

		runWithDevice(ctx, cfg, dev, wakeCh)

		// Check if we should exit or wait for reconnect
		select {
		case <-ctx.Done():
			log.Println("Exiting...")
			return nil
		default:
			log.Println("Waiting for device reconnect...")
		}
	}

	return nil
}

// enumInFlight tracks whether a device enumeration goroutine is currently running.
// IOHIDManagerCopyDevices can block indefinitely in the kernel when the USB subsystem
// is in a bad state. Without this guard, each timed-out poll spawns a new goroutine
// that also blocks, piling up zombie goroutines that hold IOKit resources and prevent
// any future enumeration from succeeding.
var enumInFlight atomic.Bool

// enumStuckWarned latches the "enumeration still in flight" warning so it is
// logged once per episode instead of on every rejected probe. waitForHardwareDevice
// now polls on a timer, and without the latch a permanently stuck enumeration
// would print a line every few seconds forever.
var enumStuckWarned atomic.Bool

// tryGetDeviceWithTimeout attempts to get and open a Stream Deck device with a timeout.
// Returns the device if successful, nil otherwise. Only one enumeration goroutine is
// allowed in flight at a time to prevent IOKit resource contention.
func tryGetDeviceWithTimeout(timeout time.Duration) *streamdeck.Device {
	// If a previous enumeration is still stuck in CGO, don't spawn another.
	// enumInFlight is only cleared by the enumeration goroutine's defer, so a
	// permanent CGO hang wedges probing for the life of the process. Say so
	// out loud rather than returning nil silently until someone notices the
	// deck has been dark for a day.
	if !enumInFlight.CompareAndSwap(false, true) {
		if enumStuckWarned.CompareAndSwap(false, true) {
			log.Println("Device enumeration still in flight from an earlier probe; skipping probes until it returns")
		}
		return nil
	}
	enumStuckWarned.Store(false)

	type result struct {
		dev *streamdeck.Device
		err error
	}
	ch := make(chan result, 1)

	go func() {
		defer enumInFlight.Store(false)
		dev, err := streamdeck.GetDevice("")
		if err != nil {
			ch <- result{nil, err}
			return
		}
		if err := dev.Open(); err != nil {
			ch <- result{nil, err}
			return
		}
		ch <- result{dev, nil}
	}()

	select {
	case r := <-ch:
		if r.err != nil {
			return nil
		}
		return r.dev
	case <-time.After(timeout):
		log.Println("Device detection timed out (enumeration goroutine still in CGO)")
		// Goroutine is stuck in kernel - clean up if it ever returns.
		go func() {
			r := <-ch
			if r.dev != nil {
				log.Println("Late device arrival from timed-out enumeration, closing")
				r.dev.Close()
			}
		}()
		return nil
	}
}

// waitForHardwareDevice waits for a Stream Deck device using event-driven USB
// detection. The deviceArrivedCh fires when IOKit detects a matching HID device,
// so the common case settles immediately and costs no polling. Wake signals are
// kept as a fallback for sleep/wake edge cases, and a backing-off timer covers
// the case where the probe fails on an edge that will never repeat.
func waitForHardwareDevice(ctx context.Context, wakeCh <-chan struct{}, deviceArrivedCh <-chan struct{}) device.Device {
	const (
		deviceTimeout = 5 * time.Second

		// Bounds for the polling backstop below.
		retryMin = 1 * time.Second
		retryMax = 30 * time.Second
	)

	// First, try to get an already-connected device
	if dev := tryGetDeviceWithTimeout(deviceTimeout); dev != nil {
		return device.NewHardware(dev)
	}

	log.Println("Waiting for device...")

	// deviceArrivedCh and wakeCh are both edge-triggered, which leaves a hole:
	// a probe that fails at the instant of the edge waits forever for an edge
	// that cannot repeat while the deck stays plugged in. That is exactly what
	// happens when launchd respawns us before the previous process's exclusive
	// HID handle is released - the deck sits dark with the daemon apparently
	// healthy. Poll as a level-triggered backstop, backing off so an unplugged
	// deck doesn't mean enumerating IOKit every second indefinitely.
	retryDelay := retryMin

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-deviceArrivedCh:
			log.Println("USB device arrival detected, probing...")
			retryDelay = retryMin
		case <-time.After(retryDelay):
			// Quiet on purpose: this fires on a timer, and tryGetDeviceWithTimeout
			// logs the failures that are actually worth reading.
		case <-wakeCh:
			// After wake, USB devices may take several seconds to enumerate.
			// Retry multiple times with short delays instead of just checking once.
			log.Println("Wake signal received, probing for device...")
			for i := 0; i < 10; i++ {
				if dev := tryGetDeviceWithTimeout(deviceTimeout); dev != nil {
					log.Println("Device connected!")
					return device.NewHardware(dev)
				}
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(500 * time.Millisecond):
				}
			}
			log.Println("Device not found after wake, resuming wait...")
			retryDelay = retryMin
			continue
		}

		if dev := tryGetDeviceWithTimeout(deviceTimeout); dev != nil {
			log.Println("Device connected!")
			return device.NewHardware(dev)
		}

		if retryDelay *= 2; retryDelay > retryMax {
			retryDelay = retryMax
		}
	}
}

// Consecutive device-init timeouts are tracked on disk because each one ends
// the process, so an in-memory counter would always read one.
const (
	// A streak older than this is treated as unrelated history.
	initFailureWindow = 5 * time.Minute
	// Failures before we stop assuming a respawn will help and say so.
	initFailureLoud = 3
	// Ceiling on how long we stall before exiting.
	initBackoffMax = 2 * time.Minute
)

func initFailurePath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "belowdeck-init-failures")
}

// noteInitFailure records another init timeout and returns the length of the
// current streak.
func noteInitFailure() int {
	count := 0
	if b, err := os.ReadFile(initFailurePath()); err == nil {
		var n int
		var unix int64
		if _, err := fmt.Sscanf(strings.TrimSpace(string(b)), "%d %d", &n, &unix); err == nil {
			if time.Since(time.Unix(unix, 0)) < initFailureWindow {
				count = n
			}
		}
	}
	count++
	path := initFailurePath()
	// The cache directory is not guaranteed to exist. Without this the write
	// fails silently, the streak never advances, and the loud message that
	// tells a human to replug the device never fires.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Printf("Warning: cannot record init failure streak: %v", err)
		return count
	}
	if err := os.WriteFile(path,
		[]byte(fmt.Sprintf("%d %d", count, time.Now().Unix())), 0o644); err != nil {
		log.Printf("Warning: cannot record init failure streak: %v", err)
	}
	return count
}

func clearInitFailures() {
	_ = os.Remove(initFailurePath())
}

// initBackoff stalls a failing process before it exits. launchd already floors
// respawns at ten seconds, which is the right pace for a wedge a new process
// can clear and far too fast for one it cannot.
func initBackoff(failures int) time.Duration {
	if failures < initFailureLoud {
		return 0
	}
	d := time.Duration(failures-initFailureLoud+1) * 15 * time.Second
	if d > initBackoffMax {
		d = initBackoffMax
	}
	return d
}

// runWithDevice runs the coordinator with the given device until disconnect, wake, or context cancel.
func runWithDevice(ctx context.Context, cfg *config.Config, dev device.Device, wakeCh <-chan struct{}) {
	log.Printf("Connected to: %s", dev.GetModelName())

	// Set brightness and clear keys.
	//
	// These are the first writes after open, and they can block forever:
	// usbhid's setReport waits on a completion callback with no timeout, and a
	// runloop that came back wedged from sleep never delivers one. Observed in
	// the wild as a daemon parked 21 hours in SetBrightness with a dark deck.
	// Same wedge the close path below already guards against, so same remedy:
	// give up and let launchd hand us a fresh process with a fresh runloop.
	//
	// That remedy only works when the wedge lives in this process. When it
	// lives in the device, every fresh process blocks in exactly the same
	// place, and exiting turns one silent hang into an endless respawn loop.
	// Observed in the wild: six restarts in ninety seconds, all identical,
	// until the deck was physically unplugged. So count the streak, slow down
	// once it is clear that respawning is not helping, and say the one thing
	// that actually fixes it.
	initDone := make(chan struct{})
	go func() {
		dev.SetBrightness(80)
		dev.ForEachKey(func(key device.KeyID) error {
			return dev.ClearKey(key)
		})
		close(initDone)
	}()

	select {
	case <-initDone:
		clearInitFailures()
	case <-time.After(5 * time.Second):
		failures := noteInitFailure()
		if failures >= initFailureLoud {
			log.Printf("Device init timed out %d times in a row, each on a fresh process. "+
				"The Stream Deck's HID endpoint is wedged and respawning will not clear it: "+
				"unplug the device and plug it back in.", failures)
		} else {
			log.Println("Device init timed out, exiting for clean respawn")
		}
		if d := initBackoff(failures); d > 0 {
			log.Printf("Backing off %s before exiting", d)
			time.Sleep(d)
		}
		os.Exit(1)
	}

	// Create coordinator and modules fresh for each connection
	coord := coordinator.New(dev)

	np := nowplaying.New(dev)
	coord.RegisterModule(np, module.Resources{
		Keys:      []module.KeyID{module.Key5, module.Key6},
		StripRect: image.Rect(0, 0, 400, 100),
		Dials:     []module.DialID{module.Dial1, module.Dial2},
	})

	w := weather.New(dev, cfg)
	coord.RegisterModule(w, module.Resources{
		StripRect: image.Rect(400, 0, 800, 100),
	})

	ha := homeassistant.New(dev, cfg)
	coord.RegisterModule(ha, module.Resources{
		Keys:  []module.KeyID{module.Key1, module.Key2},
		Dials: []module.DialID{module.Dial4},
	})

	gh := github.New(dev)
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

	// Brief delay to let any pending USB I/O callbacks complete.
	// The usbhid library doesn't cancel ongoing I/O on close, so callbacks
	// can fire after close with stale context pointers causing crashes.
	time.Sleep(200 * time.Millisecond)

	// Close device - need to wait for this on wake to avoid race condition
	// where we try to reopen before close completes
	closeDone := make(chan struct{})
	go func() {
		dev.Close()
		close(closeDone)
	}()

	// If parent context is cancelled (shutdown signal), force exit
	// since device.Close() may block indefinitely.
	//
	// If close times out, the underlying usbhid runloop is wedged and
	// the Listen goroutine will never unblock from its inputCh/disconnectCh
	// select. Reconnecting in-process leaks one Listen goroutine (holding
	// IOKit resources) per cycle, eventually wedging enumeration entirely.
	// Exit instead and let launchd respawn cleanly.
	select {
	case <-ctx.Done():
		log.Println("Exiting...")
		os.Exit(0)
	case <-closeDone:
		// Device closed cleanly
	case <-time.After(3 * time.Second):
		log.Println("Device close timed out, exiting for clean respawn")
		os.Exit(1)
	}
}
