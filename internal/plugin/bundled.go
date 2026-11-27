package plugin

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/romanvolkov/flows/internal/paths"
)

// ActivateBundled registers a prebuilt executable from an imported app bundle.
func ActivateBundled(home paths.Home, id, version, source, binary string) error {
	lock, err := acquireToolLock(filepath.Join(home.Runtime, "install-"+id+".lock"))
	if err != nil {
		return err
	}
	defer lock.Close()

	if _, err := LoadActiveTool(home, id); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	info, err := os.Stat(binary)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0100 == 0 {
		return fmt.Errorf("expected an executable plugin at %s", binary)
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		return err
	}

	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	cacheKey := "bundle-" + digest
	parent := filepath.Join(home.State, "builds", id)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return err
	}
	target := filepath.Join(parent, cacheKey)
	receipt := BuildReceipt{
		ToolID: id, Version: version, CacheKey: cacheKey, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		BinaryPath: filepath.Join(target, "plugin"), BinaryDigest: digest, BuiltAt: time.Now().UnixNano(),
	}
	if _, err := os.Stat(target); os.IsNotExist(err) {
		stage, err := os.MkdirTemp(parent, ".bundle-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(stage)
		if err := os.Link(binary, filepath.Join(stage, "plugin")); err != nil {
			return err
		}
		raw, err := json.MarshalIndent(receipt, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(stage, "receipt.json"), raw, 0600); err != nil {
			return err
		}
		if err := os.Rename(stage, target); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	return saveActiveTool(home, ActiveTool{
		ToolID: id, SourceDir: source, Bundled: true, Receipt: receipt, ActivatedAt: time.Now().UnixNano(),
	})
}
