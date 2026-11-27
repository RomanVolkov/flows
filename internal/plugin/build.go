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

var ErrMissingGoCompiler = errors.New("go compiler not found; a Go toolchain is required on the host machine")

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

// CheckGoCompiler resolves goBin (defaulting to "go") on the host machine and verifies it responds cleanly.
func CheckGoCompiler(ctx context.Context, goBin string) (string, string, error) {
	if goBin == "" {
		goBin = "go"
	}
	goPath, err := resolveGoBinary(goBin)
	if err != nil {
		return "", "", fmt.Errorf("%w: %s (%v)", ErrMissingGoCompiler, goBin, err)
	}
	verOut, err := exec.CommandContext(ctx, goPath, "version").CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("probe go version: %w", err)
	}
	return goPath, strings.TrimSpace(string(verOut)), nil
}

func resolveGoBinary(goBin string) (string, error) {
	if p, err := exec.LookPath(goBin); err == nil {
		return p, nil
	} else if goBin != "go" {
		return "", err
	}
	// GUI bundles and launchd jobs on macOS may run with a minimal PATH that omits Homebrew or /usr/local/go.
	for _, candidate := range []string{"/opt/homebrew/bin/go", "/usr/local/go/bin/go", "/usr/local/bin/go"} {
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() && info.Mode().Perm()&0111 != 0 {
			return candidate, nil
		}
	}
	return "", errors.New("executable file not found in $PATH")
}

// BuildTool compiles a Go tool package into a temporary staging directory,
// verifies its protocol handshake and manifest, and promotes it atomically.
func BuildTool(ctx context.Context, home paths.Home, toolID, version, sourceDir, entry, goBin string) (BuildReceipt, error) {
	goPath, goVersion, err := CheckGoCompiler(ctx, goBin)
	if err != nil {
		return BuildReceipt{}, err
	}

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

	resolvedEntry := resolveEntry(sourceDir, entry)
	stageBin := filepath.Join(stageDir, "plugin")
	args := []string{"build", "-trimpath", "-buildvcs=false"}
	if modfile, err := prepareStageModfile(sourceDir, stageDir, home); err != nil {
		return BuildReceipt{}, err
	} else if modfile != "" {
		args = append(args, "-modfile="+modfile)
	}
	args = append(args, "-o", stageBin, resolvedEntry)

	cmd := exec.CommandContext(ctx, goPath, args...)
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

func resolveEntry(sourceDir, entry string) string {
	if entry != "" {
		return entry
	}
	if hasGoFiles(sourceDir) {
		return "."
	}
	if hasGoFiles(filepath.Join(sourceDir, "cmd", "plugin")) {
		return "./cmd/plugin"
	}
	return "."
}

func hasGoFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
			return true
		}
	}
	return false
}

const flowsGoSDKModule = "github.com/romanvolkov/flows/sdk/go"

func prepareStageModfile(sourceDir, stageDir string, home paths.Home) (string, error) {
	raw, err := os.ReadFile(filepath.Join(sourceDir, "go.mod"))
	if err != nil || !strings.Contains(string(raw), flowsGoSDKModule) {
		return "", nil
	}
	if target, ok := extractReplaceTarget(string(raw), flowsGoSDKModule); ok {
		resolved := target
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(sourceDir, filepath.FromSlash(target))
		}
		if _, err := os.Stat(filepath.Join(resolved, "go.mod")); err == nil {
			return "", nil
		}
	}
	sdkDir, err := filepath.Abs(filepath.Join(home.Spec(), "v1", "sdk", "go"))
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(sdkDir, "go.mod")); err != nil {
		return "", nil
	}
	rewritten := rewriteSDKReplace(string(raw), flowsGoSDKModule, sdkDir)
	stageMod := filepath.Join(stageDir, "go.mod")
	if err := os.WriteFile(stageMod, []byte(rewritten), 0600); err != nil {
		return "", err
	}
	if sumData, err := os.ReadFile(filepath.Join(sourceDir, "go.sum")); err == nil {
		if err := os.WriteFile(filepath.Join(stageDir, "go.sum"), sumData, 0600); err != nil {
			return "", err
		}
	}
	return stageMod, nil
}

func extractReplaceTarget(modContent, modPath string) (string, bool) {
	for _, line := range strings.Split(modContent, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "replace ") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "replace "))
		left, right, ok := strings.Cut(rest, "=>")
		if !ok {
			continue
		}
		if fields := strings.Fields(left); len(fields) > 0 && fields[0] == modPath {
			fields := strings.Fields(right)
			if len(fields) > 0 {
				return fields[0], true
			}
		}
	}
	return "", false
}

func rewriteSDKReplace(modContent, modPath, localSDKDir string) string {
	replacement := fmt.Sprintf("replace %s => %s", modPath, filepath.ToSlash(localSDKDir))
	lines := strings.Split(modContent, "\n")
	replaced := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "replace ") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "replace "))
		left, _, ok := strings.Cut(rest, "=>")
		if ok && len(strings.Fields(left)) > 0 && strings.Fields(left)[0] == modPath {
			lines[i] = replacement
			replaced = true
		}
	}
	if !replaced {
		lines = append(lines, "", replacement)
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n"
}
