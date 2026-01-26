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

// Common colors
var (
	colorKeyBg = color.RGBA{40, 40, 40, 255}
	colorWhite = color.RGBA{255, 255, 255, 255}
	colorAmber = color.RGBA{255, 191, 0, 255}
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

// renderOfficeTimeButton renders the Office Time button.
func (m *Module) renderOfficeTimeButton() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, keySize, keySize))

	// Background
	draw.Draw(img, img.Bounds(), &image.Uniform{colorKeyBg}, image.Point{}, draw.Src)

	// Draw icon in upper portion
	iconImg := renderSVGIcon(iconLampDeskSVG, 40, colorAmber)
	iconX := (keySize - 40) / 2
	iconY := 8
	draw.Draw(img, image.Rect(iconX, iconY, iconX+40, iconY+40), iconImg, image.Point{}, draw.Over)

	// Draw label at bottom
	m.drawTextCentered(img, "Office Time", keySize/2, 62, m.labelFace, colorWhite)

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
