// Package config manages versioned settings.yaml persistence, optimistic
// concurrency, validation, and effective harness profile precedence.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"gopkg.in/yaml.v3"

	"github.com/romanvolkov/flows/internal/harness"
)

const SettingsVersion = 1

var (
	ErrRevisionConflict = errors.New("settings revision conflict")
	ErrSettingsSyntax   = errors.New("settings YAML syntax error")
	ErrInvalidSettings  = errors.New("invalid settings")
)

// NotificationSettings configures desktop and reply notifications.
type NotificationSettings struct {
	Enabled       bool `yaml:"enabled" json:"enabled"`
	DesktopAlerts bool `yaml:"desktop_alerts" json:"desktop_alerts"`
	Sound         bool `yaml:"sound" json:"sound"`
}

// TerminalSettings configures terminal appearance preferences.
type TerminalSettings struct {
	FontFamily string  `yaml:"font_family" json:"font_family"`
	FontSize   int     `yaml:"font_size" json:"font_size"`
	Opacity    float64 `yaml:"opacity" json:"opacity"`
	Blur       bool    `yaml:"blur" json:"blur"`
}

// HarnessSettings configures per-harness executable and policy defaults.
type HarnessSettings struct {
	Executable           string `yaml:"executable,omitempty" json:"executable,omitempty"`
	Model                string `yaml:"model,omitempty" json:"model,omitempty"`
	Effort               string `yaml:"effort,omitempty" json:"effort,omitempty"`
	ClaudePermissionMode string `yaml:"claude_permission_mode,omitempty" json:"claude_permission_mode,omitempty"`
	CodexApprovalPolicy  string `yaml:"codex_approval_policy,omitempty" json:"codex_approval_policy,omitempty"`
	CodexSandboxMode     string `yaml:"codex_sandbox_mode,omitempty" json:"codex_sandbox_mode,omitempty"`
	AgyMode              string `yaml:"agy_mode,omitempty" json:"agy_mode,omitempty"`
}

// Settings is the persisted settings.yaml schema.
type Settings struct {
	Version          int                              `yaml:"version" json:"version"`
	Revision         int64                            `yaml:"revision" json:"revision"`
	DefaultHarness   harness.Kind                     `yaml:"default_harness" json:"default_harness"`
	DefaultDirectory string                           `yaml:"default_directory,omitempty" json:"default_directory,omitempty"`
	SearchRoots      []string                         `yaml:"search_roots,omitempty" json:"search_roots,omitempty"`
	Concurrency      int                              `yaml:"concurrency" json:"concurrency"`
	Notifications    NotificationSettings             `yaml:"notifications" json:"notifications"`
	Terminal         TerminalSettings                 `yaml:"terminal" json:"terminal"`
	Harnesses        map[harness.Kind]HarnessSettings `yaml:"harnesses,omitempty" json:"harnesses,omitempty"`
	Credentials      map[string]string                `yaml:"credentials,omitempty" json:"credentials,omitempty"`
}

// DefaultSettings returns the bundled default settings.
func DefaultSettings() Settings {
	return Settings{
		Version:        SettingsVersion,
		Revision:       0,
		DefaultHarness: harness.KindClaude,
		Concurrency:    4,
		Notifications: NotificationSettings{
			Enabled:       true,
			DesktopAlerts: true,
			Sound:         false,
		},
		Terminal: TerminalSettings{
			FontFamily: "Iosevka Mono",
			FontSize:   19,
			Opacity:    0.90,
			Blur:       true,
		},
		Harnesses: map[harness.Kind]HarnessSettings{
			harness.KindClaude: {ClaudePermissionMode: "default"},
			harness.KindCodex:  {CodexApprovalPolicy: "on-request", CodexSandboxMode: "workspace-write"},
			harness.KindAgy:    {AgyMode: "accept-edits"},
		},
		Credentials: map[string]string{},
	}
}

// LoadSettings reads and validates settings.yaml from path.
// An absent or empty file returns DefaultSettings().
func LoadSettings(path string) (Settings, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return DefaultSettings(), nil
		}
		return Settings{}, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return DefaultSettings(), nil
	}
	s := DefaultSettings()
	if err := yaml.Unmarshal(data, &s); err != nil {
		return Settings{}, fmt.Errorf("%w: %v", ErrSettingsSyntax, err)
	}
	if s.Version == 0 {
		s.Version = SettingsVersion
	}
	if s.Version != SettingsVersion {
		return Settings{}, fmt.Errorf("%w: unsupported settings version %d", ErrInvalidSettings, s.Version)
	}
	if s.Harnesses == nil {
		s.Harnesses = DefaultSettings().Harnesses
	}
	if s.Credentials == nil {
		s.Credentials = map[string]string{}
	}
	return s, nil
}

// SaveSettings validates next, checks optimistic expectedRevision against disk,
// increments Revision, and atomically writes settings.yaml.
func SaveSettings(path string, expectedRevision int64, next Settings) (Settings, error) {
	if err := ValidateSettings(next); err != nil {
		return Settings{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return Settings{}, err
	}

	lockFile, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return Settings{}, err
	}
	defer lockFile.Close()
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return Settings{}, err
	}

	cur, loadErr := LoadSettings(path)
	if loadErr != nil && !errors.Is(loadErr, ErrSettingsSyntax) {
		return Settings{}, loadErr
	}
	if loadErr == nil && cur.Revision != expectedRevision {
		return Settings{}, fmt.Errorf("%w: expected revision %d, current is %d", ErrRevisionConflict, expectedRevision, cur.Revision)
	}

	next.Version = SettingsVersion
	next.Revision = expectedRevision + 1

	raw, err := yaml.Marshal(next)
	if err != nil {
		return Settings{}, err
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings-*")
	if err != nil {
		return Settings{}, err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return Settings{}, err
	}
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return Settings{}, err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return Settings{}, err
	}
	if err := tmp.Close(); err != nil {
		return Settings{}, err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return Settings{}, err
	}
	cleanup = false
	return next, nil
}
