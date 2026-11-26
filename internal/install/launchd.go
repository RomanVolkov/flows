// Package install manages user-scoped installation, launchd registration,
// upgrades with rollback, and manifest-based uninstallation.
package install

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// LaunchdConfig configures per-user launchd daemon registration.
type LaunchdConfig struct {
	Label     string
	DaemonBin string
	HomeDir   string
	LogDir    string
}

// DefaultLabel returns dev.flows.daemon for the standard home or an isolated
// label derived from the home path for dev homes.
func DefaultLabel(homeDir string, isDevHome bool) string {
	if !isDevHome {
		return "dev.flows.daemon"
	}
	sum := sha256.Sum256([]byte(homeDir))
	return "dev.flows.daemon." + hex.EncodeToString(sum[:4])
}

// RenderLaunchdPlist renders a launchd plist safely handling paths with spaces.
func RenderLaunchdPlist(cfg LaunchdConfig) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>--home</string>
		<string>%s</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`,
		html.EscapeString(cfg.Label),
		html.EscapeString(cfg.DaemonBin),
		html.EscapeString(cfg.HomeDir),
		html.EscapeString(filepath.Join(cfg.LogDir, "flowsd.out.log")),
		html.EscapeString(filepath.Join(cfg.LogDir, "flowsd.err.log")),
	)
}

// LaunchdRunner abstracts launchctl invocations for testing.
type LaunchdRunner interface {
	Run(ctx context.Context, args ...string) (string, error)
}

type execLaunchdRunner struct{}

func (execLaunchdRunner) Run(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "launchctl", args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// RegisterLaunchd writes the plist and bootstraps it in the per-user GUI domain.
func RegisterLaunchd(ctx context.Context, runner LaunchdRunner, plistPath string, cfg LaunchdConfig) error {
	if runner == nil {
		runner = execLaunchdRunner{}
	}
	if err := os.MkdirAll(filepath.Dir(plistPath), 0755); err != nil {
		return fmt.Errorf("create LaunchAgents dir: %w", err)
	}
	if err := os.MkdirAll(cfg.LogDir, 0755); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}
	content := RenderLaunchdPlist(cfg)
	if err := os.WriteFile(plistPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("write launchd plist: %w", err)
	}
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	// Detect if already loaded/running before bootstrapping.
	if _, err := runner.Run(ctx, "print", domain+"/"+cfg.Label); err == nil {
		return nil
	}
	if out, err := runner.Run(ctx, "bootstrap", domain, plistPath); err != nil {
		return fmt.Errorf("launchctl bootstrap failed: %w (%s)", err, out)
	}
	return nil
}
