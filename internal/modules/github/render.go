package github

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

//go:embed icons/git-pull-request.svg
var iconPRSVG string

// Common colors
var (
	colorKeyBg   = color.RGBA{40, 40, 40, 255}
	colorWhite   = color.RGBA{255, 255, 255, 255}
	colorGreen   = color.RGBA{63, 185, 80, 255}  // GitHub green
	colorYellow  = color.RGBA{210, 153, 34, 255} // GitHub yellow
	colorOrange  = color.RGBA{219, 109, 40, 255} // GitHub orange
	colorDimGray = color.RGBA{110, 110, 110, 255}
)

const keySize = 72

// initFonts initializes the font faces for rendering.
func (m *Module) initFonts() error {
	ttBold, err := opentype.Parse(fontBold)
	if err != nil {
		return fmt.Errorf("failed to parse bold font: %w", err)
	}

	m.labelFace, err = opentype.NewFace(ttBold, &opentype.FaceOptions{
		Size:    9,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return fmt.Errorf("failed to create label face: %w", err)
	}

	m.numberFace, err = opentype.NewFace(ttBold, &opentype.FaceOptions{
		Size:    11,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return fmt.Errorf("failed to create number face: %w", err)
	}

	return nil
}

// renderPRStatsButton renders the PR stats button.
func (m *Module) renderPRStatsButton() image.Image {
	stats := m.getStats()

	img := image.NewRGBA(image.Rect(0, 0, keySize, keySize))

	// Background
	draw.Draw(img, img.Bounds(), &image.Uniform{colorKeyBg}, image.Point{}, draw.Src)

	// Draw small PR icon at top
	iconImg := renderSVGIcon(iconPRSVG, 20, colorWhite)
	iconX := (keySize - 20) / 2
	draw.Draw(img, image.Rect(iconX, 4, iconX+20, 24), iconImg, image.Point{}, draw.Over)

	// Draw stats as colored rows
	// Waiting (yellow)
	m.drawStatRow(img, 28, "Wait", stats.WaitingForReview, colorYellow)
	// Approved (green)
	m.drawStatRow(img, 42, "OK", stats.Approved, colorGreen)
	// Changes requested (orange)
	m.drawStatRow(img, 56, "Chg", stats.ChangesRequested, colorOrange)

	return img
}

// drawStatRow draws a stat row with label and count.
func (m *Module) drawStatRow(img *image.RGBA, y int, label string, count int, col color.Color) {
	// Draw colored indicator dot
	dotSize := 6
	dotX := 8
	dotY := y + 2
	for dy := 0; dy < dotSize; dy++ {
		for dx := 0; dx < dotSize; dx++ {
			img.Set(dotX+dx, dotY+dy, col)
		}
	}

	// Draw label
	m.drawText(img, label, 18, y+8, m.labelFace, colorDimGray)

	// Draw count on right
	countStr := fmt.Sprintf("%d", count)
	m.drawTextRight(img, countStr, keySize-8, y+8, m.numberFace, colorWhite)
}

// drawText draws text at the given position.
func (m *Module) drawText(img *image.RGBA, text string, x, y int, face font.Face, col color.Color) {
	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(col),
		Face: face,
		Dot:  fixed.Point26_6{X: fixed.I(x), Y: fixed.I(y)},
	}
	d.DrawString(text)
}

// drawTextRight draws text right-aligned at the given position.
func (m *Module) drawTextRight(img *image.RGBA, text string, rightX, y int, face font.Face, col color.Color) {
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

// renderSVGIcon renders an SVG string to an image with the given size and color.
func renderSVGIcon(svgContent string, size int, iconColor color.Color) image.Image {
	r, g, b, _ := iconColor.RGBA()
	hexColor := fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
	svgContent = strings.ReplaceAll(svgContent, "currentColor", hexColor)

	icon, err := oksvg.ReadIconStream(strings.NewReader(svgContent))
	if err != nil {
		log.Printf("Failed to parse SVG: %v", err)
		return image.NewRGBA(image.Rect(0, 0, size, size))
	}

	img := image.NewRGBA(image.Rect(0, 0, size, size))
	icon.SetTarget(0, 0, float64(size), float64(size))

	scanner := rasterx.NewScannerGV(size, size, img, img.Bounds())
	raster := rasterx.NewDasher(size, size, scanner)
	icon.Draw(raster, 1.0)

	return img
}
