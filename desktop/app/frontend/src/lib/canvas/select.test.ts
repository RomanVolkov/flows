import { describe, expect, it } from 'vitest'
import { fromSnapshot, type Node } from '../graph'
import { banded, click, near, nearest, roots } from './select'
import type { Box, Layout } from './layout'

const node = (id: string, parent?: string): Node => ({ id, kind: parent === undefined ? 'group' : 'step', title: id, state: 'done', parent, created: '', updated: '' })

describe('selection', () => {
  it('picks the clicked node, or adds and takes out with shift', () => {
    expect(click(['a', 'b'], 'c', false)).toEqual(['c'])
    expect(click(['a', 'b'], undefined, false)).toEqual([])
    expect(click(['a'], 'b', true)).toEqual(['a', 'b'])
    expect(click(['a', 'b'], 'a', true)).toEqual(['b'])
    expect(click(['a'], undefined, true)).toEqual(['a'])
  })

  it('picks what a rectangle took in, after the selection with shift', () => {
    expect(banded(['a'], ['b', 'c'], false)).toEqual(['b', 'c'])
    expect(banded(['a', 'b'], ['b', 'c'], true)).toEqual(['a', 'b', 'c'])
  })

  it('acts on the selected nodes no selected node is above', () => {
    const g = fromSnapshot({ seq: 1, nodes: [node('g'), node('h', 'g'), node('x', 'h'), node('y')], edges: [], sessions: [] })
    expect(roots(g, ['x', 'g', 'y'])).toEqual(['g', 'y'])
    expect(roots(g, ['x', 'h'])).toEqual(['h'])
    expect(roots(g, ['x', 'missing'])).toEqual(['x', 'missing'])
  })
})

// a grid:   a  b
//           c     d   (d lower right, off to the side of b)
//           g         (a group) and e below it
const box = (id: string, x: number, y: number, w = 40, h = 40): Box => ({ id, x, y, w, h })
const grid: Layout = {
  boxes: new Map([box('a', 0, 0), box('b', 200, 0), box('c', 0, 150), box('d', 400, 160), box('g', 0, 300), box('e', 20, 440)].map((b) => [b.id, b])),
  lines: [], links: [], w: 500, h: 500,
}

describe('moving with the keys', () => {
  it('goes to the next node that way, straight ahead first', () => {
    expect(near(grid, 'a', 'right')).toBe('b')
    expect(near(grid, 'a', 'down')).toBe('c')
    expect(near(grid, 'b', 'left')).toBe('a')
    expect(near(grid, 'c', 'up')).toBe('a')
    expect(near(grid, 'c', 'right')).toBe('d')
  })

  it('reaches a group like any node, and what is below it', () => {
    expect(near(grid, 'c', 'down')).toBe('g')
    expect(near(grid, 'g', 'down')).toBe('e')
    expect(near(grid, 'e', 'up')).toBe('g')
  })

  it('stays put at an edge or for an unknown node', () => {
    expect(near(grid, 'a', 'up')).toBeUndefined()
    expect(near(grid, 'a', 'left')).toBeUndefined()
    expect(near(grid, 'gone', 'up')).toBeUndefined()
  })

  it('starts from the node nearest a point', () => {
    expect(nearest(grid, 390, 170)).toBe('d')
    expect(nearest({ ...grid, boxes: new Map() }, 0, 0)).toBeUndefined()
  })
})
