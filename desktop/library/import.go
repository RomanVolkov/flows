// Package library handles desktop .flows archive open-file and drag-and-drop previews.
package library

import (
	"net/url"
	"strings"

	"github.com/romanvolkov/flows/internal/exchange"
	"github.com/romanvolkov/flows/internal/paths"
)

// ImportModalState holds the shared preview shown by Finder open-file or drag-and-drop.
type ImportModalState struct {
	Open    bool                   `json:"open"`
	Source  string                 `json:"source"` // "finder" | "drag_drop"
	Preview exchange.BundlePreview `json:"preview"`
}

// ImportController routes both Finder file:// URLs and drag-and-drop paths
// through the shared exchange.PreviewBundle parser.
type ImportController struct {
	Home  paths.Home
	Modal ImportModalState
}

// OpenFinderURL handles macOS NSApplicationDelegate application:openURLs:.
func (c *ImportController) OpenFinderURL(rawURL string) (exchange.BundlePreview, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return exchange.BundlePreview{}, err
	}
	path := u.Path
	if u.Scheme != "file" {
		path = strings.TrimPrefix(rawURL, "file://")
	}
	return c.openPreview(path, "finder")
}

// DropArchivePath handles drag-and-drop onto the Flows window.
func (c *ImportController) DropArchivePath(path string) (exchange.BundlePreview, error) {
	return c.openPreview(path, "drag_drop")
}

// Dismiss closes the preview modal without modifying the user library.
func (c *ImportController) Dismiss() {
	c.Modal.Open = false
}

func (c *ImportController) openPreview(path, source string) (exchange.BundlePreview, error) {
	prev, err := exchange.PreviewBundle(c.Home, path)
	if err != nil {
		return exchange.BundlePreview{}, err
	}
	c.Modal = ImportModalState{
		Open:    true,
		Source:  source,
		Preview: prev,
	}
	return prev, nil
}
