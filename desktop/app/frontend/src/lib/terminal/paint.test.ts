import { describe, expect, test } from 'vitest'
import { cellAt, contentBounds, gridSize } from './paint'

describe('terminal geometry helpers', () => {
  test('contentBounds subtracts padding on both sides with a minimum size', () => {
    expect(contentBounds({ left: 100, top: 50, width: 800, height: 600 }, 9)).toEqual({
      x: 109,
      y: 59,
      width: 782,
      height: 582,
    })
    expect(contentBounds({ left: 0, top: 0, width: 12, height: 8 }, 9)).toEqual({
      x: 9,
      y: 9,
      width: 10,
      height: 10,
    })
  })

  test('gridSize and cellAt clamp cleanly', () => {
    const metrics = { w: 10, h: 20, font: 'IosevkaTerm Nerd Font Mono', size: 16 }
    expect(gridSize(metrics, 805, 499)).toEqual({ cols: 80, rows: 24 })
    expect(gridSize(metrics, 20, 10)).toEqual({ cols: 10, rows: 3 })
    expect(cellAt(metrics, 35, 45)).toEqual({ x: 3, y: 2 })
  })
})
