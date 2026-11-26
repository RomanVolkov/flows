import { describe, expect, it } from 'vitest'
import { fromSnapshot, type Edge, type Node } from '../graph'
import { anchor, hit, layout, MAX_ROW, R, shift, within, type Box } from './layout'

let t = 0
const node = (id: string, extra: Partial<Node> = {}): Node => ({
  id, kind: 'step', title: id, state: 'pending', created: `2026-11-23T10:00:${String(t++ % 60).padStart(2, '0')}Z`, updated: '', ...extra,
})
const graph = (nodes: Node[], edges: Edge[] = []) => fromSnapshot({ seq: 1, nodes, edges, sessions: [] })
const overlap = (a: Box, b: Box) => a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h
const center = (b: Box) => ({ x: b.x + b.w / 2, y: b.y + b.h / 2 })

describe('layout', () => {
  it('puts a diamond in three layers, the middle two side by side', () => {
    const l = layout(graph([node('a'), node('b'), node('c'), node('d')], [
      { from: 'a', to: 'b' }, { from: 'a', to: 'c' }, { from: 'b', to: 'd' }, { from: 'c', to: 'd' },
    ]))
    const [a, b, c, d] = ['a', 'b', 'c', 'd'].map((id) => l.boxes.get(id)!)
    expect(a.x).toBeLessThan(b.x)
    expect(b.x).toBe(c.x)
    expect(c.x).toBeLessThan(d.x)
    expect(b.y).not.toBe(c.y)
    expect(overlap(b, c)).toBe(false)
    expect(l.lines).toHaveLength(4)
  })

  it('lays a chain out in a line', () => {
    const l = layout(graph([node('a'), node('b'), node('c')], [{ from: 'a', to: 'b' }, { from: 'b', to: 'c' }]))
    const ys = ['a', 'b', 'c'].map((id) => l.boxes.get(id)!.y)
    expect(new Set(ys).size).toBe(1)
  })

  it('packs separate graphs and single nodes without overlap, within the row width', () => {
    const nodes = Array.from({ length: 30 }, (_, i) => node('n' + i))
    const l = layout(graph(nodes, [{ from: 'n0', to: 'n1' }]))
    const boxes = [...l.boxes.values()]
    for (let i = 0; i < boxes.length; i++)
      for (let j = i + 1; j < boxes.length; j++) expect(overlap(boxes[i], boxes[j])).toBe(false)
    expect(l.w).toBeLessThanOrEqual(MAX_ROW)
    expect(l.h).toBeGreaterThan(boxes[0].h) // more than one row
  })

  it('links a group to what it holds: the first of a chain, not the steps after it', () => {
    const g = graph([
      node('run', { kind: 'group' }), node('plan', { parent: 'run' }), node('code', { parent: 'run' }), node('solo', { parent: 'run' }), node('x'),
    ], [{ from: 'plan', to: 'code' }])
    const l = layout(g)
    expect(l.lines.filter((ln) => ln.part).map((ln) => `${ln.from}>${ln.to}`).sort()).toEqual(['run>plan', 'run>solo'])
    const [run, plan, code] = ['run', 'plan', 'code'].map((id) => l.boxes.get(id)!)
    expect(run.x).toBeLessThan(plan.x)
    expect(plan.x).toBeLessThan(code.x)
    const boxes = [...l.boxes.values()]
    for (let i = 0; i < boxes.length; i++)
      for (let j = i + 1; j < boxes.length; j++) expect(overlap(boxes[i], boxes[j])).toBe(false)
  })

  it('links nested groups down to their steps, and a group led to by a sibling only from it', () => {
    const l = layout(graph([
      node('run', { kind: 'group' }), node('a', { kind: 'group', parent: 'run' }), node('b', { kind: 'group', parent: 'run' }),
      node('a1', { parent: 'a' }), node('b1', { parent: 'b' }),
    ], [{ from: 'a', to: 'b' }]))
    expect(l.lines.map((ln) => `${ln.from}>${ln.to}`).sort()).toEqual(['a>a1', 'a>b', 'b>b1', 'run>a'])
  })

  it('skips an edge to a missing node', () => {
    const l = layout(graph([node('a')], [{ from: 'a', to: 'ghost' }]))
    expect(l.lines).toHaveLength(0)
    expect(l.boxes.size).toBe(1)
  })

  it('hits the node at a point', () => {
    const l = layout(graph([node('run', { kind: 'group' }), node('api', { parent: 'run' })]))
    const api = center(l.boxes.get('api')!)
    expect(hit(l, api.x, api.y)).toBe('api')
    const run = l.boxes.get('run')!
    expect(hit(l, run.x + 3, run.y + 3)).toBe('run')
    expect(hit(l, -50, -50)).toBeUndefined()
  })

  it('takes in the nodes whose mark is inside a rectangle', () => {
    const l = layout(graph([node('g', { kind: 'group' }), node('a', { parent: 'g' }), node('b', { parent: 'g' }), node('c')]))
    const [g, a] = [l.boxes.get('g')!, l.boxes.get('a')!]
    const pa = anchor(a)
    expect(within(l, pa.x + 1, pa.y + 1, pa.x - 1, pa.y - 1)).toEqual(['a']) // corners in any order
    expect(within(l, g.x - 1, g.y - 1, g.x + g.w + 1, g.y + g.h + 1)).toEqual(['g'])
    expect(within(l, -1e6, -1e6, 1e6, 1e6).sort()).toEqual(['a', 'b', 'c', 'g'])
    expect(within(l, -1e6, -1e6, -1e6 + 1, -1e6 + 1)).toEqual([])
  })

  it('moves a node, and a group with what it holds', () => {
    const g = graph([node('g', { kind: 'group' }), node('a', { parent: 'g' }), node('b', { parent: 'g' }), node('c')], [{ from: 'a', to: 'c' }])
    const l = layout(g)
    const at = (m: ReturnType<typeof layout>, id: string) => m.boxes.get(id)!
    expect(shift(l, g, {})).toBe(l)
    const moved = shift(l, g, { g: { x: 100, y: 10 }, a: { x: 0, y: 500 } })
    expect(at(moved, 'b').x).toBe(at(l, 'b').x + 100) // with its group
    expect(at(moved, 'a').y).toBe(at(l, 'a').y + 510) // its group's and its own
    expect(at(moved, 'c').x).toBe(at(l, 'c').x + 100) // not in the group, but made from in it: it goes along
    const f = at(moved, 'g'), a = at(moved, 'a')
    expect(f.x).toBe(at(l, 'g').x + 100)
    const line = moved.lines.find((ln) => ln.from === 'a')!
    const from = Math.hypot(line.x1 - anchor(a).x, line.y1 - anchor(a).y) // a step: a square's border
    expect(from).toBeGreaterThanOrEqual(R + 6 - 1e-6)
    expect(from).toBeLessThanOrEqual((R + 6) * Math.SQRT2 + 1e-6)
    expect(at(l, 'a').y).not.toBe(a.y) // the layout itself is not changed
    expect(moved.own?.get('c')).toEqual({ x: 100, y: 510 }) // as far as a went: a drag of c starts from there
  })

  it('ends lines on the rings of the nodes', () => {
    const l = layout(graph([node('g', { kind: 'group' }), node('a', { kind: 'session' })], [{ from: 'a', to: 'g' }]))
    const [g, a] = [l.boxes.get('g')!, l.boxes.get('a')!]
    const ln = l.lines[0]
    expect(Math.hypot(ln.x1 - anchor(a).x, ln.y1 - anchor(a).y)).toBeCloseTo(R + 6)
    expect(Math.hypot(ln.x2 - anchor(g).x, ln.y2 - anchor(g).y)).toBeCloseTo(R + 6)
  })

  it('puts what a node in a group made in the layer after it', () => {
    const nodes = [node('job', { kind: 'group' }), node('find', { parent: 'job' })]
    for (let i = 0; i < 12; i++) nodes.push(node('x' + i))
    nodes.push(node('run', { kind: 'group' }), node('work', { parent: 'run' }))
    const l = layout(graph(nodes, [{ from: 'find', to: 'run' }]))
    const [find, run] = [l.boxes.get('find')!, l.boxes.get('run')!]
    expect(run.x).toBeGreaterThan(find.x + find.w) // the next layer
    expect(run.x - (find.x + find.w)).toBeLessThan(100)
  })

  it('keeps what the layout placed clear of a node moved onto it', () => {
    const g = graph([node('a'), node('b'), node('c', { kind: 'group' }), node('d', { parent: 'c' })])
    const l = layout(g)
    const [a, c] = [l.boxes.get('a')!, l.boxes.get('c')!]
    const moved = shift(l, g, { a: { x: c.x - a.x, y: c.y - a.y } }) // a dropped where c is
    const [ma, mc, md] = ['a', 'c', 'd'].map((id) => moved.boxes.get(id)!)
    expect(ma.x).toBe(c.x)
    expect(overlap(ma, mc)).toBe(false)
    expect(md.y - mc.y).toBe(l.boxes.get('d')!.y - c.y) // what is in c went with it
    for (const x of moved.boxes.values()) for (const y of moved.boxes.values())
      if (x !== y && x.id !== 'd' && y.id !== 'd') expect(overlap(x, y)).toBe(false)
  })
})
