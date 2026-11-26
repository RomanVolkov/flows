package exchange

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/romanvolkov/flows/internal/library"
	"github.com/romanvolkov/flows/internal/paths"
)

const (
	MaxBundleEntries           = 4096
	MaxBundleUncompressedBytes = 64 << 20
)

var (
	ErrMalformedBundle  = errors.New("malformed .flows bundle")
	ErrDigestMismatch   = errors.New("bundle payload checksum mismatch")
	ErrBundleLimit      = errors.New("bundle exceeds entry or size limit")
	ErrUnsafeBundlePath = errors.New("unsafe path or symlink in bundle")
)

// BundlePreview is the shared CLI/app preview of a .flows ZIP or unpacked directory.
type BundlePreview struct {
	SourcePath       string               `json:"source_path"`
	Manifest         BundleManifest       `json:"manifest"`
	Status           library.ImportStatus `json:"status"`
	BuildRequired    bool                 `json:"build_required"`
	EngineCompatible bool                 `json:"engine_compatible"`
	CredentialGaps   []string             `json:"credential_gaps,omitempty"`
	payloads         map[string][]byte
}

// PreviewBundle parses and verifies a .flows ZIP or unpacked manifest directory
// without writing to home or executing any build.
func PreviewBundle(home paths.Home, sourcePath string) (BundlePreview, error) {
	info, err := os.Stat(sourcePath)
	if err != nil {
		return BundlePreview{}, err
	}

	var payloads map[string][]byte
	if info.IsDir() {
		payloads, err = readUnpackedBundle(sourcePath)
	} else {
		payloads, err = readZipBundle(sourcePath)
	}
	if err != nil {
		return BundlePreview{}, err
	}

	rawManifest, ok := payloads["bundle.json"]
	if !ok {
		return BundlePreview{}, fmt.Errorf("%w: missing bundle.json", ErrMalformedBundle)
	}
	var manifest BundleManifest
	if err := json.Unmarshal(rawManifest, &manifest); err != nil {
		return BundlePreview{}, fmt.Errorf("%w: invalid bundle.json: %v", ErrMalformedBundle, err)
	}
	if manifest.Schema != BundleSchemaVersion || manifest.EntryID == "" {
		return BundlePreview{}, fmt.Errorf("%w: unsupported bundle schema %q", ErrMalformedBundle, manifest.Schema)
	}

	// Verify all payload checksums before any build.
	for path, expected := range manifest.PayloadChecksums {
		data, exists := payloads[path]
		if !exists {
			return BundlePreview{}, fmt.Errorf("%w: missing payload %s", ErrDigestMismatch, path)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != expected {
			return BundlePreview{}, fmt.Errorf("%w: checksum mismatch for %s", ErrDigestMismatch, path)
		}
	}

	status := library.StatusNew
	targetDir := filepath.Join(home.Workflows(), manifest.EntryID)
	if manifest.EntryKind == "tool" {
		targetDir = filepath.Join(home.Tools(), manifest.EntryID)
	}
	if prev, err := library.Preview(home, targetDir); err == nil {
		var entryDigest string
		for _, p := range manifest.Packages {
			if p.ID == manifest.EntryID {
				entryDigest = p.Digest
				break
			}
		}
		if entryDigest != "" && prev.Digest == entryDigest {
			status = library.StatusIdentical
		} else {
			status = library.StatusConflict
		}
	}

	buildRequired := false
	for _, p := range manifest.Packages {
		if p.Kind == "tool" {
			buildRequired = true
			break
		}
	}

	return BundlePreview{
		SourcePath:       sourcePath,
		Manifest:         manifest,
		Status:           status,
		BuildRequired:    buildRequired,
		EngineCompatible: strings.Contains(manifest.EngineCompatibility, "1."),
		CredentialGaps:   manifest.ExternalPrerequisites.ProviderAccounts,
		payloads:         payloads,
	}, nil
}

func readZipBundle(zipPath string) (map[string][]byte, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedBundle, err)
	}
	defer r.Close()

	if len(r.File) > MaxBundleEntries {
		return nil, fmt.Errorf("%w: %d entries", ErrBundleLimit, len(r.File))
	}

	out := make(map[string][]byte, len(r.File))
	var totalBytes int64
	for _, f := range r.File {
		if f.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%w: symlink %s", ErrUnsafeBundlePath, f.Name)
		}
		clean := filepath.ToSlash(filepath.Clean(f.Name))
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
			return nil, fmt.Errorf("%w: %s", ErrUnsafeBundlePath, f.Name)
		}
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		limited := io.LimitReader(rc, MaxBundleUncompressedBytes-totalBytes+1)
		data, err := io.ReadAll(limited)
		_ = rc.Close()
		if err != nil {
			return nil, err
		}
		totalBytes += int64(len(data))
		if totalBytes > MaxBundleUncompressedBytes {
			return nil, fmt.Errorf("%w: uncompressed size exceeds limit", ErrBundleLimit)
		}
		out[clean] = data
	}
	return out, nil
}

func readUnpackedBundle(dir string) (map[string][]byte, error) {
	out := make(map[string][]byte)
	var totalBytes int64
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: symlink %s", ErrUnsafeBundlePath, p)
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil || rel == "." {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if len(out) >= MaxBundleEntries {
			return ErrBundleLimit
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		totalBytes += int64(len(data))
		if totalBytes > MaxBundleUncompressedBytes {
			return ErrBundleLimit
		}
		out[filepath.ToSlash(rel)] = data
		return nil
	})
	return out, err
}
