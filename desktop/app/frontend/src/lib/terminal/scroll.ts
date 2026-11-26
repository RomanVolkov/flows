// Wheel and trackpad scrolling of a terminal. A trackpad sends many small pixel deltas: they add up
// to whole rows, and the rest is kept for the next event.

export interface Wheel {
  deltaY: number
  deltaMode: number // 0 pixels, 1 lines, 2 pages
}

// wheelRows turns a wheel event into rows (negative: up, back in the history) and the pixels left.
export function wheelRows(rest: number, e: Wheel, rowHeight: number, pageRows: number): { rows: number; rest: number } {
  const px = e.deltaMode === 1 ? e.deltaY * rowHeight : e.deltaMode === 2 ? e.deltaY * pageRows * rowHeight : e.deltaY
  const total = rest + px
  const rows = Math.trunc(total / rowHeight)
  return { rows, rest: total - rows * rowHeight }
}

// altPages: a full-screen program that does not read the mouse has no history here, and arrow keys
// would move its cursor or its prompt history (claude's); the wheel sends it PageUp and PageDown
// instead, one per half screen of rows added up, the rest kept for the next event.
export function altPages(rest: number, rows: number, pageRows: number): { keys: string; rest: number } {
  const half = Math.max(1, Math.floor(pageRows / 2))
  const total = rest + rows
  const pages = Math.trunc(total / half)
  return { keys: (pages < 0 ? '\x1b[5~' : '\x1b[6~').repeat(Math.abs(pages)), rest: total - pages * half }
}

// mouseWheel: a program that asked for mouse reports (claude does) scrolls itself; the wheel goes to
// it as buttons 64 (up) and 65 (down) at the cell x, y (from 0), one report per row.
export function mouseWheel(rows: number, x: number, y: number, sgr: boolean): string {
  const btn = rows < 0 ? 64 : 65
  const one = sgr
    ? `\x1b[<${btn};${x + 1};${y + 1}M`
    : `\x1b[M${String.fromCharCode(32 + btn, 32 + Math.min(x + 1, 223), 32 + Math.min(y + 1, 223))}`
  return one.repeat(Math.abs(rows))
}
