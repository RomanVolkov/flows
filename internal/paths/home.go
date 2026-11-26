// Package paths resolves the writable locations shared by Flows processes.
package paths

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

type Home struct {
	Config  string
	State   string
	Cache   string
	Logs    string
	Runtime string

	userHome string
	override string
}

func Resolve() (Home, error) {
	userHome, err := os.UserHomeDir()
	if err != nil {
		return Home{}, err
	}
	return resolve(userHome, os.Getenv("XDG_CONFIG_HOME"), os.Getenv("FLOWS_HOME"), os.Getuid())
}

func resolve(userHome, xdgConfig, override string, uid int) (Home, error) {
	if !filepath.IsAbs(userHome) {
		return Home{}, fmt.Errorf("user home must be absolute: %q", userHome)
	}
	if override != "" {
		var err error
		override, err = filepath.Abs(override)
		if err != nil {
			return Home{}, fmt.Errorf("resolve FLOWS_HOME: %w", err)
		}
	}

	if !filepath.IsAbs(xdgConfig) {
		xdgConfig = filepath.Join(userHome, ".config")
	}
	library := filepath.Join(userHome, "Library")
	home := Home{
		Config:   filepath.Join(xdgConfig, "flows"),
		State:    filepath.Join(library, "Application Support", "Flows"),
		Cache:    filepath.Join(library, "Caches", "Flows"),
		Logs:     filepath.Join(library, "Logs", "Flows"),
		userHome: filepath.Clean(userHome),
		override: override,
	}

	identity := "default:" + home.userHome
	if override != "" {
		identity = "isolated:" + override
		home.Config = filepath.Join(override, "config")
		home.State = filepath.Join(override, "state")
		home.Cache = filepath.Join(override, "cache")
		home.Logs = filepath.Join(override, "logs")
	}
	// Use a short base even when macOS supplies a long per-user TMPDIR.
	digest := sha256.Sum256([]byte(identity))
	home.Runtime = filepath.Join("/tmp", fmt.Sprintf("flows-%d-%x", uid, digest[:12]))
	return home, nil
}

// Environment supplies the inputs needed to reproduce these paths in child
// processes and in a launchd EnvironmentVariables dictionary.
func (h Home) Environment() map[string]string {
	return map[string]string{
		"HOME":            h.userHome,
		"FLOWS_HOME":      h.override,
		"XDG_CONFIG_HOME": filepath.Dir(h.Config),
	}
}

func (h Home) Workflows() string { return filepath.Join(h.Config, "workflows") }
func (h Home) Tools() string     { return filepath.Join(h.Config, "tools") }
func (h Home) Spec() string      { return filepath.Join(h.Config, "spec") }
func (h Home) Settings() string  { return filepath.Join(h.Config, "settings.yaml") }

func (h Home) Socket() string { return filepath.Join(h.Runtime, "flowsd.sock") }

func (h Home) InitRuntime() error { return privateDir(h.Runtime) }

func (h Home) ServiceLabel() string {
	const base = "dev.flows.daemon"
	if h.override == "" {
		return base
	}
	return base + "." + filepath.Base(h.Runtime)
}

func (h Home) Init() error {
	dirs := []string{h.Config, h.State, h.Cache, h.Logs, h.Runtime, h.Workflows(), h.Tools(), h.Spec()}
	if h.override != "" {
		dirs = append([]string{h.override}, dirs...)
	}
	for _, dir := range dirs {
		if err := privateDir(dir); err != nil {
			return err
		}
	}
	return initSettings(h.Settings())
}

func privateDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm() != 0700 || !ownedByUser(info) {
		return fmt.Errorf("expected a user-owned directory with mode 0700: %s", path)
	}
	return nil
}

func initSettings(path string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err == nil {
		return file.Close()
	}
	if !os.IsExist(err) {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !ownedByUser(info) {
		return fmt.Errorf("expected a user-private settings file: %s", path)
	}
	return nil
}

func ownedByUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == os.Getuid()
}
