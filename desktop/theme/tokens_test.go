package theme

import (
	"strings"
	"testing"
)

func TestGhosttyConfigAndContrast(t *testing.T) {
	cfg := GhosttyConfig()
	for _, want := range []string{
		"font-family = IosevkaTerm Nerd Font Mono",
		"font-size = 19",
		"window-padding-x = 5",
		"cursor-style = block",
		"cursor-style-blink = true",
		"background = #101019",
		"foreground = #D1D9F5",
		"selection-background = #CBA6F7",
		"selection-foreground = #1E1E2F",
		"background-opacity = 0.90",
		"background-blur = true",
		"palette = 0=#45475A",
		"palette = 8=#585B70",
		"palette = 15=#A6ADC8",
	} {
		if !strings.Contains(cfg, want) {
			t.Fatalf("GhosttyConfig missing %q", want)
		}
	}

	// Verify readable contrast for primary, secondary, disabled, and selected text.
	for _, pair := range []struct {
		name   string
		fg, bg string
		min    float64
	}{
		{"primary text", Text, Background, 12.0},
		{"secondary text", Secondary, Surface, 4.5},
		{"disabled text", Disabled, Background, 3.0},
		{"selected terminal text", TerminalSelectionFg, TerminalSelectionBg, 7.0},
		{"terminal fg/bg", TerminalForeground, TerminalBackground, 10.0},
	} {
		ratio, err := ContrastRatio(pair.fg, pair.bg)
		if err != nil {
			t.Fatalf("%s contrast error: %v", pair.name, err)
		}
		if ratio < pair.min {
			t.Fatalf("%s contrast %.2f < %.2f (%s on %s)", pair.name, ratio, pair.min, pair.fg, pair.bg)
		}
	}
}
