// Painting a screen on a canvas: one cell per column, wide characters over two, the cursor on top.
import { ATTR, colors, SPACER, WIDE, type Screen } from './screen'

export interface Metrics {
  w: number // cell width
  h: number // cell height
  font: string // family and size, without weight or style
  size: number
}

export function measure(ctx: CanvasRenderingContext2D, family: string, size: number): Metrics {
  ctx.font = `${size}px ${family}`
  const w = Math.ceil(ctx.measureText('M').width * 100) / 100
  return { w, h: Math.ceil(size * 1.3), font: family, size }
}

// cellAt is the cell under a pixel; gridSize the cols and rows that fit a box.
export const cellAt = (m: Metrics, px: number, py: number) => ({ x: Math.floor(px / m.w), y: Math.floor(py / m.h) })
export const gridSize = (m: Metrics, width: number, height: number) => ({
  cols: Math.max(10, Math.floor(width / m.w)),
  rows: Math.max(3, Math.floor(height / m.h)),
})

// Look is what the theme decides beyond the screen's own colours.
export interface Look {
  cursor: string
  selection: string
  selectionInk: string
}

export function paint(ctx: CanvasRenderingContext2D, s: Screen, m: Metrics, focused: boolean, selected?: (x: number, y: number) => boolean,
  look: Look = { cursor: '#d8dbe2', selection: '#3b5a8a', selectionInk: '' }) {
  // cell edges on device pixels: a cell is a fraction of a pixel wide, and two backgrounds that
  // meet inside a pixel let the screen's own show through as a thin vertical line
  const dpr = ctx.getTransform().a || 1
  const edge = (x: number) => Math.round(x * m.w * dpr) / dpr
  ctx.fillStyle = '#' + s.bg.toString(16).padStart(6, '0')
  ctx.fillRect(0, 0, s.cols * m.w, s.rows * m.h)
  ctx.textBaseline = 'middle'
  let font = ''
  for (let y = 0; y < s.rows; y++) {
    const row = s.lines[y] ?? []
    for (let x = 0; x < row.length && x < s.cols; x++) {
      const c = row[x]
      if (c.w === SPACER) continue
      const span = c.w === WIDE ? 2 : 1
      const a = c.a ?? 0
      let { fg, bg } = colors(s, c)
      if (selected?.(x, y)) {
        bg = look.selection
        if (look.selectionInk) fg = look.selectionInk
      }
      if (bg) {
        ctx.fillStyle = bg
        ctx.fillRect(edge(x), y * m.h, edge(x + span) - edge(x), m.h)
      }
      if (!c.t) continue
      const f = `${a & ATTR.italic ? 'italic ' : ''}${a & ATTR.bold ? 'bold ' : ''}${m.size}px ${m.font}`
      if (f !== font) ctx.font = font = f // parsing a font is dear: a screen has thousands of cells
      ctx.globalAlpha = a & ATTR.faint ? 0.6 : 1
      ctx.fillStyle = fg
      ctx.fillText(c.t, x * m.w, y * m.h + m.h / 2)
      if (a & ATTR.underline) ctx.fillRect(edge(x), y * m.h + m.h - 2, edge(x + span) - edge(x), 1)
      if (a & ATTR.strike) ctx.fillRect(edge(x), y * m.h + m.h / 2, edge(x + span) - edge(x), 1)
      ctx.globalAlpha = 1
    }
  }
  const cur = s.cursor
  if (!cur.visible) return
  ctx.fillStyle = ctx.strokeStyle = look.cursor
  const cx = cur.x * m.w, cy = cur.y * m.h
  if (cur.style === 'bar') ctx.fillRect(cx, cy, 2, m.h)
  else if (cur.style === 'underline') ctx.fillRect(cx, cy + m.h - 2, m.w, 2)
  else if (cur.style === 'hollow' || !focused) ctx.strokeRect(cx + 0.5, cy + 0.5, m.w - 1, m.h - 1)
  else {
    ctx.globalAlpha = 0.7
    ctx.fillRect(cx, cy, m.w, m.h)
    ctx.globalAlpha = 1
  }
}
