// Drawing the layout on a 2D canvas: arrows for edges (fainter from a group to what it holds), a
// shape per node (circle: session, rounded square: step, diamond: watch, hexagon: group) coloured by its state, with a mark of the state
// inside, its title and label below.
import type { Graph, State } from '../graph'
import { anchor, R, type Layout } from './layout'
import { clip, toScreen, type View } from './view'

// Colors is what the canvas paints with, from the theme (lib/theme.ts).
export interface Colors {
  states: Record<State, string>
  ink: string // titles
  dim: string // labels, edges
  selected: string // the selected nodes' rings, and the selection band
  attention: string // the dot of a node with a notice not seen yet
  bg: string
  font: string // the mono stack
}

// Mark is how a node shows its state besides its colour: a glyph inside, a dashed outline while it
// waits, an arc going round while it works.
export interface Mark {
  glyph: string
  dashed: boolean
  spin: boolean
}

export function mark(s: State): Mark {
  switch (s) {
    case 'pending': return { glyph: '…', dashed: true, spin: false }
    case 'starting': return { glyph: '', dashed: true, spin: true }
    case 'running': return { glyph: '', dashed: false, spin: true }
    case 'watching': return { glyph: '◎', dashed: false, spin: false }
    case 'blocked': return { glyph: '!', dashed: false, spin: false }
    case 'done': return { glyph: '✓', dashed: false, spin: false }
    case 'failed': return { glyph: '✕', dashed: false, spin: false }
    case 'stopped': return { glyph: '■', dashed: false, spin: false }
    default: return { glyph: '?', dashed: false, spin: false }
  }
}

// moving says whether the canvas has to be redrawn over time (a node works).
export const moving = (g: Graph) => [...g.nodes.values()].some((n) => mark(n.state).spin)

// draw paints everything; attention holds the nodes with an unseen notice, t (ms) turns the arcs.
// Band is the selection rectangle being dragged, in screen space.
export interface Band {
  x0: number
  y0: number
  x1: number
  y1: number
}

export function draw(ctx: CanvasRenderingContext2D, l: Layout, g: Graph, v: View, w: number, h: number, c: Colors,
  selected: Set<string> = new Set(), attention: Set<string> = new Set(), t = 0, band?: Band) {
  ctx.clearRect(0, 0, w, h)
  ctx.save()
  ctx.lineWidth = 1
  // edges
  ctx.strokeStyle = c.dim
  ctx.fillStyle = c.dim
  for (const e of l.lines) {
    ctx.globalAlpha = e.part ? 0.55 : 1
    const p = toScreen(v, e.x1, e.y1) // the layout ends the line on both borders
    const q = toScreen(v, e.x2, e.y2)
    ctx.beginPath()
    ctx.moveTo(p.x, p.y)
    ctx.lineTo(q.x, q.y)
    ctx.stroke()
    const ang = Math.atan2(q.y - p.y, q.x - p.x)
    const s = 7 * v.scale
    ctx.beginPath()
    ctx.moveTo(q.x, q.y)
    ctx.lineTo(q.x - s * Math.cos(ang - 0.4), q.y - s * Math.sin(ang - 0.4))
    ctx.lineTo(q.x - s * Math.cos(ang + 0.4), q.y - s * Math.sin(ang + 0.4))
    ctx.closePath()
    ctx.fill()
  }
  ctx.globalAlpha = 1
  // nodes
  for (const b of l.boxes.values()) {
    const n = g.nodes.get(b.id)
    if (!n) continue
    const at = anchor(b)
    const p = toScreen(v, at.x, at.y)
    const r = R * v.scale
    shape(ctx, n.kind, p.x, p.y, r)
    const col = c.states[n.state] ?? c.dim
    const m = mark(n.state)
    ctx.globalAlpha = 0.22
    ctx.fillStyle = col
    ctx.fill()
    ctx.globalAlpha = 1
    ctx.lineWidth = 2 * v.scale
    ctx.strokeStyle = selected.has(b.id) ? c.selected : col
    if (m.dashed) ctx.setLineDash([4 * v.scale, 3 * v.scale])
    ctx.stroke()
    ctx.setLineDash([])
    if (selected.has(b.id)) { // a second ring outside: selected stays readable over any state
      shape(ctx, n.kind, p.x, p.y, r + 5 * v.scale)
      ctx.strokeStyle = c.selected
      ctx.lineWidth = 1.5 * v.scale
      ctx.stroke()
    }
    if (m.spin) { // a quarter of the outline runs round it
      const ring = r + 2.5 * v.scale
      const len = perimeter(n.kind, ring)
      shape(ctx, n.kind, p.x, p.y, ring)
      ctx.setLineDash([len / 4, len])
      ctx.lineDashOffset = -(((t / 700) % (Math.PI * 2)) / (Math.PI * 2)) * len
      ctx.strokeStyle = col
      ctx.lineWidth = 3 * v.scale
      ctx.lineCap = 'round'
      ctx.stroke()
      ctx.lineCap = 'butt'
      ctx.setLineDash([])
      ctx.lineDashOffset = 0
    }
    ctx.lineWidth = 1
    if (m.glyph) {
      ctx.textAlign = 'center'
      ctx.textBaseline = 'middle'
      ctx.font = `700 ${r * 0.9}px ${c.font}`
      ctx.fillStyle = col
      ctx.fillText(m.glyph, p.x, p.y + r * 0.04)
    }
    if (attention.has(b.id)) {
      ctx.beginPath()
      ctx.arc(p.x + r * 0.72, p.y - r * 0.72, 5 * v.scale, 0, Math.PI * 2)
      ctx.fillStyle = c.attention
      ctx.fill()
      ctx.strokeStyle = c.bg
      ctx.lineWidth = 2 * v.scale
      ctx.stroke()
      ctx.lineWidth = 1
    }
    ctx.textAlign = 'center'
    ctx.textBaseline = 'top'
    const width = b.w * v.scale
    ctx.font = `500 ${12 * v.scale}px ${c.font}`
    ctx.fillStyle = c.ink
    ctx.fillText(fitted(ctx, n.title, width), p.x, p.y + r + 6 * v.scale)
    if (n.label) {
      ctx.font = `${11 * v.scale}px ${c.font}`
      ctx.fillStyle = c.dim
      ctx.fillText(fitted(ctx, n.label, width), p.x, p.y + r + 22 * v.scale)
    }
    ctx.textAlign = 'start'
  }
  if (band) {
    const x = Math.min(band.x0, band.x1), y = Math.min(band.y0, band.y1)
    ctx.globalAlpha = 0.12
    ctx.fillStyle = c.selected
    ctx.fillRect(x, y, Math.abs(band.x1 - band.x0), Math.abs(band.y1 - band.y0))
    ctx.globalAlpha = 1
    ctx.strokeStyle = c.selected
    ctx.lineWidth = 1
    ctx.strokeRect(x + 0.5, y + 0.5, Math.abs(band.x1 - band.x0), Math.abs(band.y1 - band.y0))
  }
  ctx.restore()
}

// fitted is text clipped to width in the context's font, remembered: a spinner redraws the canvas
// 30 times a second, and measuring every title again each time is most of the work.
const clipped = new Map<string, string>()
function fitted(ctx: CanvasRenderingContext2D, text: string, width: number): string {
  const key = `${ctx.font}|${Math.round(width)}|${text}`
  let out = clipped.get(key)
  if (out === undefined) {
    if (clipped.size > 5000) clipped.clear()
    out = clip(text, Math.round(width), (s) => ctx.measureText(s).width)
    clipped.set(key, out)
  }
  return out
}

// the outline of a node of this kind around (x, y): a rounded square, a diamond, a hexagon or a circle
function shape(ctx: CanvasRenderingContext2D, kind: string, x: number, y: number, r: number) {
  if (kind === 'step') return roundRect(ctx, x - r, y - r, 2 * r, 2 * r, r / 3)
  ctx.beginPath()
  if (kind === 'group') {
    for (let i = 0; i < 6; i++) {
      const a = (Math.PI / 3) * i
      ctx[i ? 'lineTo' : 'moveTo'](x + r * Math.cos(a), y + r * Math.sin(a))
    }
    ctx.closePath()
  } else if (kind === 'watch') {
    ctx.moveTo(x, y - r)
    ctx.lineTo(x + r, y)
    ctx.lineTo(x, y + r)
    ctx.lineTo(x - r, y)
    ctx.closePath()
  } else ctx.arc(x, y, r, 0, Math.PI * 2)
}

// the length of that outline
function perimeter(kind: string, r: number) {
  if (kind === 'step') return 8 * r - (8 * r) / 3 + (2 * Math.PI * r) / 3 // straight sides less the corners, plus the corner arcs
  if (kind === 'watch') return 4 * Math.SQRT2 * r
  if (kind === 'group') return 6 * r
  return 2 * Math.PI * r
}

function roundRect(ctx: CanvasRenderingContext2D, x: number, y: number, w: number, h: number, r: number) {
  ctx.beginPath()
  ctx.moveTo(x + r, y)
  ctx.arcTo(x + w, y, x + w, y + h, r)
  ctx.arcTo(x + w, y + h, x, y + h, r)
  ctx.arcTo(x, y + h, x, y, r)
  ctx.arcTo(x, y, x + w, y, r)
  ctx.closePath()
}
