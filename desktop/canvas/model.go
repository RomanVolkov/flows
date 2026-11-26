// Package canvas implements the top-down world model, graph layout,
// viewport camera, selection/dragging, and Core Graphics rendering spec.
package canvas

// Point is a 2D coordinate in world or screen space.
type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Rect is an axis-aligned rectangle.
type Rect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// Contains reports whether p lies within r.
func (r Rect) Contains(p Point) bool {
	return p.X >= r.X && p.X <= r.X+r.Width && p.Y >= r.Y && p.Y <= r.Y+r.Height
}

// Intersects reports whether r and other overlap.
func (r Rect) Intersects(other Rect) bool {
	return r.X <= other.X+other.Width && r.X+r.Width >= other.X &&
		r.Y <= other.Y+other.Height && r.Y+r.Height >= other.Y
}

// Center returns the center point of r.
func (r Rect) Center() Point {
	return Point{X: r.X + r.Width/2, Y: r.Y + r.Height/2}
}

// NodeKind determines the visual shape of a node.
type NodeKind string

const (
	KindSession NodeKind = "session" // circle
	KindTool    NodeKind = "tool"    // square
	KindWatch   NodeKind = "watch"   // diamond
	KindGroup   NodeKind = "group"   // hexagon
)

// NodeState determines status mark and accessibility label.
type NodeState string

const (
	StatePending        NodeState = "pending"
	StateRunning        NodeState = "running"
	StateWatching       NodeState = "watching"
	StateNeedsAttention NodeState = "needs_attention"
	StateCompleted      NodeState = "completed"
	StateError          NodeState = "error"
	StateStopped        NodeState = "stopped"
)

// EdgeKind distinguishes dependency, containment, and historical attempt links.
type EdgeKind string

const (
	EdgeDependency     EdgeKind = "dependency"
	EdgeContainment    EdgeKind = "containment"
	EdgeAttemptHistory EdgeKind = "attempt_history"
)

// Node is a materialized item on the shared execution canvas.
type Node struct {
	ID            string    `json:"id"`
	RunID         string    `json:"run_id"`
	ParentID      string    `json:"parent_id,omitempty"`
	Kind          NodeKind  `json:"kind"`
	Title         string    `json:"title"`
	Detail        string    `json:"detail,omitempty"`
	State         NodeState `json:"state"`
	HasNotice     bool      `json:"has_notice,omitempty"`
	AttemptNumber int       `json:"attempt_number,omitempty"`
}

// Edge connects two nodes in the world graph.
type Edge struct {
	ID        string   `json:"id"`
	RunID     string   `json:"run_id"`
	FromID    string   `json:"from_id"`
	ToID      string   `json:"to_id"`
	Kind      EdgeKind `json:"kind"`
	Milestone string   `json:"milestone,omitempty"`
}

// Graph is the input set of nodes and edges for layout.
type Graph struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

// LayoutConfig controls world-space box sizes, spacing, and shelf packing.
type LayoutConfig struct {
	BoxWidth       float64
	BoxHeight      float64
	CornerRadius   float64
	LabelClearance float64
	ShelfWidth     float64
	ComponentGap   float64
	HGap           float64
	VGap           float64
}

// DefaultLayoutConfig returns the standard top-down canvas geometry.
func DefaultLayoutConfig() LayoutConfig {
	return LayoutConfig{
		BoxWidth:       150,
		BoxHeight:      92,
		CornerRadius:   22,
		LabelClearance: 24,
		ShelfWidth:     1800,
		ComponentGap:   48,
		HGap:           44,
		VGap:           56,
	}
}

// NodeLayout stores the computed world bounds, rank, and label clearance for a node.
type NodeLayout struct {
	Node        Node    `json:"node"`
	Bounds      Rect    `json:"bounds"`
	LabelBounds Rect    `json:"label_bounds"`
	Center      Point   `json:"center"`
	Rank        int     `json:"rank"`
	Column      int     `json:"column"`
	ComponentID int     `json:"component_id"`
	Radius      float64 `json:"radius"`
}

// EdgeLayout stores rim-to-rim routed endpoints and horizontal label coordinates.
type EdgeLayout struct {
	Edge            Edge    `json:"edge"`
	From            Point   `json:"from"`
	To              Point   `json:"to"`
	Points          []Point `json:"points"`
	LabelPos        Point   `json:"label_pos"`
	HorizontalLabel bool    `json:"horizontal_label"`
}

// ComponentBounds bounds a weak component including its nodes, labels, and edges.
type ComponentBounds struct {
	ID      int      `json:"id"`
	Bounds  Rect     `json:"bounds"`
	NodeIDs []string `json:"node_ids"`
}

// WorldLayout is the complete laid-out graph for the pan-and-zoom canvas.
type WorldLayout struct {
	Nodes       map[string]NodeLayout `json:"nodes"`
	Edges       []EdgeLayout          `json:"edges"`
	Components  []ComponentBounds     `json:"components"`
	WorldBounds Rect                  `json:"world_bounds"`
}
