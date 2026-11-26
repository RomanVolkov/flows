// A text selection on the screen grid, from one cell to another in reading order.
import { SPACER, type Screen } from './screen'

export interface Point {
  x: number
  y: number
}

export interface Selection {
  from: Point
  to: Point
}

const before = (a: Point, b: Point) => a.y < b.y || (a.y === b.y && a.x <= b.x)

export function ordered(s: Selection): Selection {
  return before(s.from, s.to) ? s : { from: s.to, to: s.from }
}

export function contains(s: Selection | undefined, x: number, y: number): boolean {
  if (!s) return false
  const { from, to } = ordered(s)
  return before(from, { x, y }) && before({ x, y }, to)
}

// selectedText is the selected cells as text: trailing blanks of each line dropped, a wide
// character once, lines joined with newlines.
export function selectedText(screen: Screen, s: Selection): string {
  const { from, to } = ordered(s)
  const out: string[] = []
  for (let y = from.y; y <= to.y && y < screen.rows; y++) {
    const row = screen.lines[y] ?? []
    const x0 = y === from.y ? from.x : 0
    const x1 = y === to.y ? to.x : screen.cols - 1
    let line = ''
    for (let x = x0; x <= x1; x++) {
      const c = row[x]
      if (c?.w === SPACER) continue
      line += c?.t || ' '
    }
    out.push(line.replace(/ +$/, ''))
  }
  return out.join('\n')
}
