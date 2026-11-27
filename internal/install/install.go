package install

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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
	workflowsDst := filepath.Join(configDir, "workflows")
	logDir := filepath.Join(opts.HomeDir, "Library", "Logs", "Flows")
	label := DefaultLabel(opts.HomeDir, opts.IsDevHome)
	plistPath := filepath.Join(opts.HomeDir, "Library", "LaunchAgents", label+".plist")

	// 1. Check preexisting CLI link conflict before mutating anything.
	cliTarget := filepath.Join(appDst, "Contents", "MacOS", "flows")
	if info, err := os.Lstat(cliDst); err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return Manifest{}, fmt.Errorf("step check cli link: %s already exists and is not a symlink", cliDst)
		}
		target, err := os.Readlink(cliDst)
		if err != nil || !strings.HasPrefix(target, appDst) {
			return Manifest{}, fmt.Errorf("step check cli link: %s points to %q (expected %s)", cliDst, target, cliTarget)
		}
	}

	// 2. Stage Flows.app.
	if err := copyDirPreserveExisting(opts.SourceBundle, appDst, true); err != nil {
		return Manifest{}, fmt.Errorf("step stage app bundle: %w", err)
	}

	// 3. Link ~/.local/bin/flows -> Flows.app/Contents/MacOS/flows.
	if err := os.MkdirAll(filepath.Dir(cliDst), 0755); err != nil {
		return Manifest{}, fmt.Errorf("step create bin dir: %w", err)
	}
	_ = os.Remove(cliDst)
	if err := os.Symlink(cliTarget, cliDst); err != nil {
		return Manifest{}, fmt.Errorf("step symlink cli: %w", err)
	}

	// 4. Stage spec/v1 (always refreshed) and default workflows (never overwrite user-edited files).
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

	// 5. Register launchd service (only pass --home for isolated dev homes).
	daemonBin := filepath.Join(appDst, "Contents", "MacOS", "flowsd")
	launchdHome := ""
	if opts.IsDevHome {
		launchdHome = filepath.Join(opts.HomeDir, ".flows-dev")
	}
	if err := RegisterLaunchd(ctx, opts.Runner, plistPath, LaunchdConfig{
		Label:     label,
		DaemonBin: daemonBin,
		HomeDir:   launchdHome,
		LogDir:    logDir,
		Restart:   true,
	}); err != nil {
		return Manifest{}, fmt.Errorf("step register launchd: %w", err)
	}

	// 6. Wait for protocol-compatible health if provided.
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
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0755); err != nil {
		return Manifest{}, fmt.Errorf("step create manifest dir: %w", err)
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return Manifest{}, fmt.Errorf("step marshal manifest: %w", err)
	}
	if err := os.WriteFile(manifestPath, raw, 0644); err != nil {
		return Manifest{}, fmt.Errorf("step write manifest: %w", err)
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
		if d.Type()&os.ModeSymlink != 0 {
			linkTarget, err := os.Readlink(path)
			if err != nil {
				return err
			}
			_ = os.Remove(target)
			return os.Symlink(linkTarget, target)
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
		return copyFileAtomic(path, target, info.Mode())
	})
}

func copyFileAtomic(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
