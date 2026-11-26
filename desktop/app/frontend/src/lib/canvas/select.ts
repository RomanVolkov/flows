// The selection: the nodes picked on the canvas, in the order they were picked.
import type { Graph } from '../graph'
import { anchor, type Layout } from './layout'

export type Dir = 'left' | 'right' | 'up' | 'down'

// near is the node next to `from` in a direction: the closest one ahead, a node straight ahead
// before one off to the side. Undefined when nothing is that way.
export function near(l: Layout, from: string, dir: Dir): string | undefined {
  const b = l.boxes.get(from)
  if (!b) return undefined
  const p = anchor(b)
  const [ax, ay] = { left: [-1, 0], right: [1, 0], up: [0, -1], down: [0, 1] }[dir]
  let best: string | undefined
  let bestScore = Infinity
  for (const c of l.boxes.values()) {
    if (c.id === from) continue
    const q = anchor(c)
    const dx = q.x - p.x, dy = q.y - p.y
    const along = dx * ax + dy * ay
    const side = Math.abs(dx * ay - dy * ax)
    if (along <= 1) continue
    const score = along + 2 * side + (side > along ? 1e6 : 0) // outside the 45° cone: only when nothing is inside
    if (score < bestScore) (best = c.id), (bestScore = score)
  }
  return best
}

// nearest is the node closest to a point (the middle of the view, to start moving from).
export function nearest(l: Layout, x: number, y: number): string | undefined {
  let best: string | undefined
  let bestD = Infinity
  for (const b of l.boxes.values()) {
    const q = anchor(b)
    const d = (q.x - x) ** 2 + (q.y - y) ** 2
    if (d < bestD) (best = b.id), (bestD = d)
  }
  return best
}

// arrows and hjkl
export const dirOf: Record<string, Dir> = {
  ArrowLeft: 'left', h: 'left', ArrowRight: 'right', l: 'right', ArrowUp: 'up', k: 'up', ArrowDown: 'down', j: 'down',
}

// click picks the node clicked (undefined: the empty canvas). With shift or ⌘ (additive) it is
// added, or taken out when it was in; without, it is all there is.
export function click(sel: string[], id: string | undefined, additive: boolean): string[] {
  if (!additive) return id ? [id] : []
  if (!id) return sel
  return sel.includes(id) ? sel.filter((x) => x !== id) : [...sel, id]
}

// banded picks the nodes a dragged rectangle took in, after the selection when additive.
export function banded(sel: string[], ids: string[], additive: boolean): string[] {
  return additive ? [...sel, ...ids.filter((id) => !sel.includes(id))] : ids
}

// roots are the selected nodes that no other selected node is above: what an action on the
// selection acts on, so a group and its child are not stopped, removed or counted twice.
export function roots(g: Graph, ids: string[]): string[] {
  const picked = new Set(ids)
  return ids.filter((id) => {
    for (let p = g.nodes.get(id)?.parent; p; p = g.nodes.get(p)?.parent) if (picked.has(p)) return false
    return true
  })
}
