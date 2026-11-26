<script lang="ts">
  import { Totals as fetchTotals } from './usage-api'
  import Analytics from './Analytics.svelte'
  import TotalsView from './TotalsView.svelte'
  import { periods, tokens, total, type Totals } from './usage'

  // The top bar's token count of today; a click shows today, the week, the month and all time.
  let today = $state<Totals | undefined>()
  let open = $state(false)
  let pick = $state(0)
  let shown = $state<Totals | undefined>()
  let error = $state<string | undefined>()
  let chart = $state(false)

  const get = (since: string) => fetchTotals(since, '') as Promise<Totals>

  $effect(() => {
    let stop = false
    const tick = async () => {
      if (stop) return
      try {
        today = await get(periods(new Date())[0].since)
      } catch {
        today = undefined // an older daemon: nothing to show
      }
      if (!stop) setTimeout(tick, 30_000)
    }
    tick()
    return () => (stop = true)
  })

  $effect(() => {
    if (!open) return
    const since = periods(new Date())[pick].since
    shown = undefined
    get(since).then((t) => ((shown = t), (error = undefined)), (e) => (error = String(e)))
  })

  function key(e: KeyboardEvent) {
    if (open && e.key === 'Escape') open = false
  }
</script>

<svelte:window onkeydown={key} />

{#if today}
  <div class="spend">
    <button class="quiet" onclick={() => (open = !open)} title="Tokens claude and codex used">
      <span class="label">today</span> {tokens(total(today.usage))}
    </button>
    {#if open}
      <button class="scrim" aria-label="Close" onclick={() => (open = false)}></button>
      <div class="pop" role="dialog" aria-label="Tokens">
        <div class="tabs">
          {#each periods(new Date()) as p, i}
            <button class:on={i === pick} onclick={() => (pick = i)}>{p.label}</button>
          {/each}
        </div>
        {#if error}<p class="error mono">{error}</p>
        {:else if shown}<TotalsView t={shown} />
        {:else}<p class="mono wait">…</p>{/if}
        <button class="more" onclick={() => ((open = false), (chart = true))}>By day and model…</button>
      </div>
    {/if}
  </div>
{/if}
{#if chart}<Analytics onclose={() => (chart = false)} />{/if}

<style>
  .spend { position: relative; --wails-draggable: no-drag; }
  .spend > button.quiet { gap: 8px; }
  .scrim { position: fixed; inset: 0; z-index: 29; height: auto; padding: 0; border: none; background: transparent; backdrop-filter: none; cursor: default; }
  .scrim:hover { background: transparent; }
  .pop { position: absolute; right: 0; top: calc(100% + 6px); z-index: 30; width: 340px; padding: 12px;
    background: var(--elevated); border: 1px solid var(--border); border-radius: var(--radius); }
  .tabs { display: flex; gap: 4px; margin-bottom: 12px; }
  .tabs button { height: 26px; padding: 0 10px; flex: 1; justify-content: center; }
  .tabs button.on { color: var(--accent); border-color: color-mix(in srgb, var(--accent) 55%, var(--border)); }
  .more { width: 100%; justify-content: center; margin-top: 12px; }
  .wait { color: var(--text-3); font-size: 12px; }
  .error { color: var(--danger); font-size: 12px; overflow-wrap: anywhere; }
</style>
