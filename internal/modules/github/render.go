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

//go:embed icons/github.svg
var iconGitHubSVG string

// Common colors
var (
	colorKeyBg   = color.RGBA{40, 40, 40, 255}
	colorWhite   = color.RGBA{255, 255, 255, 255}
	colorGreen   = color.RGBA{63, 185, 80, 255}  // GitHub green
	colorYellow  = color.RGBA{210, 153, 34, 255} // GitHub yellow
	colorOrange  = color.RGBA{219, 109, 40, 255} // GitHub orange
	colorRed     = color.RGBA{248, 81, 73, 255}  // GitHub red for CI failures
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

	m.overlayFace, err = opentype.NewFace(ttBold, &opentype.FaceOptions{
		Size:    10,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return fmt.Errorf("failed to create overlay face: %w", err)
	}

	m.stripTitleFace, err = opentype.NewFace(ttBold, &opentype.FaceOptions{
		Size:    18,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return fmt.Errorf("failed to create strip title face: %w", err)
	}

	m.stripLabelFace, err = opentype.NewFace(ttBold, &opentype.FaceOptions{
		Size:    14,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return fmt.Errorf("failed to create strip label face: %w", err)
	}

	return nil
}

// renderPRStatsButton renders the PR stats button.
func (m *Module) renderPRStatsButton() image.Image {
	stats := m.getStats()

	img := image.NewRGBA(image.Rect(0, 0, keySize, keySize))

	// Background
	draw.Draw(img, img.Bounds(), &image.Uniform{colorKeyBg}, image.Point{}, draw.Src)

	var rowY int
	if stats.CIFailed > 0 {
		// Show fail row at top instead of icon
		m.drawStatRow(img, 14, "Fail", stats.CIFailed, colorRed)
		rowY = 28
	} else {
		// Draw GitHub logo at top
		iconImg := renderSVGIcon(iconGitHubSVG, 20, colorWhite)
		iconX := (keySize - 20) / 2
		draw.Draw(img, image.Rect(iconX, 4, iconX+20, 24), iconImg, image.Point{}, draw.Over)
		rowY = 28
	}

	// Draw stats as colored rows
	// Waiting (yellow)
	m.drawStatRow(img, rowY, "Wait", stats.WaitingForReview, colorYellow)
	// Approved (green)
	m.drawStatRow(img, rowY+14, "OK", stats.Approved, colorGreen)
	// Changes requested (orange)
	m.drawStatRow(img, rowY+28, "Chg", stats.ChangesRequested, colorOrange)

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

// renderPRKey renders a single PR on a key.
func (m *Module) renderPRKey(pr PRInfo) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, keySize, keySize))

	// Background color based on status (darken if CI failed)
	var bgColor color.Color
	switch {
	case pr.CI == CIStatusFailed:
		bgColor = color.RGBA{60, 30, 30, 255} // Dark red for CI failure
	case pr.Status == PRStatusApproved:
		bgColor = color.RGBA{30, 60, 40, 255} // Dark green
	case pr.Status == PRStatusChanges:
		bgColor = color.RGBA{60, 40, 30, 255} // Dark orange
	default:
		bgColor = color.RGBA{50, 50, 40, 255} // Dark yellow
	}
	draw.Draw(img, img.Bounds(), &image.Uniform{bgColor}, image.Point{}, draw.Src)

	// Status indicator color (review status)
	var statusColor color.Color
	switch pr.Status {
	case PRStatusApproved:
		statusColor = colorGreen
	case PRStatusChanges:
		statusColor = colorOrange
	default:
		statusColor = colorYellow
	}

	// Draw status indicator bar at top (red if CI failed)
	barColor := statusColor
	if pr.CI == CIStatusFailed {
		barColor = colorRed
	}
	barRect := image.Rect(0, 0, keySize, 4)
	draw.Draw(img, barRect, &image.Uniform{barColor}, image.Point{}, draw.Src)

	// Draw PR number
	prNum := fmt.Sprintf("#%d", pr.Number)
	m.drawText(img, prNum, 4, 16, m.labelFace, statusColor)

	// Draw CI indicator next to PR number
	if pr.CI == CIStatusFailed {
		m.drawText(img, "X", 40, 16, m.labelFace, colorRed)
	} else if pr.CI == CIStatusPassed {
		m.drawText(img, "+", 40, 16, m.labelFace, colorGreen)
	}

	// Draw repo name (truncated)
	repo := pr.Repo
	// Get just the repo part (after /)
	if idx := strings.LastIndex(repo, "/"); idx != -1 {
		repo = repo[idx+1:]
	}
	if len(repo) > 10 {
		repo = repo[:9] + "."
	}
	m.drawText(img, repo, 4, 28, m.labelFace, colorDimGray)

	// Draw title (wrapped across multiple lines)
	title := pr.Title
	lines := wrapText(title, 11) // ~11 chars per line at this font size
	y := 42
	for i, line := range lines {
		if i >= 3 { // Max 3 lines
			break
		}
		m.drawText(img, line, 4, y, m.overlayFace, colorWhite)
		y += 11
	}

	return img
}

// renderEmptyKey renders an empty key for the overlay.
func (m *Module) renderEmptyKey() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, keySize, keySize))
	draw.Draw(img, img.Bounds(), &image.Uniform{colorKeyBg}, image.Point{}, draw.Src)
	return img
}

// renderBackKey renders the back button for dismissing the overlay.
func (m *Module) renderBackKey() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, keySize, keySize))
	draw.Draw(img, img.Bounds(), &image.Uniform{colorKeyBg}, image.Point{}, draw.Src)

	// Draw "Back" label centered
	m.drawTextCentered(img, "Back", keySize/2, keySize/2+4, m.overlayFace, colorDimGray)

	return img
}

// renderOverlayStrip renders the touch strip for the PR overlay.
func (m *Module) renderOverlayStrip() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 800, 100))

	// Dark background
	draw.Draw(img, img.Bounds(), &image.Uniform{color.RGBA{30, 30, 30, 255}}, image.Point{}, draw.Src)

	prList := m.getPRList()
	if len(prList) == 0 {
		m.drawTextCentered(img, "No open PRs", 400, 55, m.stripTitleFace, colorDimGray)
		return img
	}

	// Show up to 4 PRs in a single row with larger text
	// Each PR gets 200px width
	const prWidth = 200

	for i, pr := range prList {
		if i >= 4 {
			break
		}
		x := i * prWidth
		m.drawStripPR(img, pr, x)
	}

	return img
}

// drawStripPR draws a single PR entry on the strip.
func (m *Module) drawStripPR(img *image.RGBA, pr PRInfo, x int) {
	// Status color (review status)
	var statusColor color.Color
	switch pr.Status {
	case PRStatusApproved:
		statusColor = colorGreen
	case PRStatusChanges:
		statusColor = colorOrange
	default:
		statusColor = colorYellow
	}

	// Draw status bar on left edge (red if CI failed)
	barColor := statusColor
	if pr.CI == CIStatusFailed {
		barColor = colorRed
	}
	barRect := image.Rect(x+4, 15, x+8, 85)
	draw.Draw(img, barRect, &image.Uniform{barColor}, image.Point{}, draw.Src)

	// Draw repo/number (14px)
	repo := pr.Repo
	if idx := strings.LastIndex(repo, "/"); idx != -1 {
		repo = repo[idx+1:]
	}
	if len(repo) > 10 {
		repo = repo[:9] + "."
	}
	label := fmt.Sprintf("%s #%d", repo, pr.Number)
	m.drawText(img, label, x+16, 35, m.stripLabelFace, statusColor)

	// Draw CI indicator
	ciIndicatorX := x + 16 + font.MeasureString(m.stripLabelFace, label).Ceil() + 5
	if pr.CI == CIStatusFailed {
		m.drawText(img, "X", ciIndicatorX, 35, m.stripLabelFace, colorRed)
	} else if pr.CI == CIStatusPassed {
		m.drawText(img, "+", ciIndicatorX, 35, m.stripLabelFace, colorGreen)
	}

	// Draw title (18px, truncated)
	title := pr.Title
	if len(title) > 18 {
		title = title[:17] + "..."
	}
	m.drawText(img, title, x+16, 60, m.stripTitleFace, colorWhite)
}

// drawTextCentered draws text horizontally centered at the given position.
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

// wrapText wraps text to fit within a given character width.
func wrapText(text string, maxChars int) []string {
	if len(text) <= maxChars {
		return []string{text}
	}

	var lines []string
	words := strings.Fields(text)
	var currentLine string

	for _, word := range words {
		if len(currentLine) == 0 {
			if len(word) > maxChars {
				// Word too long, truncate
				lines = append(lines, word[:maxChars-1]+".")
				continue
			}
			currentLine = word
		} else if len(currentLine)+1+len(word) <= maxChars {
			currentLine += " " + word
		} else {
			lines = append(lines, currentLine)
			if len(word) > maxChars {
				currentLine = word[:maxChars-1] + "."
			} else {
				currentLine = word
			}
		}
	}

	if len(currentLine) > 0 {
		lines = append(lines, currentLine)
	}

	return lines
}
