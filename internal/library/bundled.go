package library

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/romanvolkov/flows/internal/paths"
	"github.com/romanvolkov/flows/internal/plugin"
	"golang.org/x/sys/unix"
)

// InstallBundled copies starter packages into the editable library once per Flows home.
func InstallBundled(home paths.Home, resources string) error {
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

	if _, err := os.Stat(marker); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
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
			if err := installBundledPackage(home, filepath.Join(root, entry.Name()), bundle.kind); err != nil {
				return fmt.Errorf("install bundled %s %s: %w", bundle.kind, entry.Name(), err)
			}
		}
	}
	return os.WriteFile(marker, nil, 0600)
}

func installBundledPackage(home paths.Home, source string, kind PackageKind) error {
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
	switch {
	case os.IsNotExist(targetErr) && os.IsNotExist(aliasErr):
		if _, err := Import(home, source, ImportOptions{}); err != nil {
			return err
		}
	case targetErr == nil && aliasErr == nil && prev.Status == StatusIdentical:
		// Recover if installation stopped between importing the files and activating the binary.
	default:
		return nil
	}
	if kind == KindTool {
		binary := filepath.Join(RevisionPath(home, kind, prev.ID, prev.Digest), prev.ID+"-plugin")
		return plugin.ActivateBundled(home, prev.ID, prev.Version, prev.TargetDir, binary)
	}
	return nil
}
