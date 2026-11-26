<script lang="ts">
  import { onMount } from 'svelte'
  import type { Graph } from '../graph'
  import { hit, layout, shift, within, type Offsets } from './layout'
  import { draw, moving, type Band } from './render'
  import { banded, click, dirOf, near, nearest, roots } from './select'
  import { colors } from '../theme'
  import { fit, onWheel, pan, reveal, toLayout, zoomAt, type View } from './view'

  let { graph, selection = $bindable([]), attention = new Set(), onopen, onremove }: {
    graph: Graph
    selection?: string[] // the picked nodes
    attention?: Set<string>
    onopen?: (id: string) => void // a double click
    onremove?: (ids: string[]) => void // ⌘⌫ on the selection
  } = $props()
  const picked = $derived(new Set(selection))

  let canvas: HTMLCanvasElement
  let w = $state(0)
  let h = $state(0)
  let view = $state<View>({ x: -40, y: -40, scale: 1 })
  // the canvas fits everything until a person pans, zooms or drags: while the window takes its size
  // and the nodes come in at the start
  let touched = false
  // where a person moved nodes: kept by the app (canvas.json in the home), how the canvas looks, not state
  let offsets = $state<Offsets>({})
  let loaded = $state(false)
  onMount(() => {
    try { offsets = JSON.parse(localStorage.getItem('flows.canvas.positions') ?? '{}') ?? {} }
    catch { offsets = {} }
    loaded = true
  })
  // fitted again as the window takes its size and the nodes come in (an effect of its own: it does
  // not read the view it sets)
  $effect(() => {
    if (!touched && loaded && w > 0 && l.boxes.size > 0) view = fit(l, w, h)
  })
  function save() {
    const kept: Offsets = {}
    for (const [id, o] of Object.entries(offsets)) if (graph.nodes.has(id) && (o.x || o.y)) kept[id] = o
    offsets = kept
    try { localStorage.setItem('flows.canvas.positions', JSON.stringify(kept)) } catch { /* storage unavailable */ }
  }
  const base = $derived(layout(graph))
  const l = $derived(shift(base, graph, offsets))
  // while a node works its arc goes round: redraw about 30 times a second, and only then
  let now = $state(0)
  $effect(() => {
    if (!moving(graph)) return
    let id = 0, last = 0
    const step = (t: number) => {
      if (t - last > 33) {
        now = t
        last = t
      }
      id = requestAnimationFrame(step)
    }
    id = requestAnimationFrame(step)
    return () => cancelAnimationFrame(id)
  })

  $effect(() => {
    if (!canvas || w === 0) return
    const dpr = window.devicePixelRatio || 1
    // a new size reallocates the canvas: only when it changed, not on every frame of a spinner
    if (canvas.width !== Math.round(w * dpr) || canvas.height !== Math.round(h * dpr)) {
      canvas.width = Math.round(w * dpr)
      canvas.height = Math.round(h * dpr)
    }
    const ctx = canvas.getContext('2d')!
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
    draw(ctx, l, graph, view, w, h, colors, picked, attention, now, band)
  })

  onMount(() => {
    const ro = new ResizeObserver(([e]) => {
      w = e.contentRect.width
      h = e.contentRect.height
    })
    ro.observe(canvas.parentElement!)
    return () => ro.disconnect()
  })

  // A drag selects the nodes in its rectangle; with space held (or the middle button) it pans.
  // Two fingers on the trackpad pan too (wheel).
  let space = $state(false)
  let drag = $state<{
    pan: boolean; x: number; y: number; sx: number; sy: number; moved: boolean; additive: boolean
    move?: Offsets // the nodes a drag on one of them moves, and where they were
  } | undefined>()
  let band = $state<Band | undefined>()
  const at = (e: MouseEvent) => {
    const r = canvas.getBoundingClientRect()
    return { x: e.clientX - r.left, y: e.clientY - r.top }
  }
  function down(e: PointerEvent) {
    touched = true
    if (e.button !== 0 && e.button !== 1) return
    const p = at(e)
    const additive = e.shiftKey || e.metaKey
    const panning = space || e.button === 1
    drag = { pan: panning, x: e.clientX, y: e.clientY, sx: p.x, sy: p.y, moved: false, additive }
    const q = toLayout(view, p.x, p.y)
    const id = panning || additive ? undefined : hit(l, q.x, q.y)
    if (id) { // a drag on a node moves it, with the rest of the selection when it is in it
      const ids = roots(graph, selection.includes(id) ? selection : [id])
      drag.move = Object.fromEntries(ids.map((m) => [m, offsets[m] ?? l.own?.get(m) ?? { x: 0, y: 0 }])) // one that followed its maker starts from there
    }
    canvas.setPointerCapture(e.pointerId)
  }
  function move(e: PointerEvent) {
    if (!drag) return
    const dx = e.clientX - drag.x, dy = e.clientY - drag.y
    if (!drag.moved && Math.abs(dx) + Math.abs(dy) > 3) drag.moved = true
    if (!drag.moved) return
    if (drag.pan) {
      view = pan(view, dx, dy)
      drag.x = e.clientX
      drag.y = e.clientY
    } else if (drag.move) {
      const next = { ...offsets }
      for (const [id, o] of Object.entries(drag.move)) next[id] = { x: o.x + dx / view.scale, y: o.y + dy / view.scale }
      offsets = next
    } else {
      const p = at(e)
      band = { x0: drag.sx, y0: drag.sy, x1: p.x, y1: p.y }
    }
  }
  function up(e: PointerEvent) {
    if (drag?.move && drag.moved) {
      const ids = Object.keys(drag.move)
      if (!ids.every((id) => selection.includes(id))) selection = ids
      save()
    } else if (drag && !drag.pan) {
      if (band) {
        const a = toLayout(view, band.x0, band.y0), b = toLayout(view, band.x1, band.y1)
        selection = banded(selection, within(l, a.x, a.y, b.x, b.y), drag.additive)
      } else if (!drag.moved) {
        const p = toLayout(view, drag.sx, drag.sy)
        selection = click(selection, hit(l, p.x, p.y), drag.additive)
      }
    }
    drag = undefined
    band = undefined
  }
  function dblclick(e: MouseEvent) {
    const r = canvas.getBoundingClientRect()
    const p = toLayout(view, e.clientX - r.left, e.clientY - r.top)
    const id = hit(l, p.x, p.y)
    if (id) onopen?.(id)
  }
  function wheel(e: WheelEvent) {
    touched = true
    e.preventDefault()
    const r = canvas.getBoundingClientRect()
    view = onWheel(view, e, e.clientX - r.left, e.clientY - r.top)
  }
  // WebKit reports a trackpad pinch as gesture events (scale since the gesture began), not as wheels.
  type Gesture = UIEvent & { scale: number; clientX: number; clientY: number }
  let pinched = 1
  onMount(() => {
    const start = (e: Event) => {
      e.preventDefault()
      pinched = 1
    }
    const change = (e: Event) => {
      e.preventDefault()
      const g = e as Gesture
      const r = canvas.getBoundingClientRect()
      view = zoomAt(view, g.clientX - r.left, g.clientY - r.top, g.scale / pinched)
      pinched = g.scale
    }
    canvas.addEventListener('gesturestart', start)
    canvas.addEventListener('gesturechange', change)
    return () => {
      canvas.removeEventListener('gesturestart', start)
      canvas.removeEventListener('gesturechange', change)
    }
  })
  function key(e: KeyboardEvent) {
    const t = e.target as HTMLElement | null
    if (document.querySelector('dialog[open], [role="dialog"]') || e.defaultPrevented || t?.closest('input, textarea, select, dialog, [role="dialog"]')) return // typing elsewhere, the terminal
    if (e.key === 'f' && !e.metaKey && !e.ctrlKey) view = fit(l, w, h)
    if (e.key === ' ' && !e.metaKey && !t?.closest('button')) {
      e.preventDefault() // held: a drag pans
      space = true
    }
    if (e.key === 'Escape') selection = []
    const dir = !e.metaKey && !e.ctrlKey && !e.altKey && !e.shiftKey ? dirOf[e.key] : undefined
    if (dir && l.boxes.size) { // to the next node that way; from the middle of the view with nothing picked
      e.preventDefault()
      const from = selection.length ? selection[selection.length - 1] : undefined
      const mid = toLayout(view, w / 2, h / 2)
      const to = from && l.boxes.has(from) ? near(l, from, dir) : nearest(l, mid.x, mid.y)
      if (to) {
        selection = [to]
        touched = true
        view = reveal(view, l.boxes.get(to)!, w, h)
      }
    }
    if (e.key === 'r' && !e.metaKey && !e.ctrlKey) { // the picked nodes (or all) back where the layout puts them
      const next = { ...offsets }
      for (const id of selection.length ? selection : Object.keys(next)) delete next[id]
      offsets = next
      save()
    }
    if (e.key === 'a' && e.metaKey) {
      e.preventDefault()
      selection = [...graph.nodes.keys()]
    }
    if (e.key === 'Backspace' && e.metaKey && selection.length) {
      e.preventDefault()
      onremove?.(selection)
    }
  }
  function keyup(e: KeyboardEvent) {
    if (e.key === ' ') space = false
  }
</script>

<svelte:window onkeydown={key} onkeyup={keyup} onblur={() => (space = false)} />
<canvas
  bind:this={canvas}
  style:width="{w}px"
  style:height="{h}px"
  onpointerdown={down}
  onpointermove={move}
  onpointerup={up}
  onpointercancel={up}
  class:panning={space || drag?.pan}
  class:grabbing={drag?.pan && drag.moved}
  onwheel={wheel}
  ondblclick={dblclick}
></canvas>

<style>
  canvas {
    display: block;
    cursor: default;
    touch-action: none;
  }
  canvas.panning { cursor: grab; }
  canvas.grabbing { cursor: grabbing; }
</style>
