package homeassistant

import (
	_ "embed"
	"fmt"
	"image"
	"image/color"
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

//go:embed icons/lamp-desk.svg
var iconLampDeskSVG string

//go:embed icons/circle.svg
var iconCircleSVG string

// Common colors
var (
	colorKeyBg    = color.RGBA{40, 40, 40, 255}
	colorWhite    = color.RGBA{255, 255, 255, 255}
	colorAmber    = color.RGBA{255, 191, 0, 255}
	colorLightRay = color.RGBA{255, 245, 180, 255}
	colorDimGray  = color.RGBA{80, 80, 80, 255}
)

const keySize = 72

// initFonts initializes the font faces for rendering.
func (m *Module) initFonts() error {
	ttBold, err := opentype.Parse(fontBold)
	if err != nil {
		return fmt.Errorf("failed to parse bold font: %w", err)
	}

	m.labelFace, err = opentype.NewFace(ttBold, &opentype.FaceOptions{
		Size:    11,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return fmt.Errorf("failed to create label face: %w", err)
	}

	return nil
}

// renderOfficeTimeButton renders the Office toggle button.
func (m *Module) renderOfficeTimeButton() image.Image {
	state := m.getOfficeLightState()

	img := image.NewRGBA(image.Rect(0, 0, keySize, keySize))

	// Background
	draw.Draw(img, img.Bounds(), &image.Uniform{colorKeyBg}, image.Point{}, draw.Src)

	// Choose icon color and label based on state
	var iconColor color.Color
	var labelText string

	if state.On {
		iconColor = colorAmber
		labelText = "Office On"
	} else {
		iconColor = colorDimGray
		labelText = "Office Off"
	}

	// Draw icon in upper portion
	iconImg := renderSVGIcon(iconLampDeskSVG, 40, iconColor)
	iconX := (keySize - 40) / 2
	iconY := 8
	draw.Draw(img, image.Rect(iconX, iconY, iconX+40, iconY+40), iconImg, image.Point{}, draw.Over)

	// Draw light rays when on
	if state.On {
		drawLightRays(img, colorLightRay)
	}

	// Draw label at bottom
	m.drawTextCentered(img, labelText, keySize/2, 62, m.labelFace, colorWhite)

	return img
}

// drawLightRays draws light rays emanating from the lamp's 45° shade surface.
func drawLightRays(img *image.RGBA, col color.Color) {
	// The lamp shade is a 45° diagonal line in the upper right of the icon
	// Icon is 40x40 at position (16,8), so lamp shade runs roughly from (44,12) to (52,20)
	// Rays emanate perpendicular to this surface (also at 45°, pointing upper-right)

	// Three parallel rays at 45° going SE, offset perpendicular (NE) from each other
	// Each ray: 6 pixel diagonal (dx=5, dy=5)
	// Perpendicular spacing: offset by (+5, -5) between rays
	// Shifted SW to center on lamp shade opening
	rays := []struct {
		x1, y1, x2, y2 int
	}{
		{43, 33, 48, 38},  // closest to lamp
		{48, 28, 53, 33},  // middle ray
		{53, 23, 58, 28},  // furthest ray
	}

	for _, r := range rays {
		drawLine(img, r.x1, r.y1, r.x2, r.y2, col)
	}
}

// drawLine draws a line using Bresenham's algorithm.
func drawLine(img *image.RGBA, x0, y0, x1, y1 int, col color.Color) {
	dx := abs(x1 - x0)
	dy := -abs(y1 - y0)
	sx := 1
	if x0 >= x1 {
		sx = -1
	}
	sy := 1
	if y0 >= y1 {
		sy = -1
	}
	err := dx + dy

	for {
		if x0 >= 0 && x0 < img.Bounds().Dx() && y0 >= 0 && y0 < img.Bounds().Dy() {
			img.Set(x0, y0, col)
		}
		if x0 == x1 && y0 == y1 {
			break
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

// abs returns the absolute value of x.
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// renderRingLightButton renders the Ring Light toggle button.
func (m *Module) renderRingLightButton() image.Image {
	state := m.getRingLightState()

	img := image.NewRGBA(image.Rect(0, 0, keySize, keySize))

	// Background
	draw.Draw(img, img.Bounds(), &image.Uniform{colorKeyBg}, image.Point{}, draw.Src)

	// Choose icon color based on state
	var iconColor color.Color
	var labelText string

	if state.On {
		// Scale brightness (0-255) to color intensity
		brightness := state.Brightness
		if brightness == 0 {
			brightness = 255 // Default to full if on but no brightness reported
		}
		// Create a warm white color scaled by brightness
		iconColor = color.RGBA{brightness, brightness, uint8(float64(brightness) * 0.9), 255}
		// Show percentage rounded to nearest 10
		pct := int(float64(brightness)/255.0*100+5) / 10 * 10
		labelText = fmt.Sprintf("Ring %d%%", pct)
	} else {
		iconColor = colorDimGray
		labelText = "Ring Light"
	}

	// Draw icon in upper portion
	iconImg := renderSVGIcon(iconCircleSVG, 40, iconColor)
	iconX := (keySize - 40) / 2
	iconY := 8
	draw.Draw(img, image.Rect(iconX, iconY, iconX+40, iconY+40), iconImg, image.Point{}, draw.Over)

	// Draw label at bottom
	m.drawTextCentered(img, labelText, keySize/2, 62, m.labelFace, colorWhite)

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

	// Create output image with transparent background
	img := image.NewRGBA(image.Rect(0, 0, size, size))

	// Set target size
	icon.SetTarget(0, 0, float64(size), float64(size))

	// Render to image
	scanner := rasterx.NewScannerGV(size, size, img, img.Bounds())
	raster := rasterx.NewDasher(size, size, scanner)
	icon.Draw(raster, 1.0)

	return img
}

// drawTextCentered draws text centered horizontally at the given position.
func (m *Module) drawTextCentered(img *image.RGBA, text string, centerX, y int, face font.Face, col color.Color) {
	width := font.MeasureString(face, text).Ceil()
	x := centerX - width/2

	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(col),
		Face: face,
		Dot:  fixed.Point26_6{X: fixed.I(x), Y: fixed.I(y)},
	}
	d.DrawString(text)
}
