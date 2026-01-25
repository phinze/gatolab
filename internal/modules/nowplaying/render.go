package nowplaying

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	_ "image/png"
	"log"
	"strings"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

//go:embed fonts/PublicSans-Bold.ttf
var fontBold []byte

//go:embed fonts/PublicSans-Regular.ttf
var fontRegular []byte

//go:embed icons/play.svg
var iconPlaySVG string

//go:embed icons/pause.svg
var iconPauseSVG string

//go:embed icons/info.svg
var iconInfoSVG string

// Common colors
var (
	colorLimeGreen   = color.RGBA{50, 205, 50, 255}
	colorOrange      = color.RGBA{255, 165, 0, 255}
	colorDeepSkyBlue = color.RGBA{0, 191, 255, 255}
	colorBackground  = color.RGBA{25, 25, 25, 255}
	colorKeyBg       = color.RGBA{40, 40, 40, 255}
	colorProgressBg  = color.RGBA{60, 60, 60, 255}
	colorArtist      = color.RGBA{180, 180, 180, 255}
	colorTime        = color.RGBA{120, 120, 120, 255}
)

// initFonts initializes the font faces for rendering.
func (m *Module) initFonts() error {
	// Parse bold font for title
	ttBold, err := opentype.Parse(fontBold)
	if err != nil {
		return fmt.Errorf("failed to parse bold font: %w", err)
	}

	m.titleFace, err = opentype.NewFace(ttBold, &opentype.FaceOptions{
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

	m.artistFace, err = opentype.NewFace(ttRegular, &opentype.FaceOptions{
		Size:    18,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return fmt.Errorf("failed to create artist face: %w", err)
	}

	return nil
}

// renderStrip renders the touch strip with album art, text, and progress bar.
func (m *Module) renderStrip(rect image.Rectangle, np *NowPlaying, artwork image.Image) image.Image {
	img := image.NewRGBA(rect)
	fullW := rect.Dx()
	h := rect.Dy()

	// Only use left half of the strip
	w := fullW / 2

	// Background - dark (full strip to clear any previous content)
	draw.Draw(img, img.Bounds(), &image.Uniform{colorBackground}, image.Point{}, draw.Src)

	// Layout for left half: [Art full height] [gap] [Text + progress]
	artSize := h // Full height bleed
	textX := artSize + 8
	progressH := 5
	progressMargin := 8

	// Draw album art thumbnail on left, full bleed
	if artwork != nil {
		artRect := image.Rect(0, 0, artSize, artSize)
		thumb := scaleImageSquare(artwork, artSize)
		draw.Draw(img, artRect, thumb, image.Point{}, draw.Over)
	}

	// Draw title (bold)
	if np.Title != "" {
		m.drawText(img, np.Title, textX, 30, m.titleFace, color.White, w-textX-10)
	}

	// Draw artist (regular, smaller, gray)
	if np.Artist != "" {
		m.drawText(img, np.Artist, textX, 54, m.artistFace, colorArtist, w-textX-10)
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
	progressRect := image.Rect(textX, h-progressMargin-progressH, w-10, h-progressMargin)
	draw.Draw(img, progressRect, &image.Uniform{colorProgressBg}, image.Point{}, draw.Src)

	// Progress bar fill
	progressColor := colorLimeGreen
	if !np.Playing {
		progressColor = colorOrange
	}
	progressW := int(float64(progressRect.Dx()) * progress)
	progressFill := image.Rect(textX, h-progressMargin-progressH, textX+progressW, h-progressMargin)
	draw.Draw(img, progressFill, &image.Uniform{progressColor}, image.Point{}, draw.Src)

	// Draw time (elapsed / total) above progress bar, right-aligned
	if durationMicros > 0 {
		elapsed := formatDurationMicros(elapsedMicros)
		total := formatDurationMicros(durationMicros)
		timeStr := fmt.Sprintf("%s / %s", elapsed, total)
		m.drawTextRightAligned(img, timeStr, w-10, h-progressMargin-progressH-6, m.artistFace, colorTime)
	}

	return img
}

// renderSVGIcon renders an SVG string to an image with the given size and color.
func renderSVGIcon(svgContent string, size int, iconColor color.Color) image.Image {
	// Replace currentColor with the actual color
	r, g, b, _ := iconColor.RGBA()
	hexColor := fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
	svgContent = strings.ReplaceAll(svgContent, "currentColor", hexColor)

	// Parse SVG
	icon, err := oksvg.ReadIconStream(strings.NewReader(svgContent))
	if err != nil {
		log.Printf("Failed to parse SVG: %v", err)
		return image.NewRGBA(image.Rect(0, 0, size, size))
	}

	// Create output image with dark background
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.Draw(img, img.Bounds(), &image.Uniform{colorKeyBg}, image.Point{}, draw.Src)

	// Calculate scaling and centering
	iconSize := float64(size) * 0.6 // Icon takes 60% of button
	padding := (float64(size) - iconSize) / 2

	icon.SetTarget(padding, padding, iconSize, iconSize)

	// Render to image
	scanner := rasterx.NewScannerGV(size, size, img, img.Bounds())
	raster := rasterx.NewDasher(size, size, scanner)
	icon.Draw(raster, 1.0)

	return img
}

// drawText draws text with automatic truncation if it exceeds maxWidth.
func (m *Module) drawText(img *image.RGBA, text string, x, y int, face font.Face, col color.Color, maxWidth int) {
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

// drawTextRightAligned draws text aligned to the right edge.
func (m *Module) drawTextRightAligned(img *image.RGBA, text string, rightX, y int, face font.Face, col color.Color) {
	// Measure text width and draw so it ends at rightX
	width := font.MeasureString(face, text).Ceil()
	x := rightX - width

	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(col),
		Face: face,
		Dot:  fixed.Point26_6{X: fixed.I(x), Y: fixed.I(y)},
	}
	d.DrawString(text)
}

// truncateText truncates text to fit within maxWidth, adding ellipsis if needed.
func truncateText(text string, face font.Face, maxWidth int) string {
	if maxWidth <= 0 {
		return text
	}

	ellipsis := "..."

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

// scaleImageSquare scales and crops an image to a square of the given size.
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

// decodeArtwork decodes base64 artwork data to an image.
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

// formatDurationMicros formats microseconds as m:ss.
func formatDurationMicros(micros int64) string {
	totalSeconds := micros / 1000000
	m := totalSeconds / 60
	s := totalSeconds % 60
	return fmt.Sprintf("%d:%02d", m, s)
}

// formatSeekPosition formats microseconds as seconds for the seek command.
func formatSeekPosition(micros int64) string {
	return fmt.Sprintf("%.1f", float64(micros)/1000000)
}
