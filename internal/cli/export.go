package cli

import (
	"github.com/romanvolkov/flows/internal/catalog"
	"github.com/romanvolkov/flows/internal/exchange"
)

// ExportArchive exports entryDir to outPath as a portable .flows ZIP archive.
func ExportArchive(entryDir, outPath string, cat *catalog.Registry, sdkDir string) (exchange.BundleManifest, error) {
	return exchange.Export(entryDir, outPath, exchange.ExportOptions{
		Catalog: cat,
		SDKDir:  sdkDir,
	})
}
