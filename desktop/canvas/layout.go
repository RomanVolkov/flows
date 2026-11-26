package canvas

import (
	"math"
	"slices"
)

// Layout computes weak components, group-entry links, longest-path ranks,
// predecessor-ordered centered rows, and shelf-packed component bounds.
// Saved positions (if provided) are preserved for existing nodes across updates.
func Layout(g Graph, cfg LayoutConfig, saved map[string]Point) WorldLayout {
	if cfg.BoxWidth <= 0 {
		cfg = DefaultLayoutConfig()
	}

	out := WorldLayout{
		Nodes: make(map[string]NodeLayout, len(g.Nodes)),
	}
	if len(g.Nodes) == 0 {
		return out
	}

	byID := make(map[string]Node, len(g.Nodes))
	nodeIDs := make([]string, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		byID[n.ID] = n
		nodeIDs = append(nodeIDs, n.ID)
	}
	slices.Sort(nodeIDs)

	// Build undirected adjacency for weak components and directed edges for ranking.
	undir := make(map[string][]string, len(g.Nodes))
	preds := make(map[string][]string, len(g.Nodes))
	succs := make(map[string][]string, len(g.Nodes))

	addDirected := func(from, to string) {
		if from == "" || to == "" || from == to {
			return
		}
		if _, ok := byID[from]; !ok {
			return
		}
		if _, ok := byID[to]; !ok {
			return
		}
		succs[from] = append(succs[from], to)
		preds[to] = append(preds[to], from)
		undir[from] = append(undir[from], to)
		undir[to] = append(undir[to], from)
	}

	// Containment links act as group-entry links unless an explicit edge overrides.
	for _, n := range g.Nodes {
		if n.ParentID != "" {
			addDirected(n.ParentID, n.ID)
		}
	}
	for _, e := range g.Edges {
		addDirected(e.FromID, e.ToID)
	}

	// Weak connected components.
	visited := make(map[string]bool, len(g.Nodes))
	var components [][]string
	for _, id := range nodeIDs {
		if visited[id] {
			continue
		}
		var comp []string
		queue := []string{id}
		visited[id] = true
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			comp = append(comp, cur)
			for _, nb := range undir[cur] {
				if !visited[nb] {
					visited[nb] = true
					queue = append(queue, nb)
				}
			}
		}
		slices.Sort(comp)
		components = append(components, comp)
	}

	rowStep := cfg.BoxHeight + cfg.LabelClearance + cfg.VGap
	colStep := cfg.BoxWidth + cfg.HGap

	type compPlacement struct {
		id     int
		nodes  []string
		local  map[string]NodeLayout
		width  float64
		height float64
	}
	placements := make([]compPlacement, 0, len(components))

	for compIdx, comp := range components {
		// Longest-path ranks within component.
		ranks := make(map[string]int, len(comp))
		memo := make(map[string]int, len(comp))
		visiting := make(map[string]bool, len(comp))
		var computeRank func(id string) int
		computeRank = func(id string) int {
			if r, ok := memo[id]; ok {
				return r
			}
			if visiting[id] {
				return 0
			}
			visiting[id] = true
			best := 0
			for _, p := range preds[id] {
				r := computeRank(p) + 1
				if r > best {
					best = r
				}
			}
			visiting[id] = false
			memo[id] = best
			return best
		}
		maxRank := 0
		byRank := make(map[int][]string)
		for _, id := range comp {
			r := computeRank(id)
			ranks[id] = r
			if r > maxRank {
				maxRank = r
			}
			byRank[r] = append(byRank[r], id)
		}

		// Order rank 0 deterministically; order subsequent ranks by average predecessor column.
		colOf := make(map[string]int, len(comp))
		maxRowWidth := cfg.BoxWidth
		for r := 0; r <= maxRank; r++ {
			row := byRank[r]
			if r == 0 {
				slices.Sort(row)
			} else {
				slices.SortFunc(row, func(a, b string) int {
					ba := barycenter(preds[a], colOf)
					bb := barycenter(preds[b], colOf)
					if ba < bb {
						return -1
					}
					if ba > bb {
						return 1
					}
					if a < b {
						return -1
					}
					if a > b {
						return 1
					}
					return 0
				})
			}
			for c, id := range row {
				colOf[id] = c
			}
			w := float64(len(row))*cfg.BoxWidth + float64(max(0, len(row)-1))*cfg.HGap
			if w > maxRowWidth {
				maxRowWidth = w
			}
		}

		local := make(map[string]NodeLayout, len(comp))
		for r := 0; r <= maxRank; r++ {
			row := byRank[r]
			rowWidth := float64(len(row))*cfg.BoxWidth + float64(max(0, len(row)-1))*cfg.HGap
			startX := (maxRowWidth - rowWidth) / 2
			y := float64(r) * rowStep
			for c, id := range row {
				x := startX + float64(c)*colStep
				bounds := Rect{X: x, Y: y, Width: cfg.BoxWidth, Height: cfg.BoxHeight}
				labelBounds := Rect{X: x, Y: y + cfg.BoxHeight, Width: cfg.BoxWidth, Height: cfg.LabelClearance}
				local[id] = NodeLayout{
					Node:        byID[id],
					Bounds:      bounds,
					LabelBounds: labelBounds,
					Center:      bounds.Center(),
					Rank:        r,
					Column:      c,
					ComponentID: compIdx,
					Radius:      cfg.CornerRadius,
				}
			}
		}

		compHeight := float64(maxRank+1)*(cfg.BoxHeight+cfg.LabelClearance) + float64(max(0, maxRank))*cfg.VGap
		placements = append(placements, compPlacement{
			id:     compIdx,
			nodes:  comp,
			local:  local,
			width:  maxRowWidth,
			height: compHeight,
		})
	}

	// Shelf-pack components left-to-right within cfg.ShelfWidth.
	curX := 0.0
	curY := 0.0
	shelfHeight := 0.0

	for _, cp := range placements {
		if curX > 0 && curX+cp.width > cfg.ShelfWidth {
			curX = 0
			curY += shelfHeight + cfg.ComponentGap
			shelfHeight = 0
		}

		minX := math.Inf(1)
		minY := math.Inf(1)
		maxX := math.Inf(-1)
		maxY := math.Inf(-1)

		for _, id := range cp.nodes {
			nl := cp.local[id]
			if pos, ok := saved[id]; ok {
				nl.Bounds.X = pos.X
				nl.Bounds.Y = pos.Y
			} else {
				nl.Bounds.X += curX
				nl.Bounds.Y += curY
			}
			nl.LabelBounds = Rect{
				X:      nl.Bounds.X,
				Y:      nl.Bounds.Y + nl.Bounds.Height,
				Width:  nl.Bounds.Width,
				Height: cfg.LabelClearance,
			}
			nl.Center = nl.Bounds.Center()
			out.Nodes[id] = nl

			minX = math.Min(minX, nl.Bounds.X)
			minY = math.Min(minY, nl.Bounds.Y)
			maxX = math.Max(maxX, nl.Bounds.X+nl.Bounds.Width)
			maxY = math.Max(maxY, nl.LabelBounds.Y+nl.LabelBounds.Height)
		}

		out.Components = append(out.Components, ComponentBounds{
			ID:      cp.id,
			Bounds:  Rect{X: minX, Y: minY, Width: maxX - minX, Height: maxY - minY},
			NodeIDs: cp.nodes,
		})

		curX += cp.width + cfg.ComponentGap
		if cp.height > shelfHeight {
			shelfHeight = cp.height
		}
	}

	// Route rim-to-rim edges and expand component/world bounds if needed.
	for _, e := range g.Edges {
		fromNL, okFrom := out.Nodes[e.FromID]
		toNL, okTo := out.Nodes[e.ToID]
		if !okFrom || !okTo {
			continue
		}
		fromPt := Point{X: fromNL.Center.X, Y: fromNL.Bounds.Y + fromNL.Bounds.Height}
		toPt := Point{X: toNL.Center.X, Y: toNL.Bounds.Y}
		midY := (fromPt.Y + toPt.Y) / 2
		el := EdgeLayout{
			Edge:            e,
			From:            fromPt,
			To:              toPt,
			Points:          []Point{fromPt, {X: fromPt.X, Y: midY}, {X: toPt.X, Y: midY}, toPt},
			LabelPos:        Point{X: (fromPt.X + toPt.X) / 2, Y: midY},
			HorizontalLabel: true,
		}
		out.Edges = append(out.Edges, el)
	}

	// Compute overall world bounds across all components.
	if len(out.Components) > 0 {
		wMinX, wMinY := out.Components[0].Bounds.X, out.Components[0].Bounds.Y
		wMaxX := wMinX + out.Components[0].Bounds.Width
		wMaxY := wMinY + out.Components[0].Bounds.Height
		for _, cb := range out.Components[1:] {
			wMinX = math.Min(wMinX, cb.Bounds.X)
			wMinY = math.Min(wMinY, cb.Bounds.Y)
			wMaxX = math.Max(wMaxX, cb.Bounds.X+cb.Bounds.Width)
			wMaxY = math.Max(wMaxY, cb.Bounds.Y+cb.Bounds.Height)
		}
		out.WorldBounds = Rect{X: wMinX, Y: wMinY, Width: wMaxX - wMinX, Height: wMaxY - wMinY}
	}

	return out
}

func barycenter(preds []string, colOf map[string]int) float64 {
	if len(preds) == 0 {
		return 0
	}
	sum := 0
	count := 0
	for _, p := range preds {
		if c, ok := colOf[p]; ok {
			sum += c
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return float64(sum) / float64(count)
}
