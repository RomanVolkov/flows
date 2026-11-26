import { describe, expect, it } from 'vitest'
import { clip, fit, MAX_SCALE, onWheel, pan, reveal, toLayout, toScreen, zoomAt } from './view'

describe('view', () => {
  it('maps both ways', () => {
    const v = { x: 100, y: 50, scale: 2 }
    expect(toScreen(v, 110, 60)).toEqual({ x: 20, y: 20 })
    expect(toLayout(v, 20, 20)).toEqual({ x: 110, y: 60 })
  })

  it('zooms around the cursor and keeps the scale in bounds', () => {
    const v = { x: 0, y: 0, scale: 1 }
    const z = zoomAt(v, 200, 100, 2)
    expect(toLayout(z, 200, 100)).toEqual(toLayout(v, 200, 100))
    expect(z.scale).toBe(2)
    expect(zoomAt(z, 0, 0, 100).scale).toBe(MAX_SCALE)
  })

  it('pans with two fingers and zooms with a pinch', () => {
    const v = { x: 0, y: 0, scale: 2 }
    const swipe = { deltaX: 20, deltaY: -10, deltaMode: 0, ctrlKey: false, metaKey: false }
    expect(onWheel(v, swipe, 50, 50)).toEqual({ x: 10, y: -5, scale: 2 })
    expect(onWheel(v, { ...swipe, deltaX: 0, deltaY: 1, deltaMode: 1 }, 0, 0)).toEqual({ x: 0, y: 8, scale: 2 })
    const pinch = onWheel(v, { ...swipe, deltaY: -10, ctrlKey: true }, 50, 50)
    expect(pinch.scale).toBeGreaterThan(2)
    expect(toLayout(pinch, 50, 50).x).toBeCloseTo(toLayout(v, 50, 50).x)
    expect(onWheel(v, { ...swipe, deltaY: 10, metaKey: true }, 0, 0).scale).toBeLessThan(2)
  })

  it('pans by screen pixels', () => {
    expect(pan({ x: 0, y: 0, scale: 2 }, 20, -10)).toEqual({ x: -10, y: 5, scale: 2 })
  })

  it('fits a layout and centres it', () => {
    const v = fit({ boxes: new Map(), lines: [], links: [], w: 2000, h: 1000 }, 1000, 600)
    expect(v.scale).toBeCloseTo(0.46)
    const c = toScreen(v, 1000, 500)
    expect(c.x).toBeCloseTo(500)
    expect(c.y).toBeCloseTo(300)
    expect(fit({ boxes: new Map(), lines: [], links: [], w: 100, h: 50 }, 1000, 600).scale).toBe(1)
  })

  it('moves the view the least to show a rectangle', () => {
    const v = { x: 0, y: 0, scale: 1 }
    expect(reveal(v, { x: 100, y: 100, w: 40, h: 40 }, 1000, 600)).toBe(v)
    expect(reveal(v, { x: 1000, y: 100, w: 40, h: 40 }, 1000, 600)).toEqual({ x: 80, y: 0, scale: 1 })
    expect(reveal(v, { x: -100, y: -100, w: 40, h: 40 }, 1000, 600)).toEqual({ x: -140, y: -140, scale: 1 })
  })

  it('clips text with an ellipsis', () => {
    const m = (s: string) => s.length * 10
    expect(clip('short', 100, m)).toBe('short')
    expect(clip('a much longer title', 100, m)).toBe('a much lo…')
    expect(clip('abc', 5, m)).toBe('…')
  })
})
