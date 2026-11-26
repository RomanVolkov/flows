// The view maps layout space to the screen: screen = (layout - origin) * scale.
import type { Layout } from './layout'

export interface View {
  x: number
  y: number
  scale: number
}

export const MIN_SCALE = 0.2
export const MAX_SCALE = 3

export const toScreen = (v: View, x: number, y: number) => ({ x: (x - v.x) * v.scale, y: (y - v.y) * v.scale })
export const toLayout = (v: View, sx: number, sy: number) => ({ x: sx / v.scale + v.x, y: sy / v.scale + v.y })

// zoomAt scales by factor around a screen point, which stays where it is.
export function zoomAt(v: View, sx: number, sy: number, factor: number): View {
  const scale = Math.min(MAX_SCALE, Math.max(MIN_SCALE, v.scale * factor))
  const p = toLayout(v, sx, sy)
  return { scale, x: p.x - sx / scale, y: p.y - sy / scale }
}

// reveal moves the view the least so a layout rectangle is on a w × h screen, a margin from its edges.
export function reveal(v: View, r: { x: number; y: number; w: number; h: number }, w: number, h: number, margin = 40): View {
  const a = toScreen(v, r.x, r.y), b = toScreen(v, r.x + r.w, r.y + r.h)
  const dx = a.x < margin ? margin - a.x : b.x > w - margin ? Math.max(margin - a.x, w - margin - b.x) : 0
  const dy = a.y < margin ? margin - a.y : b.y > h - margin ? Math.max(margin - a.y, h - margin - b.y) : 0
  return dx || dy ? pan(v, dx, dy) : v
}

// pan moves the view by a screen distance.
export const pan = (v: View, dx: number, dy: number): View => ({ ...v, x: v.x - dx / v.scale, y: v.y - dy / v.scale })

export interface Wheel {
  deltaX: number
  deltaY: number
  deltaMode: number // 0 pixels, 1 lines, 2 pages
  ctrlKey: boolean // also set on a trackpad pinch (Chromium-style webviews)
  metaKey: boolean
}

// onWheel moves the view like a native Mac canvas: two fingers on the trackpad (or a wheel) pan,
// a pinch or cmd/ctrl + wheel zooms around the pointer at sx, sy.
export function onWheel(v: View, e: Wheel, sx: number, sy: number): View {
  const unit = e.deltaMode === 1 ? 16 : e.deltaMode === 2 ? 400 : 1
  if (e.ctrlKey || e.metaKey) return zoomAt(v, sx, sy, Math.exp(-e.deltaY * unit * 0.01))
  return pan(v, -e.deltaX * unit, -e.deltaY * unit)
}

// fit shows the whole layout in a w x h screen with a margin, never larger than 1:1.
export function fit(l: Layout, w: number, h: number, margin = 40): View {
  if (l.w === 0 || l.h === 0) return { x: -margin, y: -margin, scale: 1 }
  const scale = Math.max(MIN_SCALE, Math.min(1, (w - 2 * margin) / l.w, (h - 2 * margin) / l.h))
  return { scale, x: l.w / 2 - w / 2 / scale, y: l.h / 2 - h / 2 / scale }
}

// clip shortens text to fit width (measured by measure), with an ellipsis.
export function clip(text: string, width: number, measure: (s: string) => number): string {
  if (measure(text) <= width) return text
  let lo = 0, hi = text.length
  while (lo < hi) {
    const mid = Math.ceil((lo + hi) / 2)
    if (measure(text.slice(0, mid) + '…') <= width) lo = mid
    else hi = mid - 1
  }
  return text.slice(0, lo) + '…'
}
