package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/romanvolkov/flows/internal/paths"
)

// ActiveTool records the currently active build receipt and source location for a tool.
type ActiveTool struct {
	PackageDigest string       `json:"package_digest,omitempty"`
	Bundled       bool         `json:"bundled,omitempty"`
	ToolID        string       `json:"tool_id"`
	SourceDir     string       `json:"source_dir"`
	Entry         string       `json:"entry,omitempty"`
	Receipt       BuildReceipt `json:"receipt"`
	ActivatedAt   int64        `json:"activated_at"`
}

// InstallOptions configures tool build and activation.
type InstallOptions struct {
	PackageDigest string
	Bundled       bool
	Entry         string
	GoBin         string
}

// Install builds sourceDir and atomically activates it under home.State,
// serializing concurrent installs and preserving the previous active version on failure.
func Install(ctx context.Context, home paths.Home, toolID, version, sourceDir string, opts InstallOptions) (ActiveTool, error) {
	if err := home.Init(); err != nil {
		return ActiveTool{}, err
	}
	lock, err := acquireToolLock(filepath.Join(home.Runtime, "install-"+toolID+".lock"))
	if err != nil {
		return ActiveTool{}, err
	}
	defer lock.Close()

	receipt, err := BuildTool(ctx, home, toolID, version, sourceDir, opts.Entry, opts.GoBin)
	if err != nil {
		return ActiveTool{}, err
	}

	active := ActiveTool{
		PackageDigest: opts.PackageDigest,
		Bundled:       opts.Bundled,
		ToolID:        toolID,
		SourceDir:     sourceDir,
		Entry:         opts.Entry,
		Receipt:       receipt,
		ActivatedAt:   time.Now().UnixNano(),
	}
	if err := saveActiveTool(home, active); err != nil {
		return ActiveTool{}, err
	}
	return active, nil
}

// Rebuild recompiles the currently installed source directory for toolID.
func Rebuild(ctx context.Context, home paths.Home, toolID string, goBin string) (ActiveTool, error) {
	cur, err := LoadActiveTool(home, toolID)
	if err != nil {
		return ActiveTool{}, err
	}
	return Install(ctx, home, toolID, cur.Receipt.Version, cur.SourceDir, InstallOptions{
		Entry: cur.Entry,
		GoBin: goBin,
	})
}

// Rollback switches the active pointer for toolID back to a previously built cacheKey.
func Rollback(home paths.Home, toolID, cacheKey string) (ActiveTool, error) {
	receiptPath := filepath.Join(home.State, "builds", toolID, cacheKey, "receipt.json")
	data, err := os.ReadFile(receiptPath)
	if err != nil {
		return ActiveTool{}, fmt.Errorf("load build receipt %s/%s: %w", toolID, cacheKey, err)
	}
	var rec BuildReceipt
	if err := json.Unmarshal(data, &rec); err != nil {
		return ActiveTool{}, err
	}
	cur, _ := LoadActiveTool(home, toolID)
	active := ActiveTool{
		ToolID:      toolID,
		Bundled:     cur.Bundled,
		SourceDir:   cur.SourceDir,
		Entry:       cur.Entry,
		Receipt:     rec,
		ActivatedAt: time.Now().UnixNano(),
	}
	if err := saveActiveTool(home, active); err != nil {
		return ActiveTool{}, err
	}
	return active, nil
}

// LoadActiveTool reads the active build metadata for toolID.
func LoadActiveTool(home paths.Home, toolID string) (ActiveTool, error) {
	data, err := os.ReadFile(filepath.Join(home.State, "installed", toolID, "active.json"))
	if err != nil {
		return ActiveTool{}, err
	}
	var active ActiveTool
	if err := json.Unmarshal(data, &active); err != nil {
		return ActiveTool{}, err
	}
	if active.Bundled {
		if _, err := os.Stat(filepath.Join(active.SourceDir, "tool.yaml")); err != nil {
			return ActiveTool{}, err
		}
	}
	return active, nil
}

// PinBuild records a run's pin on a specific build cacheKey so old builds remain
// available while pinned runs reference them.
func PinBuild(home paths.Home, toolID, cacheKey, runID string) error {
	dir := filepath.Join(home.State, "builds", toolID, cacheKey, "pins")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, runID+".pin"), []byte(runID), 0600)
}

// IsBuildPinned reports whether any run still references cacheKey.
func IsBuildPinned(home paths.Home, toolID, cacheKey string) bool {
	dir := filepath.Join(home.State, "builds", toolID, cacheKey, "pins")
	entries, err := os.ReadDir(dir)
	return err == nil && len(entries) > 0
}

func saveActiveTool(home paths.Home, active ActiveTool) error {
	dir := filepath.Join(home.State, "installed", active.ToolID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	target := filepath.Join(dir, "active.json")
	tmp, err := os.CreateTemp(dir, ".tmp-active-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	data, _ := json.MarshalIndent(active, "", "  ")
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	_ = tmp.Chmod(0600)
	_ = tmp.Sync()
	_ = tmp.Close()
	return os.Rename(tmpName, target)
}

func acquireToolLock(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}
