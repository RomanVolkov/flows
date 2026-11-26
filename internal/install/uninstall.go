package install

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// UninstallOptions configures manifest-based uninstallation.
type UninstallOptions struct {
	HomeDir        string
	ActiveHosts    int
	CancelActive   bool
	Runner         LaunchdRunner
}

// Uninstall removes only owned installation files and unregisters this home's
// launchd service, preserving user config, workflows, worktrees, and usage data.
func Uninstall(ctx context.Context, opts UninstallOptions) error {
	if opts.HomeDir == "" {
		return errors.New("home_dir is required")
	}
	if opts.ActiveHosts > 0 && !opts.CancelActive {
		return fmt.Errorf("cannot uninstall with %d active host(s); cancel or defer", opts.ActiveHosts)
	}

	configDir := filepath.Join(opts.HomeDir, ".config", "flows")
	manifestPath := filepath.Join(configDir, "install-manifest.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read install manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("parse install manifest: %w", err)
	}

	runner := opts.Runner
	if runner == nil {
		runner = execLaunchdRunner{}
	}
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	_, _ = runner.Run(ctx, "bootout", domain+"/"+m.Label)

	// Remove owned files only; check CLI symlink target to avoid removing an external CLI link.
	for _, p := range m.OwnedFiles {
		if p == m.CLILink {
			if target, err := os.Readlink(p); err == nil && !strings.HasPrefix(target, m.AppBundle) {
				continue
			}
		}
		_ = os.RemoveAll(p)
	}
	_ = os.Remove(manifestPath)
	return nil
}
