package install

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Options configures a user-scoped Flows installation.
type Options struct {
	HomeDir      string
	SourceBundle string // path to Flows.app bundle
	IsDevHome    bool
	Runner       LaunchdRunner
	HealthCheck  func(ctx context.Context) error
}

// Manifest tracks owned files created during installation for clean uninstall.
type Manifest struct {
	Version    string   `json:"version"`
	Label      string   `json:"label"`
	AppBundle  string   `json:"app_bundle"`
	CLILink    string   `json:"cli_link"`
	PlistPath  string   `json:"plist_path"`
	OwnedFiles []string `json:"owned_files"`
}

// Install stages Flows.app, CLI link, default workflows (preserving user edits),
// spec/v1, and launchd registration, recording an owned-file manifest.
func Install(ctx context.Context, opts Options) (Manifest, error) {
	if opts.HomeDir == "" || opts.SourceBundle == "" {
		return Manifest{}, errors.New("home_dir and source_bundle are required")
	}

	appDst := filepath.Join(opts.HomeDir, "Applications", "Flows.app")
	cliDst := filepath.Join(opts.HomeDir, ".local", "bin", "flows")
	configDir := filepath.Join(opts.HomeDir, ".config", "flows")
	specDst := filepath.Join(configDir, "spec", "v1")
	workflowsDst := filepath.Join(opts.HomeDir, ".local", "share", "flows", "workflows")
	logDir := filepath.Join(opts.HomeDir, "Library", "Logs", "Flows")
	label := DefaultLabel(opts.HomeDir, opts.IsDevHome)
	plistPath := filepath.Join(opts.HomeDir, "Library", "LaunchAgents", label+".plist")

	// 1. Stage Flows.app.
	if err := copyDirPreserveExisting(opts.SourceBundle, appDst, true); err != nil {
		return Manifest{}, fmt.Errorf("step stage app bundle: %w", err)
	}

	// 2. Link ~/.local/bin/flows -> Flows.app/Contents/MacOS/flows.
	if err := os.MkdirAll(filepath.Dir(cliDst), 0755); err != nil {
		return Manifest{}, fmt.Errorf("step create bin dir: %w", err)
	}
	cliTarget := filepath.Join(appDst, "Contents", "MacOS", "flows")
	_ = os.Remove(cliDst)
	if err := os.Symlink(cliTarget, cliDst); err != nil {
		return Manifest{}, fmt.Errorf("step symlink cli: %w", err)
	}

	// 3. Stage spec/v1 (always refreshed) and default workflows (never overwrite user-edited files).
	srcSpec := filepath.Join(opts.SourceBundle, "Contents", "Resources", "spec", "v1")
	if _, err := os.Stat(srcSpec); err == nil {
		if err := copyDirPreserveExisting(srcSpec, specDst, true); err != nil {
			return Manifest{}, fmt.Errorf("step stage spec: %w", err)
		}
	}
	srcWorkflows := filepath.Join(opts.SourceBundle, "Contents", "Resources", "workflows")
	if _, err := os.Stat(srcWorkflows); err == nil {
		if err := copyDirPreserveExisting(srcWorkflows, workflowsDst, false); err != nil {
			return Manifest{}, fmt.Errorf("step stage workflows: %w", err)
		}
	}

	// 4. Register launchd service.
	daemonBin := filepath.Join(appDst, "Contents", "MacOS", "flowsd")
	if err := RegisterLaunchd(ctx, opts.Runner, plistPath, LaunchdConfig{
		Label:     label,
		DaemonBin: daemonBin,
		HomeDir:   opts.HomeDir,
		LogDir:    logDir,
	}); err != nil {
		return Manifest{}, fmt.Errorf("step register launchd: %w", err)
	}

	// 5. Wait for protocol-compatible health if provided.
	if opts.HealthCheck != nil {
		if err := opts.HealthCheck(ctx); err != nil {
			return Manifest{}, fmt.Errorf("step daemon health check: %w", err)
		}
	}

	m := Manifest{
		Version:    "1.0.0",
		Label:      label,
		AppBundle:  appDst,
		CLILink:    cliDst,
		PlistPath:  plistPath,
		OwnedFiles: []string{appDst, cliDst, plistPath, specDst},
	}
	manifestPath := filepath.Join(configDir, "install-manifest.json")
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0755); err == nil {
		if raw, err := json.MarshalIndent(m, "", "  "); err == nil {
			_ = os.WriteFile(manifestPath, raw, 0644)
		}
	}
	return m, nil
}

func copyDirPreserveExisting(src, dst string, overwrite bool) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		if !overwrite {
			if _, err := os.Stat(target); err == nil {
				return nil // preserve user-edited file
			}
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
}
