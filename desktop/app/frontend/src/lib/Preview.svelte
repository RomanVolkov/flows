<script lang="ts">
  import { onMount } from 'svelte'
  import { measure, paint, type Metrics } from './terminal/paint'
  import { applyFrame, blank, type Screen } from './terminal/screen'
  import { monoStack, theme } from './theme'
  import { request } from './workspace'

  let { attempt, onopen }: { attempt: string; onopen?: () => void } = $props()
  let canvas: HTMLCanvasElement
  let screen = $state<Screen | undefined>()
  let message = $state('Loading preview…')
  let metrics: Metrics | undefined
  let connection = ''
  let timer = 0
  let stopped = false
  let pending = false

  function schedule() {
    if (pending) return
    pending = true
    requestAnimationFrame(() => {
      pending = false
      if (!screen || !metrics || !canvas) return
      const dpr = window.devicePixelRatio || 1
      const width = Math.round(screen.cols * metrics.w * dpr)
      const height = Math.round(screen.rows * metrics.h * dpr)
      if (canvas.width !== width || canvas.height !== height) {
        canvas.width = width
        canvas.height = height
      }
      const ctx = canvas.getContext('2d')!
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
      paint(ctx, screen, metrics, false, () => false, {
        cursor: $theme.cursor,
        selection: $theme.selection_background,
        selectionInk: $theme.selection_foreground,
      })
    })
  }

  $effect(() => {
    screen
    $theme.background_opacity
    $theme.cursor
    $theme.selection_background
    $theme.selection_foreground
    schedule()
  })

  function decode(value?: string): Uint8Array {
    return value ? Uint8Array.from(atob(value), (char) => char.charCodeAt(0)) : new Uint8Array()
  }

  async function refresh() {
    try {
      const result = await request<{ frame?: string; screen?: string }>('preview', attempt)
      if (stopped) return
      if (result.frame) {
        const frame = JSON.parse(new TextDecoder().decode(decode(result.frame)))
        screen = applyFrame(blank(), frame)
        message = ''
      } else if (result.screen) {
        // Older saved sessions only have ANSI replay. Keep that visible until they run again
        // and persist a cell frame.
        message = new TextDecoder().decode(decode(result.screen)).replace(/\x1b\[[0-?]*[ -/]*[@-~]/g, '').trim() || 'No session output yet.'
      } else {
        message = 'No session output yet.'
      }
    } catch (error) {
      if (!stopped) message = String(error)
    }
    if (!stopped && onopen) timer = window.setTimeout(refresh, 1500)
  }

  async function follow() {
    const api = window.go.main.App
    try {
      connection = await api.OpenPreview(attempt)
      while (!stopped) {
        const frame = JSON.parse(await api.ReadTerminal(connection))
        if (stopped || frame.kind === 6) break
        if (frame.kind === 7) {
          screen = applyFrame(screen ?? blank(), JSON.parse(new TextDecoder().decode(decode(frame.payload))))
          message = ''
        } else if (frame.kind === 1 && !screen) {
          break // Hosts without cell frames expose their text through the snapshot endpoint.
        }
      }
    } catch {
      // Previously launched hosts only support snapshot previews.
    } finally {
      if (connection) await api.Detach(connection).catch(() => {})
      if (!stopped) void refresh()
    }
  }

  onMount(() => {
    metrics = measure(canvas.getContext('2d')!, monoStack($theme), $theme.font_size)
    if (onopen) void follow()
    else void refresh()
    return () => {
      stopped = true
      clearTimeout(timer)
      if (connection) { void window.go.main.App.Detach(connection); connection = '' }
    }
  })
</script>

<button class="preview" class:live={!!onopen} onclick={onopen} title={onopen ? "Open the terminal" : undefined}>
  <canvas bind:this={canvas} class:hidden={!screen} style:background={screen ? '#' + screen.bg.toString(16).padStart(6, '0') : undefined}></canvas>
  {#if !screen}
    <pre>{message}</pre>
  {/if}
</button>

<style>

  .preview { display: block; width: 100%; height: auto; padding: 6px; border: 1px solid var(--border-subtle); border-radius: var(--radius);
    background: var(--surface); cursor: default; }
  .preview.live { cursor: pointer; }
  .preview.live:hover { border-color: var(--border); }
  canvas { display: block; width: 100%; height: auto; }
  canvas.hidden { display: none; }
  pre { margin: 0; padding: 8px; color: var(--text-2); white-space: pre-wrap; overflow-wrap: anywhere; font: 10px/1.4 var(--mono); }
</style>
