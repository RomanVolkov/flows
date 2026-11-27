// Package theme defines the Flows UI palette, typography, and Ghostty terminal appearance tokens.
package theme

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

const (
	FontFamily = "IosevkaTerm Nerd Font Mono"

	// UI palette
	Background = "#0A0A0B"
	Elevated   = "#111113"
	Surface    = "#18181B"
	Border     = "#27272A"
	Text       = "#FAFAFA"
	Secondary  = "#A1A1AA"
	Disabled   = "#71717A"
	Accent     = "#F59E0B"

	// Terminal appearance
	TerminalFontSizePt    = 19
	TerminalPaddingPt     = 5
	TerminalCursorStyle   = "block"
	TerminalCursorBlinkMs = 500
	TerminalSilentBell    = true
	TerminalOpacity       = 0.90
	TerminalBlur          = true
	TerminalBackground    = "#101019"
	TerminalForeground    = "#D1D9F5"
	TerminalSelectionBg   = "#CBA6F7"
	TerminalSelectionFg   = "#1E1E2F"
	TerminalCursor        = "#F6E3E0"
)

// ANSI16 defines the 16-color terminal palette (0-7 normal, 8-15 bright).
var ANSI16 = [16]string{
	// 0-7
	"#45475A", "#F38BA8", "#A6E3A1", "#F9E2AF",
	"#89B4FA", "#F5C2E7", "#94E2D5", "#BAC2DE",
	// 8-15: #585B70, same six accents, then #A6ADC8
	"#585B70", "#F38BA8", "#A6E3A1", "#F9E2AF",
	"#89B4FA", "#F5C2E7", "#94E2D5", "#A6ADC8",
}

// TerminalOSC sets the canonical Ghostty screen colors before a harness starts,
// matching Carson's runner initialization so its TUI uses the configured palette.
func TerminalOSC() string {
	var b strings.Builder
	for i, color := range ANSI16 {
		fmt.Fprintf(&b, "\x1b]4;%d;rgb:%s/%s/%s\x1b\\", i, color[1:3], color[3:5], color[5:7])
	}
	for i, color := range []string{TerminalForeground, TerminalBackground, TerminalCursor} {
		fmt.Fprintf(&b, "\x1b]%d;rgb:%s/%s/%s\x1b\\", 10+i, color[1:3], color[3:5], color[5:7])
	}
	return b.String()
}

// GhosttyConfig renders the Ghostty surface configuration string for the terminal dock.
// Supported mappings on Ghostty 1.4.0 libghostty surface:
//   - font-family, font-size, window-padding-x/y, cursor-style, cursor-style-blink, cursor-color, cursor-text
//   - background, foreground, selection-background, selection-foreground, palette 0..15
//   - background-opacity (0.90) and background-blur (true on macOS NSVisualEffectView/Metal)
//   - cursor-trail is not a built-in Ghostty 1.4 config key and is omitted.
func GhosttyConfig() string {
	return GhosttyConfigWith(FontFamily, TerminalFontSizePt, TerminalOpacity, TerminalBlur, TerminalPaddingPt, TerminalPaddingPt)
}

// GhosttyConfigWith renders the Ghostty configuration string using the supplied font, opacity, and padding values.
func GhosttyConfigWith(fontFamily string, fontSize int, opacity float64, blur bool, padX, padY int) string {
	if fontFamily == "" || fontFamily == "Iosevka Mono" {
		fontFamily = FontFamily
	}
	if fontSize <= 0 {
		fontSize = TerminalFontSizePt
	}
	if opacity <= 0 || opacity > 1 {
		opacity = TerminalOpacity
	}
	if padX < 0 {
		padX = TerminalPaddingPt
	}
	if padY < 0 {
		padY = TerminalPaddingPt
	}
	var b strings.Builder
	fmt.Fprintf(&b, "font-family = %s\n", fontFamily)
	fmt.Fprintf(&b, "font-size = %d\n", fontSize)
	fmt.Fprintf(&b, "window-padding-x = %d\n", padX)
	fmt.Fprintf(&b, "window-padding-y = %d\n", padY)
	fmt.Fprintf(&b, "cursor-style = %s\n", TerminalCursorStyle)
	fmt.Fprintf(&b, "cursor-style-blink = true\n")
	fmt.Fprintf(&b, "cursor-color = %s\n", TerminalCursor)
	fmt.Fprintf(&b, "cursor-text = %s\n", TerminalBackground)
	fmt.Fprintf(&b, "bell-features = no-attention,no-system,no-audio\n")
	fmt.Fprintf(&b, "background = %s\n", TerminalBackground)
	fmt.Fprintf(&b, "foreground = %s\n", TerminalForeground)
	fmt.Fprintf(&b, "selection-background = %s\n", TerminalSelectionBg)
	fmt.Fprintf(&b, "selection-foreground = %s\n", TerminalSelectionFg)
	fmt.Fprintf(&b, "background-opacity = %.2f\n", opacity)
	fmt.Fprintf(&b, "background-blur = %t\n", blur)
	for i, c := range ANSI16 {
		fmt.Fprintf(&b, "palette = %d=%s\n", i, c)
	}
	return b.String()
}

// ContrastRatio computes the WCAG 2.1 relative luminance contrast ratio between two #RRGGBB colors.
func ContrastRatio(hexA, hexB string) (float64, error) {
	la, err := relativeLuminance(hexA)
	if err != nil {
		return 0, err
	}
	lb, err := relativeLuminance(hexB)
	if err != nil {
		return 0, err
	}
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05), nil
}

func relativeLuminance(hex string) (float64, error) {
	s := strings.TrimPrefix(hex, "#")
	if len(s) != 6 {
		return 0, fmt.Errorf("invalid hex color %q", hex)
	}
	parse := func(off int) (float64, error) {
		v, err := strconv.ParseUint(s[off:off+2], 16, 8)
		if err != nil {
			return 0, err
		}
		c := float64(v) / 255.0
		if c <= 0.03928 {
			return c / 12.92, nil
		}
		return math.Pow((c+0.055)/1.055, 2.4), nil
	}
	r, err := parse(0)
	if err != nil {
		return 0, err
	}
	g, err := parse(2)
	if err != nil {
		return 0, err
	}
	b, err := parse(4)
	if err != nil {
		return 0, err
	}
	return 0.2126*r + 0.7152*g + 0.0722*b, nil
}
