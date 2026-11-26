package settings

import (
	"github.com/romanvolkov/flows/desktop/theme"
	"github.com/romanvolkov/flows/internal/config"
	"github.com/romanvolkov/flows/internal/paths"
)

// GeneralPane manages General and Terminal settings, root folder reveal paths,
// safe reload with rollback on syntax error, and keyboard-only navigation.
type GeneralPane struct {
	home        paths.Home
	Settings    config.Settings `json:"settings"`
	ReloadError string          `json:"reload_error,omitempty"`

	ConfigRoot string `json:"config_root"`
	StateRoot  string `json:"state_root"`
	LogsRoot   string `json:"logs_root"`

	FocusedField int      `json:"focused_field"`
	Fields       []string `json:"fields"`
}

// NewGeneralPane loads settings from home and initializes reveal paths.
func NewGeneralPane(home paths.Home) *GeneralPane {
	s, err := config.LoadSettings(home.Settings())
	var errMsg string
	if err != nil {
		s = config.DefaultSettings()
		errMsg = err.Error()
	}
	return &GeneralPane{
		home:        home,
		Settings:    s,
		ReloadError: errMsg,
		ConfigRoot:  home.Config,
		StateRoot:   home.State,
		LogsRoot:    home.Logs,
		Fields: []string{
			"default_harness",
			"default_directory",
			"concurrency",
			"notifications.enabled",
			"terminal.font_size",
			"terminal.opacity",
			"reset_defaults",
		},
	}
}

// Reload re-reads settings.yaml. On syntax or validation error, it preserves the
// previous valid Settings intact and records ReloadError.
func (g *GeneralPane) Reload() bool {
	loaded, err := config.LoadSettings(g.home.Settings())
	if err != nil {
		g.ReloadError = err.Error()
		return false
	}
	if err := config.ValidateSettings(loaded); err != nil {
		g.ReloadError = err.Error()
		return false
	}
	g.Settings = loaded
	g.ReloadError = ""
	return true
}

// Save persists the current Settings atomically with optimistic revision check.
func (g *GeneralPane) Save() error {
	saved, err := config.SaveSettings(g.home.Settings(), g.Settings.Revision, g.Settings)
	if err != nil {
		return err
	}
	g.Settings = saved
	g.ReloadError = ""
	return nil
}

// ResetToDefaults restores bundled default settings and persists them.
func (g *GeneralPane) ResetToDefaults() error {
	def := config.DefaultSettings()
	saved, err := config.SaveSettings(g.home.Settings(), g.Settings.Revision, def)
	if err != nil {
		return err
	}
	g.Settings = saved
	g.ReloadError = ""
	return nil
}

// TerminalPreview returns the Ghostty configuration string for the terminal preview.
func (g *GeneralPane) TerminalPreview() string {
	return theme.GhosttyConfig()
}

// NextField advances keyboard focus to the next control.
func (g *GeneralPane) NextField() string {
	if len(g.Fields) == 0 {
		return ""
	}
	g.FocusedField = (g.FocusedField + 1) % len(g.Fields)
	return g.Fields[g.FocusedField]
}

// PrevField moves keyboard focus to the previous control.
func (g *GeneralPane) PrevField() string {
	if len(g.Fields) == 0 {
		return ""
	}
	g.FocusedField = (g.FocusedField - 1 + len(g.Fields)) % len(g.Fields)
	return g.Fields[g.FocusedField]
}

// StepFocusedValue adjusts the currently focused field via keyboard only.
func (g *GeneralPane) StepFocusedValue(delta int) {
	if g.FocusedField < 0 || g.FocusedField >= len(g.Fields) {
		return
	}
	switch g.Fields[g.FocusedField] {
	case "concurrency":
		next := g.Settings.Concurrency + delta
		if next >= 1 && next <= 64 {
			g.Settings.Concurrency = next
		}
	case "notifications.enabled":
		g.Settings.Notifications.Enabled = !g.Settings.Notifications.Enabled
	case "terminal.font_size":
		next := g.Settings.Terminal.FontSize + delta
		if next >= 8 && next <= 48 {
			g.Settings.Terminal.FontSize = next
		}
	}
}
