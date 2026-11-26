// Layout of the node graph. Every node is a node, a group too: a group is linked to what it holds
// (each child no sibling leads to), and the whole graph is laid out in layers from left to right
// along the edges; separate graphs and single nodes are packed in rows. Pure functions: the canvas
// draws what they return.
import type { Edge, Graph, Node } from '../graph'

export const R = 22 // radius of a node
export const LEAF_W = 150 // room for a node with its label under it
export const LEAF_H = 92
const GAP_X = 60
const GAP_Y = 24
const ROW_GAP = 48
export const MAX_ROW = 1800 // packing width

export interface Box {
  id: string
  x: number // top left, absolute
  y: number
  w: number
  h: number
}

export interface Line {
  from: string
  to: string
  reason?: string
  part?: boolean // a group to what it holds
  x1: number
  y1: number
  x2: number
  y2: number
}

export interface Layout {
  boxes: Map<string, Box>
  lines: Line[]
  w: number
  h: number
  // the edges layout followed: the graph's, and a group's to what it holds
  links: (Edge & { part?: boolean })[]
  // where shift moved each node by its own (or, never moved itself, by the node that made it)
  own?: Map<string, { x: number; y: number }>
}

interface Sized {
  id: string
  w: number
  h: number
  place: (x: number, y: number, out: Map<string, Box>) => void
}

// center of a node's shape: it sits at the top middle of its box, its label below
export function anchor(b: Box): { x: number; y: number } {
  return { x: b.x + b.w / 2, y: b.y + R + 6 }
}

// the outline a line ends at: a node's shape, with the room of its ring
const RIM = R + 6
function rim(b: Box, kind: string | undefined, toward: { x: number; y: number }): { x: number; y: number } {
  const c = anchor(b)
  const dx = toward.x - c.x, dy = toward.y - c.y
  const len = Math.hypot(dx, dy) || 1
  const ux = dx / len, uy = dy / len
  let t: number
  if (kind === 'step') t = RIM / Math.max(Math.abs(ux), Math.abs(uy))
  else if (kind === 'watch') t = RIM / (Math.abs(ux) + Math.abs(uy))
  else t = RIM
  return { x: c.x + ux * t, y: c.y + uy * t }
}

// line draws an edge from border to border
function line(e: Edge & { part?: boolean }, a: Box, b: Box, g: Graph): Line {
  const p = rim(a, g.nodes.get(a.id)?.kind, anchor(b))
  const q = rim(b, g.nodes.get(b.id)?.kind, anchor(a))
  return { from: e.from, to: e.to, reason: e.reason, part: e.part, x1: p.x, y1: p.y, x2: q.x, y2: q.y }
}

const parentOf = (g: Graph, id: string) => {
  const p = g.nodes.get(id)?.parent
  return p && g.nodes.has(p) ? p : ''
}

// parts are the links from a group to what it holds: to each child that neither the group nor a
// sibling leads to (the first step of a chain; the steps after it hang off that one)
function parts(g: Graph): (Edge & { part: true })[] {
  const led = new Set<string>()
  for (const e of g.edges) {
    const p = parentOf(g, e.to)
    if (p && (e.from === p || parentOf(g, e.from) === p)) led.add(e.to)
  }
  const out: (Edge & { part: true })[] = []
  for (const n of g.nodes.values()) {
    const p = parentOf(g, n.id)
    if (p && !led.has(n.id)) out.push({ from: p, to: n.id, part: true })
  }
  return out
}

export function layout(g: Graph): Layout {
  const order = (a: Node, b: Node) => a.created.localeCompare(b.created) || a.id.localeCompare(b.id)
  const nodes = [...g.nodes.values()].sort(order)
  const edges = [...g.edges.filter((e) => g.nodes.has(e.from) && g.nodes.has(e.to)), ...parts(g)]
  const items: Sized[] = nodes.map((n) => ({
    id: n.id, w: LEAF_W, h: LEAF_H,
    place: (x, y, out) => out.set(n.id, { id: n.id, x, y, w: LEAF_W, h: LEAF_H }),
  }))
  const top = arrange(items, edges, MAX_ROW)
  const boxes = new Map<string, Box>()
  top.place(0, 0, boxes)
  const lines: Line[] = []
  for (const e of edges) {
    const a = boxes.get(e.from)
    const b = boxes.get(e.to)
    if (a && b && e.from !== e.to) lines.push(line(e, a, b, g))
  }
  return { boxes, lines, w: top.w, h: top.h, links: edges }
}

// arrange lays out the nodes: connected ones in layers, then the graphs in rows no wider than maxRow.
function arrange(items: Sized[], edges: Edge[], maxRow: number): { w: number; h: number; place: Sized['place'] } {
  const ids = new Set(items.map((i) => i.id))
  const local = edges.filter((e) => ids.has(e.from) && ids.has(e.to) && e.from !== e.to)
  const comps = components(items, local)
  const parts = comps.map((c) => layered(c, local))
  // shelf packing: left to right, a new row when the next does not fit
  const pos: { x: number; y: number }[] = []
  let x = 0, y = 0, rowH = 0, w = 0
  for (const p of parts) {
    if (x > 0 && x + p.w > maxRow) {
      y += rowH + ROW_GAP
      x = 0
      rowH = 0
    }
    pos.push({ x, y })
    x += p.w + GAP_X
    rowH = Math.max(rowH, p.h)
    w = Math.max(w, x - GAP_X)
  }
  const h = parts.length ? y + rowH : 0
  return { w, h, place: (ox, oy, out) => parts.forEach((p, i) => p.place(ox + pos[i].x, oy + pos[i].y, out)) }
}

function components(items: Sized[], edges: Edge[]): Sized[][] {
  const parent = new Map(items.map((i) => [i.id, i.id]))
  const find = (a: string): string => {
    while (parent.get(a) !== a) a = parent.get(a)!
    return a
  }
  for (const e of edges) parent.set(find(e.from), find(e.to))
  const groups = new Map<string, Sized[]>()
  for (const i of items) {
    const r = find(i.id)
    if (!groups.has(r)) groups.set(r, [])
    groups.get(r)!.push(i)
  }
  return [...groups.values()]
}

// layered places one connected component: layer = longest path from a source (a cycle, which the
// store refuses, is broken by visit order); within a layer, nodes follow the mean row of their
// predecessors.
function layered(items: Sized[], edges: Edge[]): { w: number; h: number; place: Sized['place'] } {
  const byId = new Map(items.map((i) => [i.id, i]))
  const preds = new Map<string, string[]>(items.map((i) => [i.id, []]))
  for (const e of edges) if (byId.has(e.from) && byId.has(e.to)) preds.get(e.to)!.push(e.from)
  const layer = new Map<string, number>()
  const visiting = new Set<string>()
  const depth = (id: string): number => {
    if (layer.has(id)) return layer.get(id)!
    if (visiting.has(id)) return 0
    visiting.add(id)
    const d = Math.max(-1, ...preds.get(id)!.map(depth)) + 1
    visiting.delete(id)
    layer.set(id, d)
    return d
  }
  items.forEach((i) => depth(i.id))
  const layers: Sized[][] = []
  for (const i of items) (layers[layer.get(i.id)!] ??= []).push(i)
  const row = new Map<string, number>()
  for (const l of layers) {
    if (!l) continue
    const key = (i: Sized) => {
      const ps = preds.get(i.id)!.filter((p) => row.has(p))
      return ps.length ? ps.reduce((s, p) => s + row.get(p)!, 0) / ps.length : Infinity
    }
    l.sort((a, b) => key(a) - key(b))
    l.forEach((i, k) => row.set(i.id, k))
  }
  const colW = layers.map((l) => Math.max(0, ...(l ?? []).map((i) => i.w)))
  const colH = layers.map((l) => (l ?? []).reduce((s, i) => s + i.h, 0) + Math.max(0, (l?.length ?? 0) - 1) * GAP_Y)
  const w = colW.reduce((s, c) => s + c, 0) + Math.max(0, layers.length - 1) * GAP_X
  const h = Math.max(0, ...colH)
  return {
    w, h,
    place: (ox, oy, out) => {
      let x = ox
      layers.forEach((l, k) => {
        let y = oy + (h - colH[k]) / 2 // each column centred
        for (const i of l ?? []) {
          i.place(x + (colW[k] - i.w) / 2, y, out)
          y += i.h + GAP_Y
        }
        x += colW[k] + GAP_X
      })
    },
  }
}

// hit returns the node at a point in layout space.
export function hit(l: Layout, x: number, y: number): string | undefined {
  for (const b of l.boxes.values()) if (x >= b.x && y >= b.y && x <= b.x + b.w && y <= b.y + b.h) return b.id
  return undefined
}

// within returns the nodes whose mark is inside a rectangle in layout space (corners in any order).
export function within(l: Layout, x0: number, y0: number, x1: number, y1: number): string[] {
  const [left, right] = x0 < x1 ? [x0, x1] : [x1, x0]
  const [top, bottom] = y0 < y1 ? [y0, y1] : [y1, y0]
  const out: string[] = []
  for (const b of l.boxes.values()) {
    const a = anchor(b)
    if (a.x >= left && a.x <= right && a.y >= top && a.y <= bottom) out.push(b.id)
  }
  return out
}

// Offsets are where a person moved nodes, in layout units from where the layout put them.
export type Offsets = Record<string, { x: number; y: number }>

// shift moves the boxes by the offsets: a node by its own and its groups', so a group takes what
// it holds along; a node never moved itself goes with the node that made it (the first link into
// it), so it stays near it. What the layout placed is then pushed down clear of what was moved.
// The lines follow.
export function shift(l: Layout, g: Graph, offsets: Offsets): Layout {
  if (Object.keys(offsets).length === 0) return l
  const maker = new Map<string, string>() // a group's link to what it holds is not making it
  for (const e of l.links) if (!e.part && !maker.has(e.to)) maker.set(e.to, e.from)
  const own = new Map<string, { x: number; y: number }>()
  const seen = new Set<string>()
  // own is how far a node moved by itself: its offset, or as far as its maker moved beyond the
  // groups they share
  const ownOf = (id: string): { x: number; y: number } => {
    if (own.has(id)) return own.get(id)!
    let o = offsets[id]
    const m = maker.get(id)
    if (!o && m && !seen.has(id)) {
      seen.add(id)
      const a = total(m), b = total(parentOf(g, id))
      o = { x: a.x - b.x, y: a.y - b.y }
    }
    o ??= { x: 0, y: 0 }
    own.set(id, o)
    return o
  }
  const total = (id: string): { x: number; y: number } => {
    let x = 0, y = 0
    for (let n = id; n; n = parentOf(g, n)) {
      const o = ownOf(n)
      ;(x += o.x), (y += o.y)
    }
    return { x, y }
  }
  const boxes = new Map<string, Box>()
  for (const b of l.boxes.values()) {
    const o = total(b.id)
    boxes.set(b.id, { ...b, x: b.x + o.x, y: b.y + o.y })
  }
  const kids = new Map<string, string[]>()
  for (const id of boxes.keys()) {
    const p = parentOf(g, id)
    if (!kids.has(p)) kids.set(p, [])
    kids.get(p)!.push(id)
  }
  const moveTree = (id: string, dy: number) => {
    boxes.get(id)!.y += dy
    for (const c of kids.get(id) ?? []) moveTree(c, dy)
  }
  const depth = (id: string) => {
    let d = 0
    for (let n = parentOf(g, id); n; n = parentOf(g, n)) d++
    return d
  }
  // siblings, the deepest first: what the layout placed goes below what was moved
  const containers = [...kids.keys()].sort((a, b) => (b ? depth(b) + 1 : 0) - (a ? depth(a) + 1 : 0))
  for (const cid of containers) {
    const ids = kids.get(cid)!
    const created = (id: string) => g.nodes.get(id)?.created ?? ''
    const placed = ids.filter((id) => offsets[id]).map((id) => boxes.get(id)!)
    for (const id of ids.filter((id) => !offsets[id]).sort((a, b) => created(a).localeCompare(created(b)) || a.localeCompare(b))) {
      const b = boxes.get(id)!
      for (let i = 0; i < 1000; i++) {
        const hit = placed.find((o) => b.x < o.x + o.w + GAP_Y && o.x < b.x + b.w + GAP_Y && b.y < o.y + o.h + GAP_Y && o.y < b.y + b.h + GAP_Y)
        if (!hit) break
        moveTree(id, hit.y + hit.h + GAP_Y - b.y)
      }
      placed.push(b)
    }
  }
  const lines = l.lines.map((ln) => line(ln, boxes.get(ln.from)!, boxes.get(ln.to)!, g))
  let w = 0, h = 0
  for (const b of boxes.values()) (w = Math.max(w, b.x + b.w)), (h = Math.max(h, b.y + b.h))
  return { boxes, lines, w, h, links: l.links, own }
}
