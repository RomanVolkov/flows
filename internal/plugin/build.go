package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/romanvolkov/flows/internal/paths"
)

var ErrMissingGoCompiler = errors.New("go compiler not found")

// CompileError surfaces compiler output when building a tool fails.
type CompileError struct {
	Diagnostics string
	Err         error
}

func (e *CompileError) Error() string {
	return fmt.Sprintf("compile tool: %v\n%s", e.Err, e.Diagnostics)
}

func (e *CompileError) Unwrap() error { return e.Err }

// BuildReceipt records a verified per-platform tool executable build.
type BuildReceipt struct {
	ToolID       string `json:"tool_id"`
	Version      string `json:"version"`
	SourceDigest string `json:"source_digest"`
	ModuleDigest string `json:"module_digest"`
	CacheKey     string `json:"cache_key"`
	GOOS         string `json:"goos"`
	GOARCH       string `json:"goarch"`
	GoVersion    string `json:"go_version"`
	BinaryPath   string `json:"binary_path"`
	BinaryDigest string `json:"binary_digest"`
	BuiltAt      int64  `json:"built_at"`
}

// BuildTool compiles a Go tool package into a temporary staging directory,
// verifies its protocol handshake and manifest, and promotes it atomically.
func BuildTool(ctx context.Context, home paths.Home, toolID, version, sourceDir, entry, goBin string) (BuildReceipt, error) {
	if goBin == "" {
		goBin = "go"
	}
	goPath, err := exec.LookPath(goBin)
	if err != nil {
		return BuildReceipt{}, fmt.Errorf("%w: %s (%v)", ErrMissingGoCompiler, goBin, err)
	}
	verOut, err := exec.CommandContext(ctx, goPath, "version").CombinedOutput()
	if err != nil {
		return BuildReceipt{}, fmt.Errorf("probe go version: %w", err)
	}
	goVersion := strings.TrimSpace(string(verOut))

	srcDigest, modDigest, err := computeSourceAndModDigests(sourceDir)
	if err != nil {
		return BuildReceipt{}, err
	}
	goos, goarch := runtime.GOOS, runtime.GOARCH
	cacheKey := computeCacheKey(srcDigest, modDigest, goos, goarch, goVersion)

	finalDir := filepath.Join(home.State, "builds", toolID, cacheKey)
	finalBin := filepath.Join(finalDir, "plugin")
	receiptPath := filepath.Join(finalDir, "receipt.json")

	if data, err := os.ReadFile(receiptPath); err == nil {
		var existing BuildReceipt
		if json.Unmarshal(data, &existing) == nil && existing.BinaryPath == finalBin {
			if _, statErr := os.Stat(finalBin); statErr == nil {
				return existing, nil
			}
		}
	}

	if err := os.MkdirAll(filepath.Dir(finalDir), 0700); err != nil {
		return BuildReceipt{}, err
	}
	stageDir := fmt.Sprintf("%s.staging-%d", finalDir, time.Now().UnixNano())
	if err := os.MkdirAll(stageDir, 0700); err != nil {
		return BuildReceipt{}, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(stageDir)
		}
	}()

	if entry == "" {
		entry = "."
	}
	stageBin := filepath.Join(stageDir, "plugin")
	cmd := exec.CommandContext(ctx, goPath, "build", "-trimpath", "-o", stageBin, entry)
	cmd.Dir = sourceDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return BuildReceipt{}, &CompileError{Diagnostics: string(out), Err: err}
	}

	// Verify protocol version and manifest before activating.
	manifest, err := Describe(ctx, stageBin)
	if err != nil {
		return BuildReceipt{}, fmt.Errorf("verify plugin handshake: %w", err)
	}
	if toolID != "" && manifest.ID != "" && manifest.ID != toolID {
		return BuildReceipt{}, fmt.Errorf("plugin manifest ID %q does not match tool %q", manifest.ID, toolID)
	}

	binData, err := os.ReadFile(stageBin)
	if err != nil {
		return BuildReceipt{}, err
	}
	binSum := sha256.Sum256(binData)

	receipt := BuildReceipt{
		ToolID:       toolID,
		Version:      version,
		SourceDigest: srcDigest,
		ModuleDigest: modDigest,
		CacheKey:     cacheKey,
		GOOS:         goos,
		GOARCH:       goarch,
		GoVersion:    goVersion,
		BinaryPath:   finalBin,
		BinaryDigest: hex.EncodeToString(binSum[:]),
		BuiltAt:      time.Now().UnixNano(),
	}
	rawReceipt, _ := json.MarshalIndent(receipt, "", "  ")
	if err := os.WriteFile(filepath.Join(stageDir, "receipt.json"), rawReceipt, 0600); err != nil {
		return BuildReceipt{}, err
	}

	if err := os.Rename(stageDir, finalDir); err != nil {
		// Another concurrent builder may have promoted the identical cacheKey.
		if data, readErr := os.ReadFile(receiptPath); readErr == nil {
			var existing BuildReceipt
			if json.Unmarshal(data, &existing) == nil {
				return existing, nil
			}
		}
		return BuildReceipt{}, err
	}
	cleanup = false
	return receipt, nil
}

func computeSourceAndModDigests(root string) (string, string, error) {
	var files []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		return "", "", err
	}
	slices.Sort(files)

	srcH := sha256.New()
	modH := sha256.New()
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			return "", "", err
		}
		srcH.Write([]byte(rel + "\x00"))
		srcH.Write(data)
		if rel == "go.mod" || rel == "go.sum" {
			modH.Write([]byte(rel + "\x00"))
			modH.Write(data)
		}
	}
	return hex.EncodeToString(srcH.Sum(nil)), hex.EncodeToString(modH.Sum(nil)), nil
}

func computeCacheKey(srcDigest, modDigest, goos, goarch, goVersion string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%s|%s|%s", srcDigest, modDigest, goos, goarch, goVersion)
	return hex.EncodeToString(h.Sum(nil))[:24]
}
