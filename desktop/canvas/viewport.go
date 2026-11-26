package canvas

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
)

const (
	MinZoom    = 0.2
	MaxZoom    = 3.0
	MaxFitZoom = 1.0
	FitMargin  = 40.0
)

// Size represents 2D viewport dimensions in points.
type Size struct {
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// Camera stores pan, zoom, and whether the user has manually navigated.
type Camera struct {
	Pan          Point   `json:"pan"`
	Zoom         float64 `json:"zoom"`
	HasManualNav bool    `json:"has_manual_nav"`
}

// DefaultCamera returns an unnavigated 1x camera.
func DefaultCamera() Camera {
	return Camera{Zoom: 1.0}
}

// ClampZoom restricts zoom to [MinZoom, MaxZoom].
func ClampZoom(z float64) float64 {
	if z < MinZoom {
		return MinZoom
	}
	if z > MaxZoom {
		return MaxZoom
	}
	return z
}

// ScreenToWorld converts a view-space point to world coordinates.
func (c Camera) ScreenToWorld(screen Point) Point {
	z := c.Zoom
	if z <= 0 {
		z = 1.0
	}
	return Point{
		X: (screen.X - c.Pan.X) / z,
		Y: (screen.Y - c.Pan.Y) / z,
	}
}

// WorldToScreen converts a world point to view-space coordinates.
func (c Camera) WorldToScreen(world Point) Point {
	z := c.Zoom
	if z <= 0 {
		z = 1.0
	}
	return Point{
		X: world.X*z + c.Pan.X,
		Y: world.Y*z + c.Pan.Y,
	}
}

// PanBy translates the camera and marks manual navigation.
func (c *Camera) PanBy(dx, dy float64) {
	if c.Zoom <= 0 {
		c.Zoom = 1.0
	}
	c.Pan.X += dx
	c.Pan.Y += dy
	c.HasManualNav = true
}

// ZoomAt scales the camera around anchorScreen so the underlying world point
// stays fixed under the pointer.
func (c *Camera) ZoomAt(anchorScreen Point, factor float64) {
	if c.Zoom <= 0 {
		c.Zoom = 1.0
	}
	world := c.ScreenToWorld(anchorScreen)
	newZoom := ClampZoom(c.Zoom * factor)
	c.Zoom = newZoom
	c.Pan.X = anchorScreen.X - world.X*newZoom
	c.Pan.Y = anchorScreen.Y - world.Y*newZoom
	c.HasManualNav = true
}

// Fit adjusts pan and zoom to show worldBounds inside viewSize with margin,
// capping zoom at MaxFitZoom (1.0x).
func (c *Camera) Fit(worldBounds Rect, viewSize Size, margin float64, manual bool) {
	if worldBounds.Width <= 0 || worldBounds.Height <= 0 || viewSize.Width <= 0 || viewSize.Height <= 0 {
		c.Pan = Point{}
		c.Zoom = 1.0
		if manual {
			c.HasManualNav = true
		}
		return
	}
	availW := math.Max(1.0, viewSize.Width-2*margin)
	availH := math.Max(1.0, viewSize.Height-2*margin)
	z := math.Min(availW/worldBounds.Width, availH/worldBounds.Height)
	if z > MaxFitZoom {
		z = MaxFitZoom
	}
	if z < MinZoom {
		z = MinZoom
	}
	c.Zoom = z
	cx := worldBounds.X + worldBounds.Width/2
	cy := worldBounds.Y + worldBounds.Height/2
	c.Pan = Point{
		X: viewSize.Width/2 - cx*z,
		Y: viewSize.Height/2 - cy*z,
	}
	if manual {
		c.HasManualNav = true
	}
}

// OnGraphUpdate auto-fits initial data until the first manual navigation, then
// preserves the user's camera across streamed updates.
func (c *Camera) OnGraphUpdate(worldBounds Rect, viewSize Size) {
	if c.HasManualNav {
		return
	}
	c.Fit(worldBounds, viewSize, FitMargin, false)
}

// GestureAction identifies what a pointer/scroll/key gesture resolves to.
type GestureAction string

const (
	GestureNone     GestureAction = "none"
	GesturePan      GestureAction = "pan"
	GestureZoom     GestureAction = "zoom"
	GestureDragNode GestureAction = "drag_node"
)

// ResolvePointerDrag enforces pan priority over node dragging when Space is held
// or the middle mouse button is used.
func ResolvePointerDrag(spaceHeld bool, button int, onNode bool) GestureAction {
	if spaceHeld || button == 2 {
		return GesturePan
	}
	if onNode {
		return GestureDragNode
	}
	return GestureNone
}

// ResolveScroll determines whether a scroll wheel event pans or zooms.
func ResolveScroll(cmdHeld, ctrlHeld bool) GestureAction {
	if cmdHeld || ctrlHeld {
		return GestureZoom
	}
	return GesturePan
}

// CameraStore persists the workspace camera separately from template editor cameras.
type CameraStore struct {
	Workspace Camera            `json:"workspace"`
	Templates map[string]Camera `json:"templates,omitempty"`
}

// SaveCameraStore atomically persists workspace and template cameras to path.
func SaveCameraStore(path string, store CameraStore) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// LoadCameraStore loads persisted cameras from path, returning defaults if absent.
func LoadCameraStore(path string) (CameraStore, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return CameraStore{
				Workspace: DefaultCamera(),
				Templates: make(map[string]Camera),
			}, nil
		}
		return CameraStore{}, err
	}
	var cs CameraStore
	if err := json.Unmarshal(data, &cs); err != nil {
		return CameraStore{}, err
	}
	if cs.Templates == nil {
		cs.Templates = make(map[string]Camera)
	}
	return cs, nil
}
