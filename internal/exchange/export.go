package exchange

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

var deterministicModTime = time.Unix(0, 0).UTC()

// Export resolves entryDir's transitive dependencies and writes a deterministic,
// atomic .flows ZIP archive to outPath.
func Export(entryDir, outPath string, opts ExportOptions) (BundleManifest, error) {
	resolved, err := resolveClosure(entryDir, opts)
	if err != nil {
		return BundleManifest{}, err
	}

	if err := os.MkdirAll(filepath.Dir(outPath), 0700); err != nil {
		return BundleManifest{}, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(outPath), ".flows-export-*")
	if err != nil {
		return BundleManifest{}, err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()

	zw := zip.NewWriter(tmp)
	manifestBytes, err := json.MarshalIndent(resolved.manifest, "", "  ")
	if err != nil {
		_ = zw.Close()
		_ = tmp.Close()
		return BundleManifest{}, err
	}
	if err := writeZipEntry(zw, "bundle.json", manifestBytes, 0644); err != nil {
		_ = zw.Close()
		_ = tmp.Close()
		return BundleManifest{}, err
	}
	for _, f := range resolved.files {
		mode := f.mode
		if mode&0111 != 0 {
			mode = 0755
		} else {
			mode = 0644
		}
		if err := writeZipEntry(zw, f.zipPath, f.data, mode); err != nil {
			_ = zw.Close()
			_ = tmp.Close()
			return BundleManifest{}, err
		}
	}
	if err := zw.Close(); err != nil {
		_ = tmp.Close()
		return BundleManifest{}, err
	}
	if err := tmp.Chmod(0644); err != nil {
		_ = tmp.Close()
		return BundleManifest{}, err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return BundleManifest{}, err
	}
	if err := tmp.Close(); err != nil {
		return BundleManifest{}, err
	}
	if err := os.Rename(tmpName, outPath); err != nil {
		return BundleManifest{}, err
	}
	cleanup = false
	return resolved.manifest, nil
}

func writeZipEntry(zw *zip.Writer, name string, data []byte, mode os.FileMode) error {
	hdr := &zip.FileHeader{
		Name:     name,
		Method:   zip.Deflate,
		Modified: deterministicModTime,
	}
	hdr.SetMode(mode)
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}
