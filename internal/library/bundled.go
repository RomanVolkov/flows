package library

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/romanvolkov/flows/internal/paths"
	"github.com/romanvolkov/flows/internal/plugin"
	"golang.org/x/sys/unix"
)

const bundledMarkerVersion = "source-v4\n"

// Upgrade only working copies that still match a shipped workflow revision.
var previousBundledWorkflows = map[string][]string{
	"deliver-change": {
		"b93f1c12d9235f6e7baf343fdc4efcf9d8d93d341c09dd727d048d2fd77c0501",
		"6fc45874c5d072e547f5025eb437f8a0903278ef596ec19ca290752a4856a6c4",
	},
	"review-open-prs": {
		"6026fbe08b9b63f3980e1b0e6728bf233a29f988c244056cf7db6e352aba5cb5",
		"d44eb186370914d4c52adff4bbedb65dfd344d40de58db6714003293c32e437d",
		"97496552e19f4c1e889cd7e113c3ace64be8a7932f7fb2e76887ca3d26318017",
	},
	"validate-package": {
		"3badd7bab4e52761b179c496e73ca0ffcf2f258bc8d69ffd691c6c760e6ea132",
		"58bbeb1a991df358861d61b3cf4838a5ba3e7581b4f312295bf3d46514444f48",
	},
}

var previousBundledTools = map[string]string{
	"git":    "6ece8cf1abf7dc3a7e6e1897c456c033d0130e55afed8af5410ae33b5c11f104",
	"github": "5b8ef86eff3236599395468f57fe13da012f5b8c89a57ff69ebd5a71aacf478c",
	"gitlab": "df71b02c41c5080f300eca8bd6d1aa4af1f8b5a2de6673b204d69114dd992eb1",
}

// InstallBundled copies starter packages into the editable library once per bundle
// format version. Bundled tools with a prebuilt executable binary are activated directly;
// bundled tools distributed as Go source (without a binary) require the host Go compiler
// and are compiled from source.
func InstallBundled(home paths.Home, resources string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	return installBundled(ctx, home, resources, "")
}

func installBundled(ctx context.Context, home paths.Home, resources, goBin string) error {
	if err := home.Init(); err != nil {
		return err
	}

	marker := filepath.Join(home.State, "bundled-library-installed")
	fd, err := unix.Open(marker+".lock", unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	if err := unix.Flock(fd, unix.LOCK_EX); err != nil {
		return err
	}

	data, err := os.ReadFile(marker)
	if err == nil && string(data) == bundledMarkerVersion {
		return nil
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}

	// Existing installations only need the affected starter packages updated.
	if string(data) == "source-v1\n" || string(data) == "source-v2\n" || string(data) == "source-v3\n" {
		for id := range previousBundledTools {
			if err := installBundledPackage(ctx, home, filepath.Join(resources, "plugins", id), KindTool, goBin); err != nil {
				return err
			}
		}
		for id := range previousBundledWorkflows {
			if err := installBundledPackage(ctx, home, filepath.Join(resources, "workflows", id), KindWorkflow, goBin); err != nil {
				return err
			}
		}
		return os.WriteFile(marker, []byte(bundledMarkerVersion), 0600)
	}

	// Only require a host Go compiler if at least one bundled tool lacks a prebuilt binary.
	needsCompiler, err := anySourceOnlyPlugin(filepath.Join(resources, "plugins"))
	if err != nil {
		return err
	}
	if needsCompiler {
		if _, _, err := plugin.CheckGoCompiler(ctx, goBin); err != nil {
			return err
		}
	}

	// Sync bundled spec/v1 (including sdk/go) so offline tool compilation works even without a repo checkout.
	srcSpec := filepath.Join(resources, "spec", "v1")
	if info, err := os.Stat(srcSpec); err == nil && info.IsDir() {
		if err := syncDir(srcSpec, filepath.Join(home.Spec(), "v1")); err != nil {
			return fmt.Errorf("sync bundled spec/v1: %w", err)
		}
	}

	for _, bundle := range []struct {
		dir  string
		kind PackageKind
	}{{"plugins", KindTool}, {"workflows", KindWorkflow}} {
		root := filepath.Join(resources, bundle.dir)
		entries, err := os.ReadDir(root)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			if err := installBundledPackage(ctx, home, filepath.Join(root, entry.Name()), bundle.kind, goBin); err != nil {
				return fmt.Errorf("install bundled %s %s: %w", bundle.kind, entry.Name(), err)
			}
		}
	}
	return os.WriteFile(marker, []byte(bundledMarkerVersion), 0600)
}

func anySourceOnlyPlugin(pluginsRoot string) (bool, error) {
	entries, err := os.ReadDir(pluginsRoot)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if !hasExecutableBinary(filepath.Join(pluginsRoot, entry.Name(), entry.Name()+"-plugin")) {
			return true, nil
		}
	}
	return false, nil
}

func hasExecutableBinary(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0100 != 0
}

func installBundledPackage(ctx context.Context, home paths.Home, source string, kind PackageKind, goBin string) error {
	prev, err := Preview(home, source)
	if err != nil {
		return err
	}
	if prev.Kind != kind || prev.ID != filepath.Base(source) {
		return fmt.Errorf("manifest kind and id must match the bundle location")
	}
	alias := filepath.Join(home.State, "aliases", string(kind), prev.ID+".json")
	_, targetErr := os.Lstat(prev.TargetDir)
	_, aliasErr := os.Stat(alias)
	if targetErr != nil && !os.IsNotExist(targetErr) {
		return targetErr
	}
	if aliasErr != nil && !os.IsNotExist(aliasErr) {
		return aliasErr
	}

	shouldActivate := false
	switch {
	case os.IsNotExist(targetErr) && os.IsNotExist(aliasErr):
		if _, err := Import(home, source, ImportOptions{}); err != nil {
			return err
		}
		shouldActivate = true
	case targetErr == nil && aliasErr == nil && prev.Status == StatusIdentical:
		// Recover if installation stopped before compilation/activation, or upgrade identical sources.
		shouldActivate = true
	case targetErr == nil && aliasErr == nil && kind == KindWorkflow && prev.Status == StatusConflict:
		if slices.Contains(previousBundledWorkflows[prev.ID], prev.ExistingDigest) {
			_, err := Import(home, source, ImportOptions{AllowOverwrite: true})
			return err
		}
	case targetErr == nil && aliasErr == nil && kind == KindTool && prev.Status == StatusConflict:
		// Upgrade legacy prebuilt binary-only bundles cleanly while preserving user-modified tool sources.
		if prev.ExistingDigest == previousBundledTools[prev.ID] || isLegacyPrebuiltBundle(home, prev.ID, prev.TargetDir) {
			if _, err := Import(home, source, ImportOptions{AllowOverwrite: true}); err != nil {
				return err
			}
			shouldActivate = true
		}
	default:
		return nil
	}

	if kind != KindTool || !shouldActivate {
		return nil
	}

	// Support both distribution formats: activate prebuilt binary when included,
	// otherwise compile from source using the host Go compiler.
	prebuilt := filepath.Join(RevisionPath(home, kind, prev.ID, prev.Digest), prev.ID+"-plugin")
	if hasExecutableBinary(prebuilt) {
		return plugin.ActivateBundled(home, prev.ID, prev.Version, prev.TargetDir, prebuilt)
	}
	_, err = plugin.Install(ctx, home, prev.ID, prev.Version, prev.TargetDir, plugin.InstallOptions{
		Bundled:       true,
		PackageDigest: prev.Digest,
		GoBin:         goBin,
	})
	return err
}

func isLegacyPrebuiltBundle(home paths.Home, toolID, targetDir string) bool {
	active, err := plugin.LoadActiveTool(home, toolID)
	if err != nil || !strings.HasPrefix(active.Receipt.CacheKey, "bundle-") {
		return false
	}
	return !hasGoSourceFiles(targetDir)
}

func hasGoSourceFiles(root string) bool {
	found := false
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.HasSuffix(d.Name(), ".go") {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	return found
}

func syncDir(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: %s", ErrEscapingPath, p)
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0700)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(out), 0700); err != nil {
			return err
		}
		return os.WriteFile(out, data, 0600)
	})
}
