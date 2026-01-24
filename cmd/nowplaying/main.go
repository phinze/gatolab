package main

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	_ "image/png"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"golang.org/x/image/colornames"
	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"rafaelmartins.com/p/streamdeck"
)

//go:embed fonts/PublicSans-Bold.ttf
var fontBold []byte

//go:embed fonts/PublicSans-Regular.ttf
var fontRegular []byte

var (
	titleFace   font.Face
	artistFace  font.Face
)

// NowPlaying represents the media-control JSON output (with --micros flag)
type NowPlaying struct {
	Title                string `json:"title"`
	Artist               string `json:"artist"`
	Album                string `json:"album"`
	DurationMicros       int64  `json:"durationMicros"`
	ElapsedTimeMicros    int64  `json:"elapsedTimeMicros"`
	TimestampEpochMicros int64  `json:"timestampEpochMicros"`
	Playing              bool   `json:"playing"`
	ArtworkData          string `json:"artworkData"`
	ArtworkMime          string `json:"artworkMimeType"`
}

// Live state updated from stream
type LiveState struct {
	sync.RWMutex
	NowPlaying
}

var liveState = &LiveState{}

// Layout:
// [1:Prev] [2:Play] [3:Art] [4:Art]
// [5:Next] [6:Info] [7:Art] [8:Art]

var (
	keyPrev = streamdeck.KEY_1
	keyPlay = streamdeck.KEY_2
	keyNext = streamdeck.KEY_5
	keyInfo = streamdeck.KEY_6

	// Album art keys (2x2 grid on right side)
	artKeys = []streamdeck.KeyID{
		streamdeck.KEY_3, streamdeck.KEY_4, // top row
		streamdeck.KEY_7, streamdeck.KEY_8, // bottom row
	}
)

func initFonts() error {
	// Parse bold font for title
	ttBold, err := opentype.Parse(fontBold)
	if err != nil {
		return fmt.Errorf("failed to parse bold font: %w", err)
	}

	titleFace, err = opentype.NewFace(ttBold, &opentype.FaceOptions{
		Size:    24,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return fmt.Errorf("failed to create title face: %w", err)
	}

	// Parse regular font for artist
	ttRegular, err := opentype.Parse(fontRegular)
	if err != nil {
		return fmt.Errorf("failed to parse regular font: %w", err)
	}

	artistFace, err = opentype.NewFace(ttRegular, &opentype.FaceOptions{
		Size:    18,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return fmt.Errorf("failed to create artist face: %w", err)
	}

	return nil
}

func main() {
	log.Println("=== Stream Deck Now Playing ===")
	log.Println("Press Ctrl+C to exit")

	// Initialize fonts
	if err := initFonts(); err != nil {
		log.Fatalf("Failed to initialize fonts: %v", err)
	}
	log.Println("Fonts loaded")

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

	// Draw control icons
	drawControlIcons(device)

	// Setup key handlers
	setupKeyControls(device)

	// Setup dial controls
	setupDialControls(device)

	// Context for cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Start listening for device events
	errChan := make(chan error, 1)
	go func() {
		if err := device.Listen(errChan); err != nil {
			errChan <- err
		}
	}()

	// Start media-control stream in background
	go startMediaStream(ctx)

	// Update display every 500ms for smooth progress
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	var lastArtwork string
	var lastPlaying bool

	log.Println("Ready! Controls on left, album art on right")

	for {
		select {
		case <-ctx.Done():
			return
		case <-sigChan:
			log.Println("\nShutting down...")
			cancel()
			return
		case err := <-errChan:
			if err != nil {
				log.Printf("Device error: %v", err)
			}
		case <-ticker.C:
			updateDisplay(device, &lastArtwork, &lastPlaying)
		}
	}
}

// StreamPayload wraps the stream JSON structure with raw payload for proper merging
type StreamPayload struct {
	Diff    bool            `json:"diff"`
	Payload json.RawMessage `json:"payload"`
}

func startMediaStream(ctx context.Context) {
	cmd := exec.CommandContext(ctx, "media-control", "stream", "--micros")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Printf("Failed to get stdout pipe: %v", err)
		return
	}

	if err := cmd.Start(); err != nil {
		log.Printf("Failed to start media-control stream: %v", err)
		return
	}

	log.Println("Started media-control stream")

	scanner := bufio.NewScanner(stdout)
	// Increase buffer size for large artwork payloads
	buf := make([]byte, 0, 1024*1024) // 1MB buffer
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		var envelope StreamPayload
		if err := json.Unmarshal(line, &envelope); err != nil {
			continue
		}

		// Parse payload as a map to see which fields are present
		var payloadMap map[string]interface{}
		if err := json.Unmarshal(envelope.Payload, &payloadMap); err != nil {
			continue
		}

		liveState.Lock()
		if !envelope.Diff && len(payloadMap) == 0 {
			// Reset to defaults
			liveState.NowPlaying = NowPlaying{
				Title:                "?",
				Artist:               "?",
				TimestampEpochMicros: time.Now().UnixMicro(),
			}
		} else {
			// Merge only fields that are present in the payload
			mergePayloadMap(&liveState.NowPlaying, payloadMap)
		}
		liveState.Unlock()
	}

	if err := scanner.Err(); err != nil {
		log.Printf("Scanner error: %v", err)
	}

	cmd.Wait()
}

func mergePayloadMap(dst *NowPlaying, src map[string]interface{}) {
	if v, ok := src["title"].(string); ok {
		dst.Title = v
	}
	if v, ok := src["artist"].(string); ok {
		dst.Artist = v
	}
	if v, ok := src["album"].(string); ok {
		dst.Album = v
	}
	if v, ok := src["durationMicros"].(float64); ok {
		dst.DurationMicros = int64(v)
	}
	if v, ok := src["elapsedTimeMicros"].(float64); ok {
		dst.ElapsedTimeMicros = int64(v)
	}
	if v, ok := src["timestampEpochMicros"].(float64); ok {
		dst.TimestampEpochMicros = int64(v)
	}
	// Only update playing if it's actually present in the payload
	if v, ok := src["playing"].(bool); ok {
		dst.Playing = v
	}
	if v, ok := src["artworkData"].(string); ok {
		dst.ArtworkData = v
	}
	if v, ok := src["artworkMimeType"].(string); ok {
		dst.ArtworkMime = v
	}
}

func getLiveState() NowPlaying {
	liveState.RLock()
	defer liveState.RUnlock()
	return liveState.NowPlaying
}

// Calculate live elapsed time based on timestamp and playing state
func getLiveElapsedMicros(np *NowPlaying) int64 {
	if !np.Playing {
		return np.ElapsedTimeMicros
	}
	// Calculate: elapsed + (now - timestamp)
	nowMicros := time.Now().UnixMicro()
	timeDiff := nowMicros - np.TimestampEpochMicros
	return np.ElapsedTimeMicros + timeDiff
}

// Cache for decoded artwork
var cachedArtwork image.Image
var cachedArtworkHash string

func updateDisplay(device *streamdeck.Device, lastArtwork *string, lastPlaying *bool) {
	np := getLiveState()

	// Update artwork if changed
	if np.ArtworkData != "" && np.ArtworkData != *lastArtwork {
		*lastArtwork = np.ArtworkData
		updateArtwork(device, np.ArtworkData)
		// Cache decoded artwork for touch strip
		if img := decodeArtwork(np.ArtworkData); img != nil {
			cachedArtwork = img
			cachedArtworkHash = np.ArtworkData
		}
		log.Printf("Track: %s - %s", np.Artist, np.Title)
	}

	// Update play/pause icon if state changed
	if np.Playing != *lastPlaying {
		*lastPlaying = np.Playing
		drawPlayPauseIcon(device, np.Playing)
	}

	// Update touch strip with album art, text, and progress
	updateTouchStrip(device, &np)
}

func decodeArtwork(artworkBase64 string) image.Image {
	imgData, err := base64.StdEncoding.DecodeString(artworkBase64)
	if err != nil {
		return nil
	}
	img, _, err := image.Decode(bytes.NewReader(imgData))
	if err != nil {
		return nil
	}
	return img
}

func updateArtwork(device *streamdeck.Device, artworkBase64 string) {
	img := decodeArtwork(artworkBase64)
	if img == nil {
		log.Printf("Failed to decode artwork")
		return
	}

	keyRect, err := device.GetKeyImageRectangle()
	if err != nil {
		log.Printf("Failed to get key size: %v", err)
		return
	}

	keyW := keyRect.Dx()
	keyH := keyRect.Dy()

	// Scale album art to 2x2 key size (square)
	totalSize := keyW * 2
	scaled := scaleImageSquare(img, totalSize)

	// Split into 4 tiles for the 2x2 grid
	positions := []image.Point{
		{0, 0}, {1, 0},
		{0, 1}, {1, 1},
	}

	for i, key := range artKeys {
		pos := positions[i]
		tile := image.NewRGBA(image.Rect(0, 0, keyW, keyH))
		srcRect := image.Rect(pos.X*keyW, pos.Y*keyH, (pos.X+1)*keyW, (pos.Y+1)*keyH)
		draw.Draw(tile, tile.Bounds(), scaled, srcRect.Min, draw.Src)

		if err := device.SetKeyImage(key, tile); err != nil {
			log.Printf("Failed to set key %d image: %v", key, err)
		}
	}

	log.Println("Updated album artwork")
}

func scaleImageSquare(src image.Image, size int) image.Image {
	srcBounds := src.Bounds()
	srcW := srcBounds.Dx()
	srcH := srcBounds.Dy()

	var cropRect image.Rectangle
	if srcW > srcH {
		offset := (srcW - srcH) / 2
		cropRect = image.Rect(offset, 0, offset+srcH, srcH)
	} else {
		offset := (srcH - srcW) / 2
		cropRect = image.Rect(0, offset, srcW, offset+srcW)
	}

	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, cropRect, draw.Over, nil)
	return dst
}

func updateTouchStrip(device *streamdeck.Device, np *NowPlaying) {
	if !device.GetTouchStripSupported() {
		return
	}

	rect, err := device.GetTouchStripImageRectangle()
	if err != nil {
		return
	}

	img := image.NewRGBA(rect)
	w := rect.Dx()
	h := rect.Dy()

	// Background - dark
	bgColor := color.RGBA{25, 25, 25, 255}
	draw.Draw(img, img.Bounds(), &image.Uniform{bgColor}, image.Point{}, draw.Src)

	// Layout: [Art 80px] [10px gap] [Text area] [Progress bar at bottom]
	artSize := 80
	artMargin := 10
	textX := artSize + artMargin + 10
	progressH := 6
	progressMargin := 10 // bump up from bottom edge

	// Draw album art thumbnail on left
	if cachedArtwork != nil {
		artRect := image.Rect(artMargin, (h-artSize)/2, artMargin+artSize, (h+artSize)/2)
		thumb := scaleImageSquare(cachedArtwork, artSize)
		draw.Draw(img, artRect, thumb, image.Point{}, draw.Over)
	}

	// Draw title (bold, larger)
	if np.Title != "" {
		titleColor := color.White
		drawText(img, np.Title, textX, 32, titleFace, titleColor, w-textX-20)
	}

	// Draw artist (regular, smaller, gray)
	if np.Artist != "" {
		artistColor := color.RGBA{180, 180, 180, 255}
		drawText(img, np.Artist, textX, 58, artistFace, artistColor, w-textX-20)
	}

	// Calculate live elapsed time
	elapsedMicros := getLiveElapsedMicros(np)
	durationMicros := np.DurationMicros

	// Draw progress bar at bottom
	progress := 0.0
	if durationMicros > 0 {
		progress = float64(elapsedMicros) / float64(durationMicros)
		if progress > 1.0 {
			progress = 1.0
		}
	}

	// Progress bar background
	progressBg := color.RGBA{60, 60, 60, 255}
	progressRect := image.Rect(textX, h-progressMargin-progressH, w-20, h-progressMargin)
	draw.Draw(img, progressRect, &image.Uniform{progressBg}, image.Point{}, draw.Src)

	// Progress bar fill
	progressColor := colornames.Limegreen
	if !np.Playing {
		progressColor = colornames.Orange
	}
	progressW := int(float64(progressRect.Dx()) * progress)
	progressFill := image.Rect(textX, h-progressMargin-progressH, textX+progressW, h-progressMargin)
	draw.Draw(img, progressFill, &image.Uniform{progressColor}, image.Point{}, draw.Src)

	// Draw time info
	if durationMicros > 0 {
		elapsed := formatDurationMicros(elapsedMicros)
		total := formatDurationMicros(durationMicros)
		timeStr := fmt.Sprintf("%s / %s", elapsed, total)
		timeColor := color.RGBA{120, 120, 120, 255}
		// Draw right-aligned near progress bar
		drawText(img, timeStr, w-100, h-progressMargin-progressH-8, artistFace, timeColor, 90)
	}

	device.SetTouchStripImage(img)
}

func formatDurationMicros(micros int64) string {
	totalSeconds := micros / 1000000
	m := totalSeconds / 60
	s := totalSeconds % 60
	return fmt.Sprintf("%d:%02d", m, s)
}

func drawText(img *image.RGBA, text string, x, y int, face font.Face, col color.Color, maxWidth int) {
	// Truncate text if too long
	truncated := truncateText(text, face, maxWidth)

	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(col),
		Face: face,
		Dot:  fixed.Point26_6{X: fixed.I(x), Y: fixed.I(y)},
	}
	d.DrawString(truncated)
}

func truncateText(text string, face font.Face, maxWidth int) string {
	if maxWidth <= 0 {
		return text
	}

	ellipsis := "…"

	width := font.MeasureString(face, text).Ceil()
	if width <= maxWidth {
		return text
	}

	// Binary search for the right length
	runes := []rune(text)
	for i := len(runes); i > 0; i-- {
		truncated := string(runes[:i]) + ellipsis
		w := font.MeasureString(face, truncated).Ceil()
		if w <= maxWidth {
			return truncated
		}
	}

	return ellipsis
}

func drawControlIcons(device *streamdeck.Device) {
	keyRect, _ := device.GetKeyImageRectangle()
	size := keyRect.Dx()

	device.SetKeyImage(keyPrev, drawPrevIcon(size))
	drawPlayPauseIcon(device, false)
	device.SetKeyImage(keyNext, drawNextIcon(size))
	device.SetKeyImage(keyInfo, drawInfoIcon(size))
}

func drawPlayPauseIcon(device *streamdeck.Device, playing bool) {
	keyRect, _ := device.GetKeyImageRectangle()
	size := keyRect.Dx()

	if playing {
		device.SetKeyImage(keyPlay, drawPauseIcon(size))
	} else {
		device.SetKeyImage(keyPlay, drawPlayIcon(size))
	}
}

func drawPrevIcon(size int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	fillRect(img, color.RGBA{40, 40, 40, 255})

	iconColor := colornames.White
	center := size / 2
	iconSize := size / 3

	barW := iconSize / 4
	fillRectArea(img, iconColor, center-iconSize, center-iconSize/2, barW, iconSize)
	drawTriangleLeft(img, iconColor, center-iconSize/2, center, iconSize/2)
	drawTriangleLeft(img, iconColor, center+iconSize/4, center, iconSize/2)

	return img
}

func drawNextIcon(size int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	fillRect(img, color.RGBA{40, 40, 40, 255})

	iconColor := colornames.White
	center := size / 2
	iconSize := size / 3

	drawTriangleRight(img, iconColor, center-iconSize/2, center, iconSize/2)
	drawTriangleRight(img, iconColor, center+iconSize/4, center, iconSize/2)
	barW := iconSize / 4
	fillRectArea(img, iconColor, center+iconSize-barW, center-iconSize/2, barW, iconSize)

	return img
}

func drawPlayIcon(size int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	fillRect(img, color.RGBA{40, 40, 40, 255})

	iconColor := colornames.Limegreen
	center := size / 2
	iconSize := size / 3

	drawTriangleRight(img, iconColor, center-iconSize/3, center, iconSize)

	return img
}

func drawPauseIcon(size int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	fillRect(img, color.RGBA{40, 40, 40, 255})

	iconColor := colornames.Orange
	center := size / 2
	barW := size / 8
	barH := size / 3
	gap := size / 8

	fillRectArea(img, iconColor, center-gap-barW, center-barH/2, barW, barH)
	fillRectArea(img, iconColor, center+gap, center-barH/2, barW, barH)

	return img
}

func drawInfoIcon(size int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	fillRect(img, color.RGBA{40, 40, 40, 255})

	iconColor := colornames.Deepskyblue
	center := size / 2

	dotR := size / 12
	fillCircle(img, iconColor, center, center-size/5, dotR)

	stemW := size / 8
	stemH := size / 4
	fillRectArea(img, iconColor, center-stemW/2, center-size/12, stemW, stemH)

	return img
}

func fillRect(img *image.RGBA, c color.Color) {
	draw.Draw(img, img.Bounds(), &image.Uniform{c}, image.Point{}, draw.Src)
}

func fillRectArea(img *image.RGBA, c color.Color, x, y, w, h int) {
	rect := image.Rect(x, y, x+w, y+h)
	draw.Draw(img, rect, &image.Uniform{c}, image.Point{}, draw.Src)
}

func fillCircle(img *image.RGBA, c color.Color, cx, cy, r int) {
	for y := cy - r; y <= cy+r; y++ {
		for x := cx - r; x <= cx+r; x++ {
			dx := x - cx
			dy := y - cy
			if dx*dx+dy*dy <= r*r {
				img.Set(x, y, c)
			}
		}
	}
}

func drawTriangleRight(img *image.RGBA, c color.Color, x, cy, size int) {
	for i := 0; i < size; i++ {
		halfH := (size - i) * size / (2 * size)
		for dy := -halfH; dy <= halfH; dy++ {
			img.Set(x+i, cy+dy, c)
		}
	}
}

func drawTriangleLeft(img *image.RGBA, c color.Color, x, cy, size int) {
	for i := 0; i < size; i++ {
		halfH := (size - i) * size / (2 * size)
		for dy := -halfH; dy <= halfH; dy++ {
			img.Set(x-i, cy+dy, c)
		}
	}
}

func setupKeyControls(device *streamdeck.Device) {
	device.AddKeyHandler(keyPrev, func(d *streamdeck.Device, k *streamdeck.Key) error {
		log.Println("Key: Previous track")
		exec.Command("media-control", "previous-track").Run()
		k.WaitForRelease()
		return nil
	})

	device.AddKeyHandler(keyPlay, func(d *streamdeck.Device, k *streamdeck.Key) error {
		log.Println("Key: Toggle play/pause")
		go exec.Command("media-control", "toggle-play-pause").Run()
		k.WaitForRelease()
		return nil
	})

	device.AddKeyHandler(keyNext, func(d *streamdeck.Device, k *streamdeck.Key) error {
		log.Println("Key: Next track")
		exec.Command("media-control", "next-track").Run()
		k.WaitForRelease()
		return nil
	})

	device.AddKeyHandler(keyInfo, func(d *streamdeck.Device, k *streamdeck.Key) error {
		np := getLiveState()
		log.Printf("Info: %s - %s (%s)", np.Artist, np.Title, np.Album)
		k.WaitForRelease()
		return nil
	})

	log.Println("Key controls configured")
}

func setupDialControls(device *streamdeck.Device) {
	if device.GetDialCount() == 0 {
		return
	}

	device.AddDialRotateHandler(streamdeck.DIAL_1, func(d *streamdeck.Device, di *streamdeck.Dial, delta int8) error {
		seekAmount := int64(delta) * 5 * 1000000 // 5 seconds in micros
		log.Printf("Dial: Seeking %+d seconds", delta*5)

		np := getLiveState()
		currentPos := getLiveElapsedMicros(&np)

		newPos := currentPos + seekAmount
		if newPos < 0 {
			newPos = 0
		}
		if newPos > np.DurationMicros {
			newPos = np.DurationMicros
		}

		// media-control seek takes seconds
		cmd := exec.Command("media-control", "seek", fmt.Sprintf("%.1f", float64(newPos)/1000000))
		cmd.Run()
		return nil
	})

	device.AddDialSwitchHandler(streamdeck.DIAL_1, func(d *streamdeck.Device, di *streamdeck.Dial) error {
		log.Println("Dial: Toggle play/pause")
		go exec.Command("media-control", "toggle-play-pause").Run()
		di.WaitForRelease()
		return nil
	})

	device.AddDialRotateHandler(streamdeck.DIAL_2, func(d *streamdeck.Device, di *streamdeck.Dial, delta int8) error {
		if delta < 0 {
			log.Println("Dial: Previous track")
			exec.Command("media-control", "previous-track").Run()
		} else {
			log.Println("Dial: Next track")
			exec.Command("media-control", "next-track").Run()
		}
		return nil
	})

	log.Println("Dial controls: D1=seek/play-pause, D2=prev/next")
}
