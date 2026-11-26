// The client's copy of a session's screen: the host's frames applied in order (internal/vt.Frame:
// a full frame replaces everything, otherwise only the rows it carries change).

export interface Cell {
  t?: string // text; empty is a blank
  f?: number // 0xRRGGBB with bit 24 set, or absent for the default colour
  b?: number
  a?: number // attribute bits
  w?: number // 0 narrow, 1 wide (first column), 2 spacer
}

export interface Frame {
  full?: boolean
  cols: number
  rows: number
  fg: number
  bg: number
  cursor: { x: number; y: number; visible: boolean; style: string }
  alt?: boolean
  title?: string
  paste?: boolean
  appkey?: boolean
  scrolled?: number // rows the view is back in the history
  mouse?: boolean // the program reads mouse reports
  mousesgr?: boolean
  lines?: { y: number; cells: Cell[] }[]
}

export const ATTR = { bold: 1, faint: 2, italic: 4, underline: 8, strike: 16, inverse: 32 }
export const WIDE = 1
export const SPACER = 2
const SET = 1 << 24

export interface Screen {
  cols: number
  rows: number
  fg: number
  bg: number
  cursor: Frame['cursor']
  title: string
  paste: boolean
  appkey: boolean
  alt: boolean
  scrolled: number
  mouse: boolean
  mousesgr: boolean
  lines: Cell[][]
}

export function blank(cols = 80, rows = 24): Screen {
  return {
    cols, rows, fg: 0xd8dbe2, bg: 0x111318, title: '', paste: false, appkey: false, alt: false, scrolled: 0, mouse: false, mousesgr: false,
    cursor: { x: 0, y: 0, visible: false, style: 'block' },
    lines: Array.from({ length: rows }, () => []),
  }
}

// applyFrame returns the screen after a frame. Rows outside the screen are ignored.
export function applyFrame(s: Screen, f: Frame): Screen {
  const sized = f.cols !== s.cols || f.rows !== s.rows
  const lines = f.full || sized ? Array.from({ length: f.rows }, (_, y) => (f.full ? [] : (s.lines[y] ?? []))) : [...s.lines]
  for (const l of f.lines ?? []) if (l.y >= 0 && l.y < f.rows) lines[l.y] = l.cells
  return {
    cols: f.cols, rows: f.rows, fg: f.fg, bg: f.bg, cursor: f.cursor, title: f.title ?? '',
    paste: !!f.paste, appkey: !!f.appkey, alt: !!f.alt, scrolled: f.scrolled ?? 0,
    mouse: !!f.mouse, mousesgr: !!f.mousesgr, lines,
  }
}

const hex = (c: number) => '#' + (c & 0xffffff).toString(16).padStart(6, '0')

// colors resolves a cell's foreground and background, inverse applied.
export function colors(s: Screen, c: Cell): { fg: string; bg: string | undefined } {
  let fg = c.f && c.f & SET ? c.f : s.fg
  let bg: number | undefined = c.b && c.b & SET ? c.b : undefined
  if ((c.a ?? 0) & ATTR.inverse) {
    const back = bg ?? s.bg
    bg = fg
    fg = back
  }
  return { fg: hex(fg), bg: bg === undefined ? undefined : hex(bg) }
}

export function rowText(cells: Cell[]): string {
  return cells.filter((c) => c.w !== SPACER).map((c) => c.t || ' ').join('').replace(/ +$/, '')
}

export function text(s: Screen): string {
  return s.lines.map(rowText).join('\n').replace(/\n+$/, '')
}
