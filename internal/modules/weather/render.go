package weather

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

//go:embed fonts/PublicSans-Regular.ttf
var fontRegular []byte

// Weather icons
//
//go:embed icons/sun.svg
var iconSunSVG string

//go:embed icons/moon.svg
var iconMoonSVG string

//go:embed icons/cloud.svg
var iconCloudSVG string

//go:embed icons/cloud-sun.svg
var iconCloudSunSVG string

//go:embed icons/cloud-moon.svg
var iconCloudMoonSVG string

//go:embed icons/cloud-rain.svg
var iconCloudRainSVG string

//go:embed icons/cloud-snow.svg
var iconCloudSnowSVG string

//go:embed icons/cloud-lightning.svg
var iconCloudLightningSVG string

//go:embed icons/cloud-fog.svg
var iconCloudFogSVG string

// Colors
var (
	colorSunny      = color.RGBA{255, 200, 50, 255}  // Yellow/gold for sunny
	colorNight      = color.RGBA{100, 149, 237, 255} // Cornflower blue for night
	colorCloudy     = color.RGBA{180, 180, 180, 255} // Gray for cloudy
	colorRain       = color.RGBA{100, 149, 237, 255} // Blue for rain
	colorSnow       = color.RGBA{200, 220, 255, 255} // Light blue for snow
	colorStorm      = color.RGBA{255, 200, 50, 255}  // Yellow for lightning
	colorBackground = color.RGBA{25, 25, 25, 255}
	colorKeyBg      = color.RGBA{40, 40, 40, 255}
	colorWhite      = color.RGBA{255, 255, 255, 255}
	colorGray       = color.RGBA{160, 160, 160, 255}
)

// initFonts initializes the font faces for rendering.
func (m *Module) initFonts() error {
	ttBold, err := opentype.Parse(fontBold)
	if err != nil {
		return fmt.Errorf("parse bold font: %w", err)
	}

	// Large temp for strip
	m.tempSmallFace, err = opentype.NewFace(ttBold, &opentype.FaceOptions{
		Size:    32,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return fmt.Errorf("create temp face: %w", err)
	}

	ttRegular, err := opentype.Parse(fontRegular)
	if err != nil {
		return fmt.Errorf("parse regular font: %w", err)
	}

	m.conditionFace, err = opentype.NewFace(ttRegular, &opentype.FaceOptions{
		Size:    16,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return fmt.Errorf("create condition face: %w", err)
	}

	return nil
}

// renderStrip renders the weather strip segment.
func (m *Module) renderStrip(rect image.Rectangle, current CurrentWeather, daily DailyForecast, precip PrecipForecast) image.Image {
	// Create full-size image but only fill our region (400-800)
	img := image.NewRGBA(rect)
	h := rect.Dy()

	// Only fill our region with background (don't touch 0-400)
	myRegion := image.Rect(400, 0, 800, h)
	draw.Draw(img, myRegion, &image.Uniform{colorBackground}, image.Point{}, draw.Src)

	// If no data yet, show placeholder
	if current.Temp == 0 {
		m.drawText(img, "Loading...", 410, h/2+6, m.conditionFace, colorGray)
		return img
	}

	// Layout (400-800, 400px wide):
	// Icon: 400-480 (centered 70px icon with padding)
	// Left text: 490-610 (temp, feels like, condition)
	// Right text: 620-790 (high/low, precip)

	// ICON (left side)
	// OpenWeatherMap's current.weather condition lags reality and often
	// disagrees with the radar-based minutely nowcast. When the nowcast says
	// it's actively precipitating, trust it over current.Icon/Description.
	iconSVG, iconColor := getWeatherIcon(current.Icon)
	if precip.Active {
		iconSVG, iconColor = getPrecipIcon(precip.Type)
	}
	iconSize := 70
	iconImg := renderSVGIcon(iconSVG, iconSize, iconColor)
	iconX := 405
	iconY := (h - iconSize) / 2
	iconRect := image.Rect(iconX, iconY, iconX+iconSize, iconY+iconSize)
	draw.Draw(img, iconRect, iconImg, image.Point{}, draw.Over)

	// LEFT TEXT SECTION
	leftX := 490

	// Current temperature (large)
	tempStr := fmt.Sprintf("%.0f°", current.Temp)
	m.drawText(img, tempStr, leftX, 38, m.tempSmallFace, colorWhite)

	// Feels like
	feelsStr := fmt.Sprintf("Feels %.0f°", current.FeelsLike)
	m.drawText(img, feelsStr, leftX, 60, m.conditionFace, colorGray)

	// Condition text (nowcast wins when actively precipitating)
	condition := current.Description
	if precip.Active {
		condition = precipLabel(precip.Type)
	} else if condition == "" {
		condition = current.Condition
	}
	if len(condition) > 0 {
		condition = strings.ToUpper(condition[:1]) + condition[1:]
	}
	m.drawText(img, condition, leftX, 82, m.conditionFace, colorGray)

	// RIGHT TEXT SECTION
	rightX := 620

	// High/Low
	if daily.TempMax != 0 || daily.TempMin != 0 {
		hiLoStr := fmt.Sprintf("H:%.0f° L:%.0f°", daily.TempMax, daily.TempMin)
		m.drawText(img, hiLoStr, rightX, 38, m.conditionFace, colorWhite)
	}

	// Precipitation forecast
	if precip.Description != "" {
		precipColor := colorRain
		if precip.Type == "Snow" || precip.Type == "Sleet" {
			precipColor = colorSnow
		}
		m.drawText(img, precip.Description, rightX, 60, m.conditionFace, precipColor)
	}

	return img
}

// getWeatherIcon returns the appropriate SVG and color for an OpenWeatherMap icon code.
func getWeatherIcon(iconCode string) (string, color.Color) {
	// OpenWeatherMap icon codes:
	// 01d/01n - clear sky
	// 02d/02n - few clouds
	// 03d/03n - scattered clouds
	// 04d/04n - broken clouds
	// 09d/09n - shower rain
	// 10d/10n - rain
	// 11d/11n - thunderstorm
	// 13d/13n - snow
	// 50d/50n - mist

	isNight := strings.HasSuffix(iconCode, "n")

	switch {
	case strings.HasPrefix(iconCode, "01"):
		if isNight {
			return iconMoonSVG, colorNight
		}
		return iconSunSVG, colorSunny
	case strings.HasPrefix(iconCode, "02"):
		if isNight {
			return iconCloudMoonSVG, colorNight
		}
		return iconCloudSunSVG, colorSunny
	case strings.HasPrefix(iconCode, "03"), strings.HasPrefix(iconCode, "04"):
		return iconCloudSVG, colorCloudy
	case strings.HasPrefix(iconCode, "09"), strings.HasPrefix(iconCode, "10"):
		return iconCloudRainSVG, colorRain
	case strings.HasPrefix(iconCode, "11"):
		return iconCloudLightningSVG, colorStorm
	case strings.HasPrefix(iconCode, "13"):
		return iconCloudSnowSVG, colorSnow
	case strings.HasPrefix(iconCode, "50"):
		return iconCloudFogSVG, colorCloudy
	default:
		// Default to cloud
		return iconCloudSVG, colorCloudy
	}
}

// getPrecipIcon returns the SVG and color for an active precipitation type,
// as reported by the minutely nowcast (see analyzePrecipitation / getPrecipType).
func getPrecipIcon(precipType string) (string, color.Color) {
	switch precipType {
	case "Snow":
		return iconCloudSnowSVG, colorSnow
	case "Sleet":
		return iconCloudSnowSVG, colorSnow
	case "Storm":
		return iconCloudLightningSVG, colorStorm
	default: // Rain, Drizzle
		return iconCloudRainSVG, colorRain
	}
}

// precipLabel returns the headline condition text for an active precipitation type.
func precipLabel(precipType string) string {
	switch precipType {
	case "Snow":
		return "Snowing"
	case "Sleet":
		return "Sleet"
	case "Storm":
		return "Thunderstorm"
	case "Drizzle":
		return "Drizzle"
	default: // Rain
		return "Raining"
	}
}

// renderSVGIcon renders an SVG string to an image with the given size and color.
func renderSVGIcon(svgContent string, size int, iconColor color.Color) image.Image {
	// Replace currentColor with the actual color
	r, g, b, _ := iconColor.RGBA()
	hexColor := fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
	svgContent = strings.ReplaceAll(svgContent, "currentColor", hexColor)

	icon, err := oksvg.ReadIconStream(strings.NewReader(svgContent))
	if err != nil {
		log.Printf("Failed to parse SVG: %v", err)
		return image.NewRGBA(image.Rect(0, 0, size, size))
	}

	img := image.NewRGBA(image.Rect(0, 0, size, size))
	// Transparent background for icon
	draw.Draw(img, img.Bounds(), &image.Uniform{color.Transparent}, image.Point{}, draw.Src)

	icon.SetTarget(0, 0, float64(size), float64(size))

	scanner := rasterx.NewScannerGV(size, size, img, img.Bounds())
	raster := rasterx.NewDasher(size, size, scanner)
	icon.Draw(raster, 1.0)

	return img
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

