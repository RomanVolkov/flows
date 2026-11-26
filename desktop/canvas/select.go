package canvas

import (
	"math"
	"slices"
	"strings"
)

// Direction represents screen-space directional navigation.
type Direction string

const (
	DirUp    Direction = "up"
	DirDown  Direction = "down"
	DirLeft  Direction = "left"
	DirRight Direction = "right"
)

// Selection tracks selected node IDs, primary focus, and pending removal confirmation.
type Selection struct {
	Items                map[string]bool
	Primary              string
	PendingRemoveIDs     []string
	ConfirmationOpen     bool
}

// NewSelection creates an empty Selection.
func NewSelection() *Selection {
	return &Selection{Items: make(map[string]bool)}
}

// Click handles single-click and Shift/Cmd toggle selection.
func (s *Selection) Click(nodeID string, toggle bool) {
	if nodeID == "" {
		if !toggle {
			s.Clear()
		}
		return
	}
	if toggle {
		if s.Items[nodeID] {
			delete(s.Items, nodeID)
			if s.Primary == nodeID {
				s.Primary = ""
			}
		} else {
			s.Items[nodeID] = true
			s.Primary = nodeID
		}
		return
	}
	s.Items = map[string]bool{nodeID: true}
	s.Primary = nodeID
}

// BandSelect selects all nodes intersecting screenRect (in view coordinates) at any zoom.
func (s *Selection) BandSelect(wl WorldLayout, cam Camera, screenRect Rect, additive bool) {
	p1 := cam.ScreenToWorld(Point{X: screenRect.X, Y: screenRect.Y})
	p2 := cam.ScreenToWorld(Point{X: screenRect.X + screenRect.Width, Y: screenRect.Y + screenRect.Height})
	worldRect := Rect{
		X:      math.Min(p1.X, p2.X),
		Y:      math.Min(p1.Y, p2.Y),
		Width:  math.Abs(p2.X - p1.X),
		Height: math.Abs(p2.Y - p1.Y),
	}
	if !additive {
		s.Items = make(map[string]bool)
		s.Primary = ""
	}
	var ids []string
	for id, nl := range wl.Nodes {
		if nl.Bounds.Intersects(worldRect) {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	for _, id := range ids {
		s.Items[id] = true
		s.Primary = id
	}
}

// SelectAll selects every node in wl.
func (s *Selection) SelectAll(wl WorldLayout) {
	s.Items = make(map[string]bool, len(wl.Nodes))
	var ids []string
	for id := range wl.Nodes {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		s.Items[id] = true
	}
	if len(ids) > 0 && s.Primary == "" {
		s.Primary = ids[0]
	}
}

// Clear deselects all nodes.
func (s *Selection) Clear() {
	s.Items = make(map[string]bool)
	s.Primary = ""
}

// RequestRemove opens a confirmation for removing the currently selected nodes.
func (s *Selection) RequestRemove() []string {
	if len(s.Items) == 0 {
		return nil
	}
	ids := make([]string, 0, len(s.Items))
	for id := range s.Items {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	s.PendingRemoveIDs = ids
	s.ConfirmationOpen = true
	return ids
}

// CancelRemove dismisses the pending removal without deleting any nodes.
func (s *Selection) CancelRemove() {
	s.PendingRemoveIDs = nil
	s.ConfirmationOpen = false
}

// ConfirmRemove returns the confirmed IDs and clears the confirmation state.
func (s *Selection) ConfirmRemove() []string {
	ids := s.PendingRemoveIDs
	s.PendingRemoveIDs = nil
	s.ConfirmationOpen = false
	for _, id := range ids {
		delete(s.Items, id)
		if s.Primary == id {
			s.Primary = ""
		}
	}
	return ids
}

// NavigateDirection moves focus to the best node in dir using a 45-degree cone
// preference with a half-plane directional fallback, and reveals it in cam.
func (s *Selection) NavigateDirection(wl WorldLayout, cam *Camera, viewSize Size, dir Direction) (string, bool) {
	if len(wl.Nodes) == 0 {
		return "", false
	}
	if s.Primary == "" {
		var ids []string
		for id := range wl.Nodes {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		s.Click(ids[0], false)
		revealNode(wl.Nodes[ids[0]], cam, viewSize)
		return ids[0], true
	}
	origin, ok := wl.Nodes[s.Primary]
	if !ok {
		return "", false
	}

	ux, uy := dirVector(dir)
	const coneCos = 0.70710678 // cos(45 deg)

	bestConeID := ""
	bestConeDist := math.Inf(1)
	bestFallbackID := ""
	bestFallbackDist := math.Inf(1)

	for id, nl := range wl.Nodes {
		if id == s.Primary {
			continue
		}
		dx := nl.Center.X - origin.Center.X
		dy := nl.Center.Y - origin.Center.Y
		dist := math.Hypot(dx, dy)
		if dist == 0 {
			continue
		}
		proj := dx*ux + dy*uy
		if proj <= 0 {
			continue
		}
		cosAngle := proj / dist
		if cosAngle >= coneCos {
			if dist < bestConeDist || (dist == bestConeDist && id < bestConeID) {
				bestConeDist = dist
				bestConeID = id
			}
		} else if dist < bestFallbackDist || (dist == bestFallbackDist && id < bestFallbackID) {
			bestFallbackDist = dist
			bestFallbackID = id
		}
	}

	target := bestConeID
	if target == "" {
		target = bestFallbackID
	}
	if target == "" {
		return "", false
	}
	s.Click(target, false)
	revealNode(wl.Nodes[target], cam, viewSize)
	return target, true
}

func dirVector(dir Direction) (float64, float64) {
	switch dir {
	case DirUp:
		return 0, -1
	case DirDown:
		return 0, 1
	case DirLeft:
		return -1, 0
	case DirRight:
		return 1, 0
	default:
		return 0, 1
	}
}

func revealNode(nl NodeLayout, cam *Camera, viewSize Size) {
	if cam == nil || viewSize.Width <= 0 || viewSize.Height <= 0 {
		return
	}
	tl := cam.WorldToScreen(Point{X: nl.Bounds.X, Y: nl.Bounds.Y})
	br := cam.WorldToScreen(Point{X: nl.Bounds.X + nl.Bounds.Width, Y: nl.Bounds.Y + nl.Bounds.Height})
	margin := 24.0
	if tl.X < margin {
		cam.Pan.X += margin - tl.X
	} else if br.X > viewSize.Width-margin {
		cam.Pan.X -= br.X - (viewSize.Width - margin)
	}
	if tl.Y < margin {
		cam.Pan.Y += margin - tl.Y
	} else if br.Y > viewSize.Height-margin {
		cam.Pan.Y -= br.Y - (viewSize.Height - margin)
	}
}

// ShortcutAction identifies a canvas keyboard shortcut outcome.
type ShortcutAction string

const (
	ShortcutSuppressed    ShortcutAction = "suppressed"
	ShortcutNone          ShortcutAction = "none"
	ShortcutSelectAll     ShortcutAction = "select_all"
	ShortcutClear         ShortcutAction = "clear"
	ShortcutOpenSession   ShortcutAction = "open_session"
	ShortcutRequestRemove ShortcutAction = "request_remove"
	ShortcutResetOffsets  ShortcutAction = "reset_offsets"
	ShortcutNavigate      ShortcutAction = "navigate"
)

// HandleShortcut processes canvas keyboard shortcuts unless suppressed by typing or modal dialogs.
func (s *Selection) HandleShortcut(key string, cmd, typing, modalOpen bool, wl WorldLayout, cam *Camera, viewSize Size, offsets map[string]Point) ShortcutAction {
	if typing || modalOpen {
		return ShortcutSuppressed
	}
	lower := strings.ToLower(key)
	switch {
	case cmd && lower == "a":
		s.SelectAll(wl)
		return ShortcutSelectAll
	case key == "Escape":
		s.Clear()
		return ShortcutClear
	case cmd && (key == "Return" || key == "\r" || key == "\n"):
		if s.Primary != "" {
			return ShortcutOpenSession
		}
		return ShortcutNone
	case cmd && (key == "Backspace" || key == "\x7f"):
		if len(s.RequestRemove()) > 0 {
			return ShortcutRequestRemove
		}
		return ShortcutNone
	case !cmd && lower == "r":
		ResetOffsets(s.Items, offsets)
		return ShortcutResetOffsets
	case key == "ArrowUp" || (!cmd && lower == "k"):
		s.NavigateDirection(wl, cam, viewSize, DirUp)
		return ShortcutNavigate
	case key == "ArrowDown" || (!cmd && lower == "j"):
		s.NavigateDirection(wl, cam, viewSize, DirDown)
		return ShortcutNavigate
	case key == "ArrowLeft" || (!cmd && lower == "h"):
		s.NavigateDirection(wl, cam, viewSize, DirLeft)
		return ShortcutNavigate
	case key == "ArrowRight" || (!cmd && lower == "l"):
		s.NavigateDirection(wl, cam, viewSize, DirRight)
		return ShortcutNavigate
	default:
		return ShortcutNone
	}
}
