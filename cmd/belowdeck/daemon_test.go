package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The init-failure streak only advances when the Stream Deck's HID endpoint is
// wedged, which is rare and impossible to provoke on demand. Cover it here so
// the logic does not rot between incidents.

func TestInitFailureStreakAccumulates(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	clearInitFailures()

	for want := 1; want <= 4; want++ {
		if got := noteInitFailure(); got != want {
			t.Fatalf("noteInitFailure() = %d, want %d", got, want)
		}
	}
}

func TestSuccessfulInitClearsStreak(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	clearInitFailures()

	noteInitFailure()
	noteInitFailure()
	clearInitFailures()

	if got := noteInitFailure(); got != 1 {
		t.Fatalf("after clear, noteInitFailure() = %d, want 1", got)
	}
}

func TestStaleStreakStartsOver(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	clearInitFailures()

	// A streak from well outside the window is unrelated history, not a
	// continuing failure.
	stale := time.Now().Add(-2 * initFailureWindow).Unix()
	if err := os.MkdirAll(filepath.Dir(initFailurePath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(initFailurePath(),
		[]byte(fmt.Sprintf("%d %d", 7, stale)), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := noteInitFailure(); got != 1 {
		t.Fatalf("stale streak continued: got %d, want 1", got)
	}
}

func TestInitBackoffStaysQuietThenRamps(t *testing.T) {
	// Below the loud threshold we exit immediately, so a wedge a respawn can
	// actually clear still recovers in seconds.
	for f := 1; f < initFailureLoud; f++ {
		if d := initBackoff(f); d != 0 {
			t.Fatalf("initBackoff(%d) = %s, want 0", f, d)
		}
	}
	if d := initBackoff(initFailureLoud); d <= 0 {
		t.Fatalf("initBackoff(%d) = %s, want > 0", initFailureLoud, d)
	}
	// And it must not grow without bound.
	if d := initBackoff(1000); d != initBackoffMax {
		t.Fatalf("initBackoff(1000) = %s, want %s", d, initBackoffMax)
	}
	if a, b := initBackoff(initFailureLoud), initBackoff(initFailureLoud+1); b <= a {
		t.Fatalf("backoff did not increase: %s then %s", a, b)
	}
}
