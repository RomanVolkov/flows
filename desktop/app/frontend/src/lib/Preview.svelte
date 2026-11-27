<script lang="ts">
  import { onMount } from 'svelte'
  import { measure, paint, type Metrics } from './terminal/paint'
  import { applyFrame, blank, type Screen } from './terminal/screen'
  import { monoStack, theme } from './theme'
  import { request } from './workspace'

  let { attempt }: { attempt: string } = $props()
  let canvas: HTMLCanvasElement
  let screen = $state<Screen | undefined>()
  let message = $state('Loading preview…')
  let metrics: Metrics | undefined
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
      }, $theme.background_opacity)
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
    if (!stopped) timer = window.setTimeout(refresh, 1500)
  }

  onMount(() => {
    metrics = measure(canvas.getContext('2d')!, monoStack($theme), $theme.font_size)
    refresh()
    return () => {
      stopped = true
      clearTimeout(timer)
    }
  })
</script>

<div class="preview">
  <canvas bind:this={canvas} class:hidden={!screen} style:background={screen ? '#' + screen.bg.toString(16).padStart(6, '0') : undefined}></canvas>
  {#if !screen}
    <pre>{message}</pre>
  {/if}
</div>

<style>
  .preview { width: 100%; min-height: 48px; background: var(--surface); }
  canvas { display: block; width: 100%; height: auto; }
  canvas.hidden { display: none; }
  pre { margin: 0; padding: 8px; color: var(--text-2); white-space: pre-wrap; overflow-wrap: anywhere; font: 10px/1.4 var(--mono); }
</style>
