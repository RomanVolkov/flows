<script lang="ts">
  import { onMount } from 'svelte'
  import { encodeKey, encodePaste } from './keys'
  import { cellAt, gridSize, measure, paint, type Metrics } from './paint'
  import { applyFrame, blank, type Screen } from './screen'
  import { altPages, mouseWheel, wheelRows } from './scroll'
  import { contains, selectedText, type Selection } from './selection'
  import { monoStack, theme } from '../theme'

  let { sessionId, title, agent = '', onclose }: { sessionId: string; title: string; agent?: string; onclose: () => void } = $props()

  let box: HTMLDivElement
  let canvas: HTMLCanvasElement
  let input: HTMLTextAreaElement
  let screen = $state<Screen>(blank())
  let output = $state('') // a pipe session sends raw output, no screen
  let status = $state('connecting…')
  let metrics: Metrics | undefined
  let selection = $state<Selection | undefined>()
  let composing = false
  let sent = { cols: 0, rows: 0 }
  let api: any
  let terminalId = ''
  let owner = false
  let hasScreen = false

  function redraw() {
    if (!metrics || !canvas) return
    const dpr = window.devicePixelRatio || 1
    const cw = Math.round(screen.cols * metrics.w * dpr), ch = Math.round(screen.rows * metrics.h * dpr)
    if (canvas.width !== cw || canvas.height !== ch) {
      canvas.width = cw
      canvas.height = ch
    }
    canvas.style.width = `${screen.cols * metrics.w}px`
    canvas.style.height = `${screen.rows * metrics.h}px`
    const ctx = canvas.getContext('2d')!
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
    paint(ctx, screen, metrics, document.activeElement === input, (x, y) => contains(selection, x, y),
      { cursor: $theme.cursor, selection: $theme.selection_background, selectionInk: $theme.selection_foreground })
  }
  $effect(() => {
    screen
    selection
    $theme.background_opacity
    $theme.cursor
    $theme.selection_background
    $theme.selection_foreground
    schedule()
  })
  // frames come faster than the screen refreshes: paint once per animation frame
  let pending = false
  function schedule() {
    if (pending) return
    pending = true
    requestAnimationFrame(() => {
      pending = false
      redraw()
    })
  }

  function fitSize() {
    if (!metrics) return
    const r = box.getBoundingClientRect()
    const pad = 2 * ($theme.padding + 4)
    const { cols, rows } = gridSize(metrics, r.width - pad, r.height - pad)
    if (owner && terminalId && (cols !== sent.cols || rows !== sent.rows)) {
      sent = { cols, rows }
      api.ResizeTerminal(terminalId, cols, rows).catch((e: unknown) => (status = String(e)))
    }
  }

  onMount(() => {
    metrics = measure(canvas.getContext('2d')!, monoStack($theme), $theme.font_size)
    api = window.go.main.App
    let stopped = false
    async function connect() {
      try {
        terminalId = await api.Attach(sessionId)
        if (stopped) { await api.Detach(terminalId); return }
        while (!stopped) {
          const frame = JSON.parse(await api.ReadTerminal(terminalId)) as { kind: number; payload: string }
          const bytes = Uint8Array.from(atob(frame.payload ?? ''), (c) => c.charCodeAt(0))
          if (frame.kind === 7) {
            hasScreen = true
            output = ''
            screen = applyFrame(screen, JSON.parse(new TextDecoder().decode(bytes)))
            status = ''
          } else if (frame.kind === 1 && !hasScreen) {
            output = (output + new TextDecoder().decode(bytes)).slice(-200_000)
            status = ''
          } else if (frame.kind === 5) {
            owner = JSON.parse(new TextDecoder().decode(bytes)).owner_id === terminalId
            status = owner ? '' : `Viewing · input owned by another terminal · ${agent}`
            if (owner) fitSize()
          } else if (frame.kind === 6) {
            if (!stopped) onclose()
            break
          }
        }
      } catch (e) {
        if (!stopped) status = String(e)
      } finally {
        owner = false
      }
    }
    connect()
    const ro = new ResizeObserver(fitSize)
    ro.observe(box)
    input.focus()
    return () => {
      stopped = true
      ro.disconnect()
      if (terminalId) api.Detach(terminalId).catch(() => {})
    }
  })

  function send(data: string) {
    selection = undefined
    if (owner && terminalId) api.WriteTerminal(terminalId, data).catch((e: unknown) => (status = String(e)))
  }

  let wheelRest = 0
  let pageRest = 0
  function wheel(e: WheelEvent) {
    if (!metrics || output) return
    const r = wheelRows(wheelRest, e, metrics.h, screen.rows)
    wheelRest = r.rest
    if (r.rows === 0) return
    // claude on its full screen always reads the mouse (SGR); an older host does not say so
    const mouse = screen.mouse || (screen.alt && agent === 'claude')
    if (mouse) {
      // the program scrolls itself (claude's transcript): it gets the wheel where the pointer is
      const c = canvas.getBoundingClientRect()
      const at = cellAt(metrics, e.clientX - c.left, e.clientY - c.top)
      const x = Math.max(0, Math.min(screen.cols - 1, at.x)), y = Math.max(0, Math.min(screen.rows - 1, at.y))
      if (owner) api.WriteTerminal(terminalId, mouseWheel(r.rows, x, y, screen.mousesgr || !screen.mouse)).catch(() => {})
    } else if (screen.alt) {
      const p = altPages(pageRest, r.rows, screen.rows)
      pageRest = p.rest
      if (p.keys) send(p.keys)
    } else scroll(r.rows)
  }
  function scroll(rows: number) {
    selection = undefined // it was drawn on the rows that move away
    if (terminalId) api.ScrollScreen(terminalId, rows).catch((e: unknown) => (status = String(e)))
  }

  function keydown(e: KeyboardEvent) {
    if (composing || e.isComposing) return
    if (e.metaKey && e.key === 'w') { // Esc belongs to the program (claude cancels with it)
      e.preventDefault()
      onclose()
      return
    }
    if (e.shiftKey && (e.key === 'PageUp' || e.key === 'PageDown') && !screen.alt) {
      e.preventDefault()
      scroll((e.key === 'PageUp' ? -1 : 1) * Math.max(1, screen.rows - 1))
      return
    }
    if (e.metaKey && e.key === 'c' && selection) {
      e.preventDefault()
      copy(selectedText(screen, selection))
      return
    }
    const data = encodeKey(e, screen.appkey)
    if (data !== undefined) {
      e.preventDefault()
      send(data)
    }
  }
  // copy goes through the app: WKWebView refuses navigator.clipboard outside a secure origin
  function copy(text: string) {
    api.Clipboard(text).then(
      (ok: boolean) => { if (!ok) status = 'could not copy' },
      (e: unknown) => (status = `could not copy: ${e}`),
    )
  }
  function paste(e: ClipboardEvent) {
    e.preventDefault()
    const t = e.clipboardData?.getData('text/plain')
    if (t) send(encodePaste(t, screen.paste))
  }
  function compositionend(e: CompositionEvent) {
    composing = false
    if (e.data) send(e.data)
    input.value = ''
  }

  let dragging = false
  function cell(e: PointerEvent) {
    const r = canvas.getBoundingClientRect()
    return cellAt(metrics!, e.clientX - r.left, e.clientY - r.top)
  }
  function down(e: PointerEvent) {
    if (!metrics) return
    dragging = true
    const p = cell(e)
    selection = { from: p, to: p }
    canvas.setPointerCapture(e.pointerId)
  }
  function move(e: PointerEvent) {
    if (dragging && selection) selection = { from: selection.from, to: cell(e) }
  }
  function up() {
    dragging = false
    if (selection && selection.from.x === selection.to.x && selection.from.y === selection.to.y) selection = undefined
    input.focus()
  }
</script>

<div class="overlay" role="dialog" aria-label={title}>
  <div class="window">
    <header>
      <span>{screen.title || title}</span>
      {#if screen.scrolled > 0}<em>↑ {screen.scrolled} lines back · type to return</em>{/if}
      {#if status}<em>{status}</em>{/if}
      <button class="quiet" onclick={onclose} title="Close (the session keeps running)">⌘W ✕</button>
    </header>
    <div class="box" style:--pad="{$theme.padding}px" bind:this={box} onclick={() => input.focus()} onwheel={wheel} role="presentation">
      {#if output}<pre>{output}</pre>{/if}
      <canvas bind:this={canvas} onpointerdown={down} onpointermove={move} onpointerup={up} class:hidden={!!output}></canvas>
      <textarea
        bind:this={input}
        class="input"
        aria-label="terminal input"
        onkeydown={keydown}
        onpaste={paste}
        oncompositionstart={() => (composing = true)}
        oncompositionend={compositionend}
        onblur={() => schedule()}
        onfocus={() => schedule()}
      ></textarea>
    </div>
  </div>
</div>

<style>
  .overlay { position: fixed; inset: 0; background: color-mix(in srgb, var(--bg-solid) 55%, transparent); backdrop-filter: blur(6px);
    display: flex; align-items: center; justify-content: center; z-index: 10; }
  .window { width: 88vw; height: 84vh; background: var(--bg); border: 1px solid var(--border); border-radius: var(--radius);
    display: flex; flex-direction: column; overflow: hidden; }
  header { display: flex; gap: 12px; align-items: center; height: 36px; padding: 0 6px 0 14px; border-bottom: 1px solid var(--border-subtle); }
  header span { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font: 600 11px var(--mono);
    letter-spacing: 0.08em; text-transform: uppercase; color: var(--text-2); }
  header em { font: 11px var(--mono); font-style: normal; color: var(--text-3); }
  .box { flex: 1; min-height: 0; padding: calc(var(--pad, 5px) + 4px); position: relative; overflow: hidden; }
  canvas { display: block; cursor: text; }
  .hidden { display: none; }
  pre { height: 100%; overflow: auto; font: var(--term-size, 13px)/1.3 var(--mono); white-space: pre-wrap; color: var(--text); }
  .input { position: absolute; opacity: 0; width: 1px; height: 1px; left: 0; top: 0; resize: none; }
</style>
