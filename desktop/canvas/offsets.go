package canvas

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
)

// DragThresholdPoints is the minimum pointer travel before a node drag begins.
const DragThresholdPoints = 3.0

// ShouldStartDrag reports whether pointer movement exceeds the 3-point drag threshold.
func ShouldStartDrag(startScreen, currentScreen Point) bool {
	return math.Hypot(currentScreen.X-startScreen.X, currentScreen.Y-startScreen.Y) >= DragThresholdPoints
}

// MoveSelected moves each selected root once, carries its descendants, records
// saved offsets, and pushes overlapping automatic siblings clear.
func MoveSelected(wl *WorldLayout, graph Graph, selected map[string]bool, deltaWorld Point, offsets map[string]Point) {
	if len(selected) == 0 || (deltaWorld.X == 0 && deltaWorld.Y == 0) {
		return
	}
	if offsets == nil {
		offsets = make(map[string]Point)
	}

	childrenOf := make(map[string][]string)
	parentOf := make(map[string]string, len(graph.Nodes))
	for _, n := range graph.Nodes {
		if n.ParentID != "" {
			parentOf[n.ID] = n.ParentID
			childrenOf[n.ParentID] = append(childrenOf[n.ParentID], n.ID)
		}
	}

	// Only move selected roots (nodes whose ancestor is not also selected)
	// so selecting both a parent and child moves the child once.
	moved := make(map[string]bool)
	var collectSubtree func(id string)
	collectSubtree = func(id string) {
		if moved[id] {
			return
		}
		moved[id] = true
		for _, childID := range childrenOf[id] {
			collectSubtree(childID)
		}
	}

	for id := range selected {
		if hasSelectedAncestor(id, parentOf, selected) {
			continue
		}
		collectSubtree(id)
	}

	for id := range moved {
		nl, ok := wl.Nodes[id]
		if !ok {
			continue
		}
		nl.Bounds.X += deltaWorld.X
		nl.Bounds.Y += deltaWorld.Y
		nl.LabelBounds.X += deltaWorld.X
		nl.LabelBounds.Y += deltaWorld.Y
		nl.Center = nl.Bounds.Center()
		wl.Nodes[id] = nl

		prev := offsets[id]
		offsets[id] = Point{X: prev.X + deltaWorld.X, Y: prev.Y + deltaWorld.Y}
	}

	// Push unpinned automatic siblings clear if a moved node overlaps them.
	const siblingGap = 24.0
	for movedID := range moved {
		mNL := wl.Nodes[movedID]
		for sibID, sNL := range wl.Nodes {
			if moved[sibID] {
				continue
			}
			if _, pinned := offsets[sibID]; pinned {
				continue
			}
			if sNL.Bounds.Intersects(mNL.Bounds) {
				shiftX := (mNL.Bounds.X + mNL.Bounds.Width + siblingGap) - sNL.Bounds.X
				if shiftX > 0 {
					sNL.Bounds.X += shiftX
					sNL.LabelBounds.X += shiftX
					sNL.Center = sNL.Bounds.Center()
					wl.Nodes[sibID] = sNL
				}
			}
		}
	}
}

func hasSelectedAncestor(id string, parentOf map[string]string, selected map[string]bool) bool {
	cur := parentOf[id]
	seen := make(map[string]bool)
	for cur != "" && !seen[cur] {
		seen[cur] = true
		if selected[cur] {
			return true
		}
		cur = parentOf[cur]
	}
	return false
}

// InheritPredecessorOffset returns the saved offset of a node's first causal
// predecessor when the node itself does not yet have a manual offset.
func InheritPredecessorOffset(nodeID string, graph Graph, offsets map[string]Point) (Point, bool) {
	if off, ok := offsets[nodeID]; ok {
		return off, true
	}
	for _, e := range graph.Edges {
		if e.ToID == nodeID && e.Kind == EdgeDependency {
			if off, ok := offsets[e.FromID]; ok {
				return off, true
			}
		}
	}
	return Point{}, false
}

// ResetOffsets clears saved offsets for selected nodes, or all offsets when
// nothing is selected.
func ResetOffsets(selected map[string]bool, offsets map[string]Point) {
	if len(offsets) == 0 {
		return
	}
	if len(selected) == 0 {
		for k := range offsets {
			delete(offsets, k)
		}
		return
	}
	for id := range selected {
		delete(offsets, id)
	}
}

// SaveOffsets persists node drag offsets to path.
func SaveOffsets(path string, offsets map[string]Point) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(offsets, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// LoadOffsets restores node drag offsets from path.
func LoadOffsets(path string) (map[string]Point, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]Point), nil
		}
		return nil, err
	}
	var out map[string]Point
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = make(map[string]Point)
	}
	return out, nil
}
