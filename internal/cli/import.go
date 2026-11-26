package cli

import (
	"github.com/romanvolkov/flows/internal/library"
	"github.com/romanvolkov/flows/internal/paths"
)

// PreviewImport returns the import preview for a local workflow or tool directory.
func PreviewImport(home paths.Home, dir string) (library.PreviewResult, error) {
	return library.Preview(home, dir)
}

// ImportPackage imports a local workflow or tool directory into home.
func ImportPackage(home paths.Home, dir string, overwrite bool) (library.ImportReceipt, error) {
	return library.Import(home, dir, library.ImportOptions{AllowOverwrite: overwrite})
}
