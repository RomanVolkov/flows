package canvas

import (
	"math"
	"time"
)

const AnimationFPS = 30
const AnimationInterval = time.Second / AnimationFPS

// ShapeKind is the geometric silhouette drawn for a node kind.
type ShapeKind string

const (
	ShapeCircle  ShapeKind = "circle"
	ShapeSquare  ShapeKind = "square"
	ShapeDiamond ShapeKind = "diamond"
	ShapeHexagon ShapeKind = "hexagon"
)

// EdgeStyle describes how an edge is stroked in Core Graphics.
type EdgeStyle string

const (
	StyleSolidArrow    EdgeStyle = "solid_arrow"
	StyleFaintDashed   EdgeStyle = "faint_dashed"
	StyleDottedHistory EdgeStyle = "dotted_history"
)

// AccessibilityItem exposes a node's semantic role, state, and actions.
type AccessibilityItem struct {
	NodeID  string   `json:"node_id"`
	Role    string   `json:"role"`
	Label   string   `json:"label"`
	State   string   `json:"state"`
	Frame   Rect     `json:"frame"`
	Actions []string `json:"actions"`
}

// RenderEdge is a resolved edge ready for Core Graphics drawing.
type RenderEdge struct {
	ID         string    `json:"id"`
	Style      EdgeStyle `json:"style"`
	Alpha      float64   `json:"alpha"`
	From       Point     `json:"from"`
	To         Point     `json:"to"`
	Points     []Point   `json:"points"`
	DrawArrow  bool      `json:"draw_arrow"`
	Label      string    `json:"label,omitempty"`
	LabelPos   Point     `json:"label_pos"`
}

// RenderNode is a resolved node ready for Core Graphics drawing.
type RenderNode struct {
	ID              string            `json:"id"`
	Shape           ShapeKind         `json:"shape"`
	Bounds          Rect              `json:"bounds"`
	ClippedTitle    string            `json:"clipped_title"`
	ClippedDetail   string            `json:"clipped_detail,omitempty"`
	SelectionRing   bool              `json:"selection_ring"`
	NoticeDot       bool              `json:"notice_dot"`
	State           NodeState         `json:"state"`
	MarkSymbol      string            `json:"mark_symbol"`
	RunningArcPhase float64           `json:"running_arc_phase,omitempty"`
	Accessibility   AccessibilityItem `json:"accessibility"`
}

// RenderScene is the complete frame description for CanvasView.
type RenderScene struct {
	Scale          float64      `json:"scale"`
	NeedsAnimation bool         `json:"needs_animation"`
	Nodes          []RenderNode `json:"nodes"`
	Edges          []RenderEdge `json:"edges"`
}

// ShapeForKind maps a NodeKind to its geometric silhouette.
func ShapeForKind(kind NodeKind) ShapeKind {
	switch kind {
	case KindSession:
		return ShapeCircle
	case KindTool:
		return ShapeSquare
	case KindWatch:
		return ShapeDiamond
	case KindGroup:
		return ShapeHexagon
	default:
		return ShapeSquare
	}
}

// MarkForState returns the distinct non-color symbol/mark for a node state.
func MarkForState(state NodeState) string {
	switch state {
	case StatePending:
		return "pending_ring"
	case StateRunning:
		return "running_arc"
	case StateWatching:
		return "watching_eye"
	case StateNeedsAttention:
		return "attention_bang"
	case StateCompleted:
		return "completed_check"
	case StateError:
		return "error_cross"
	case StateStopped:
		return "stopped_square"
	default:
		return "pending_ring"
	}
}

// NeedsAnimation reports whether any visible node requires the 30 fps arc timer.
func NeedsAnimation(wl WorldLayout) bool {
	for _, nl := range wl.Nodes {
		if nl.Node.State == StateRunning {
			return true
		}
	}
	return false
}

// BuildScene constructs a scale-aware RenderScene from a WorldLayout.
func BuildScene(wl WorldLayout, selected map[string]bool, scale float64, phase float64) RenderScene {
	if scale <= 0 {
		scale = 1.0
	}
	scene := RenderScene{
		Scale:          scale,
		NeedsAnimation: NeedsAnimation(wl),
		Nodes:          make([]RenderNode, 0, len(wl.Nodes)),
		Edges:          make([]RenderEdge, 0, len(wl.Edges)),
	}

	for _, el := range wl.Edges {
		style := StyleSolidArrow
		alpha := 1.0
		arrow := true
		switch el.Edge.Kind {
		case EdgeContainment:
			style = StyleFaintDashed
			alpha = 0.25
			arrow = false
		case EdgeAttemptHistory:
			style = StyleDottedHistory
			alpha = 0.45
		}
		pts := make([]Point, len(el.Points))
		for i, p := range el.Points {
			pts[i] = Point{X: p.X * scale, Y: p.Y * scale}
		}
		scene.Edges = append(scene.Edges, RenderEdge{
			ID:        el.Edge.ID,
			Style:     style,
			Alpha:     alpha,
			From:      Point{X: el.From.X * scale, Y: el.From.Y * scale},
			To:        Point{X: el.To.X * scale, Y: el.To.Y * scale},
			Points:    pts,
			DrawArrow: arrow,
			Label:     el.Edge.Milestone,
			LabelPos:  Point{X: el.LabelPos.X * scale, Y: el.LabelPos.Y * scale},
		})
	}

	for _, nl := range wl.Nodes {
		bounds := Rect{
			X:      nl.Bounds.X * scale,
			Y:      nl.Bounds.Y * scale,
			Width:  nl.Bounds.Width * scale,
			Height: nl.Bounds.Height * scale,
		}
		actions := []string{"select", "inspect"}
		if nl.Node.Kind == KindSession {
			actions = append(actions, "open_session")
		}
		var arcPhase float64
		if nl.Node.State == StateRunning {
			arcPhase = math.Mod(phase, 2*math.Pi)
		}
		rn := RenderNode{
			ID:              nl.Node.ID,
			Shape:           ShapeForKind(nl.Node.Kind),
			Bounds:          bounds,
			ClippedTitle:    clipText(nl.Node.Title, 18),
			ClippedDetail:   clipText(nl.Node.Detail, 24),
			SelectionRing:   selected[nl.Node.ID],
			NoticeDot:       nl.Node.HasNotice || nl.Node.State == StateNeedsAttention,
			State:           nl.Node.State,
			MarkSymbol:      MarkForState(nl.Node.State),
			RunningArcPhase: arcPhase,
			Accessibility: AccessibilityItem{
				NodeID:  nl.Node.ID,
				Role:    "AXButton",
				Label:   nl.Node.Title,
				State:   string(nl.Node.State),
				Frame:   bounds,
				Actions: actions,
			},
		}
		scene.Nodes = append(scene.Nodes, rn)
	}
	return scene
}

// HitTestNode maps a screen/backing point to the topmost node at that coordinate.
func HitTestNode(wl WorldLayout, pt Point, pan Point, zoom, backingScale float64) (string, bool) {
	if zoom <= 0 {
		zoom = 1.0
	}
	if backingScale <= 0 {
		backingScale = 1.0
	}
	worldPt := Point{
		X: (pt.X/backingScale - pan.X) / zoom,
		Y: (pt.Y/backingScale - pan.Y) / zoom,
	}
	for id, nl := range wl.Nodes {
		if nl.Bounds.Contains(worldPt) {
			return id, true
		}
	}
	return "", false
}

func clipText(s string, maxRunes int) string {
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	if maxRunes <= 1 {
		return string(r[:maxRunes])
	}
	return string(r[:maxRunes-1]) + "…"
}
