package canvas

import (
	"math"
	"path/filepath"
	"testing"
)

func TestZoomClampAnchoredPointAndFit(t *testing.T) {
	cam := DefaultCamera()
	anchor := Point{X: 320, Y: 240}
	worldBefore := cam.ScreenToWorld(anchor)

	// Zoom in around anchor; world point under pointer must stay fixed.
	cam.ZoomAt(anchor, 2.0)
	worldAfter := cam.ScreenToWorld(anchor)
	if math.Abs(worldBefore.X-worldAfter.X) > 1e-6 || math.Abs(worldBefore.Y-worldAfter.Y) > 1e-6 {
		t.Fatalf("anchored world point shifted: before=%+v after=%+v", worldBefore, worldAfter)
	}

	// Clamp to [0.2, 3.0].
	cam.ZoomAt(anchor, 100.0)
	if cam.Zoom != MaxZoom {
		t.Fatalf("zoom = %.2f, want MaxZoom %.2f", cam.Zoom, MaxZoom)
	}
	cam.ZoomAt(anchor, 0.001)
	if cam.Zoom != MinZoom {
		t.Fatalf("zoom = %.2f, want MinZoom %.2f", cam.Zoom, MinZoom)
	}

	// Empty fit resets to 1x.
	cam.Fit(Rect{}, Size{Width: 800, Height: 600}, FitMargin, false)
	if cam.Zoom != 1.0 {
		t.Fatalf("empty fit zoom = %.2f, want 1.0", cam.Zoom)
	}

	// Small world fit caps at MaxFitZoom (1.0x).
	cam.Fit(Rect{X: 0, Y: 0, Width: 200, Height: 100}, Size{Width: 1000, Height: 800}, FitMargin, false)
	if cam.Zoom != MaxFitZoom {
		t.Fatalf("small fit zoom = %.2f, want 1.0", cam.Zoom)
	}

	// Large world fit uses 40pt margin.
	cam.Fit(Rect{X: 0, Y: 0, Width: 2000, Height: 1000}, Size{Width: 1080, Height: 580}, FitMargin, true)
	if math.Abs(cam.Zoom-0.5) > 1e-6 {
		t.Fatalf("large fit zoom = %.4f, want 0.5", cam.Zoom)
	}
}

func TestAutoFitPreservesManualCameraAndSeparatePersistence(t *testing.T) {
	cam := DefaultCamera()
	view := Size{Width: 1000, Height: 800}

	// Initial update auto-fits.
	cam.OnGraphUpdate(Rect{X: 0, Y: 0, Width: 2000, Height: 1000}, view)
	if cam.HasManualNav || cam.Zoom >= 1.0 {
		t.Fatalf("expected initial auto-fit, got %+v", cam)
	}

	// User pans; subsequent streamed node updates must preserve camera.
	cam.PanBy(120, -40)
	savedPan := cam.Pan
	savedZoom := cam.Zoom
	cam.OnGraphUpdate(Rect{X: 0, Y: 0, Width: 4000, Height: 3000}, view)
	if cam.Pan != savedPan || cam.Zoom != savedZoom {
		t.Fatalf("manual camera overwritten on graph update: got %+v, want pan=%+v zoom=%.2f", cam, savedPan, savedZoom)
	}

	// Gesture priority: Space-drag and middle-drag pan even over a node.
	if act := ResolvePointerDrag(true, 0, true); act != GesturePan {
		t.Fatalf("Space-drag over node: got %s, want pan", act)
	}
	if act := ResolvePointerDrag(false, 2, true); act != GesturePan {
		t.Fatalf("middle-drag over node: got %s, want pan", act)
	}
	if act := ResolvePointerDrag(false, 0, true); act != GestureDragNode {
		t.Fatalf("normal drag over node: got %s, want drag_node", act)
	}

	// Separate persistence for workspace camera vs template cameras.
	path := filepath.Join(t.TempDir(), "cameras.json")
	cs := CameraStore{
		Workspace: cam,
		Templates: map[string]Camera{
			"review-open-prs": {Pan: Point{X: 10, Y: 20}, Zoom: 1.25, HasManualNav: true},
		},
	}
	if err := SaveCameraStore(path, cs); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadCameraStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Workspace.Pan != cam.Pan || loaded.Templates["review-open-prs"].Zoom != 1.25 {
		t.Fatalf("camera store mismatch: %+v", loaded)
	}
}
