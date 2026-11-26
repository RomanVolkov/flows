<script lang="ts">
  import { onMount } from 'svelte'
  import { Daily, Totals as fetchTotals } from './usage-api'
  import { colors, dayOf, days, series, thousands, ticks, type Day } from './chart'
  import { tokens, type Totals } from './usage'

  // Tokens by day: a line per model over a period, thousands of tokens up; or the same as a table.
  let { onclose }: { onclose: () => void } = $props()

  const PERIODS = [
    { label: '7 days', back: 6 },
    { label: '30 days', back: 29 },
    { label: '90 days', back: 89 },
    { label: 'All', back: -1 },
  ]
  let pick = $state(1)
  let table = $state(false)
  let data = $state<Day[] | undefined>()
  let hue = $state(new Map<string, string>())
  let harnesses = $state<Record<string, number>>({})
  let error = $state<string | undefined>()
  let w = $state(800)
  let hover = $state<number | undefined>()

  const today = dayOf(new Date())
  const firstOf = (back: number) => {
    const now = new Date()
    return new Date(now.getFullYear(), now.getMonth(), now.getDate() - back)
  }

  onMount(() => {
    fetchTotals('', '').then((t) => { const totals = t as Totals; hue = colors(totals.models); harnesses = totals.harnesses ?? {} }, () => {})
  })

  $effect(() => {
    const back = PERIODS[pick].back
    data = undefined
    hover = undefined
    Daily(back < 0 ? '' : firstOf(back).toISOString()).then(
      (d) => ((data = (d ?? []) as Day[]), (error = undefined)),
      (e) => (error = String(e)),
    )
  })

  const range = $derived.by(() => {
    const back = PERIODS[pick].back
    const first = back >= 0 ? dayOf(firstOf(back)) : (data?.[0]?.day ?? today)
    return days(first < today ? first : today, today)
  })
  const lines = $derived(data ? series(data, range, hue) : [])
  const sum = $derived(lines.reduce((a, s) => a + s.total, 0))
  const requests = $derived((data ?? []).filter((d) => range.includes(d.day)).reduce((a, d) => a + d.requests, 0))

  // geometry
  const H = 340
  const labelled = $derived(lines.length > 0 && lines.length <= 4) // few lines: named at their ends too
  const m = $derived({ l: 56, r: labelled ? 150 : 16, t: 12, b: 28 }) // r: room for the names
  const pw = $derived(Math.max(100, w - m.l - m.r))
  const ph = $derived(H - m.t - m.b)
  const max = $derived(Math.max(0, ...lines.flatMap((s) => s.values)) / 1000)
  const yTicks = $derived(ticks(max))
  const top = $derived(yTicks[yTicks.length - 1] || 1)
  const x = (i: number) => m.l + (range.length === 1 ? pw / 2 : (i * pw) / (range.length - 1))
  const y = (tokens: number) => m.t + ph - (tokens / 1000 / top) * ph
  const every = $derived(Math.max(1, Math.ceil(range.length / Math.max(2, Math.floor(pw / 70)))))
  const short = (d: string) => new Date(`${d}T12:00:00`).toLocaleDateString('en-US', { month: 'short', day: 'numeric' })
  const path = (vs: number[]) => vs.map((v, i) => `${i ? 'L' : 'M'}${x(i).toFixed(1)},${y(v).toFixed(1)}`).join('')

  // the names at the line ends, pushed apart so they do not overlap
  const ends = $derived.by(() => {
    const out = lines.map((s) => ({ s, at: y(s.values[s.values.length - 1] ?? 0) })).sort((a, b) => a.at - b.at)
    for (let i = 1; i < out.length; i++) out[i].at = Math.max(out[i].at, out[i - 1].at + 14)
    return out
  })

  function move(e: MouseEvent) {
    const r = (e.currentTarget as SVGRectElement).getBoundingClientRect()
    const px = e.clientX - r.left - 8 // the hit area starts 8px left of the plot
    hover = range.length === 1 ? 0 : Math.max(0, Math.min(range.length - 1, Math.round((px / pw) * (range.length - 1))))
  }
  const tip = $derived(
    hover === undefined ? [] : lines.map((s) => ({ s, v: s.values[hover!] })).filter((t) => t.v > 0).sort((a, b) => b.v - a.v),
  )

  function key(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      e.preventDefault()
      e.stopPropagation()
      onclose()
    }
  }
</script>

<svelte:window onkeydown={key} />

<div class="overlay" role="dialog" aria-label="Tokens by day">
  <section class="panel">
    <header>
      <h2>Tokens</h2>
      <div class="tabs">
        {#each PERIODS as p, i}<button class:on={i === pick} onclick={() => (pick = i)}>{p.label}</button>{/each}
      </div>
      <span class="grow"></span>
      <button onclick={() => (table = !table)}>{table ? 'Chart' : 'Table'}</button>
      <button class="quiet" onclick={onclose} title="Close (esc)">✕</button>
    </header>

    {#if error}
      <p class="error mono">{error}</p>
    {:else if !data}
      <p class="muted mono">…</p>
    {:else if lines.length === 0}
      <p class="muted mono">no claude or codex tokens in this period</p>
    {:else}
      <div class="summary mono"><b>{tokens(sum)}</b> tokens · {requests} requests
        {#if Object.keys(harnesses).length}<span class="harnesses">{#each Object.entries(harnesses).sort((a, b) => b[1] - a[1]) as [name, amount]}<span>{name} {tokens(amount)}</span>{/each}</span>{/if}
      </div>
      <ul class="legend mono">
        {#each lines as s}
          <li><span class="swatch" style:background={s.color}></span>{s.model}<span class="n">{tokens(s.total)}</span></li>
        {/each}
      </ul>

      {#if table}
        <div class="table">
          <table class="mono">
            <thead><tr><th>day</th>{#each lines as s}<th>{s.model}</th>{/each}<th>total</th></tr></thead>
            <tbody>
              {#each [...range.keys()].reverse() as i}
                <tr>
                  <td>{range[i]}</td>
                  {#each lines as s}<td>{s.values[i] ? thousands(s.values[i]) : '·'}</td>{/each}
                  <td>{thousands(lines.reduce((a, s) => a + s.values[i], 0))}</td>
                </tr>
              {/each}
            </tbody>
          </table>
          <p class="muted">thousands of tokens</p>
        </div>
      {:else}
        <div class="chart" bind:clientWidth={w}>
          <svg width={w} height={H} role="img" aria-label="Thousands of tokens by day and model">
            {#each yTicks as t}
              <line class="grid" x1={m.l} x2={m.l + pw} y1={y(t * 1000)} y2={y(t * 1000)} />
              <text class="axis" x={m.l - 8} y={y(t * 1000)} text-anchor="end" dominant-baseline="middle">{t.toLocaleString('en-US')}</text>
            {/each}
            {#each range as d, i}
              {#if i % every === 0 || i === range.length - 1}
                <text class="axis" x={x(i)} y={H - 8} text-anchor="middle">{short(d)}</text>
              {/if}
            {/each}
            {#each lines as s}
              <path d={path(s.values)} fill="none" stroke={s.color} stroke-width="2" stroke-linejoin="round" stroke-linecap="round" />
              {#if range.length === 1}<circle cx={x(0)} cy={y(s.values[0])} r="4" fill={s.color} />{/if}
            {/each}
            {#if labelled}
              {#each ends as e}
                <text class="end" x={x(range.length - 1) + 10} y={e.at} dominant-baseline="middle">{e.s.model}</text>
              {/each}
            {/if}
            {#if hover !== undefined}
              <line class="cross" x1={x(hover)} x2={x(hover)} y1={m.t} y2={m.t + ph} />
              {#each lines as s}
                <circle cx={x(hover)} cy={y(s.values[hover])} r="4" fill={s.color} stroke="var(--elevated)" stroke-width="2" />
              {/each}
            {/if}
            <rect class="hit" x={m.l - 8} y={m.t} width={pw + 16} height={ph} role="presentation"
              onmousemove={move} onmouseleave={() => (hover = undefined)} />
          </svg>
          {#if hover !== undefined}
            <div class="tip mono" style:left="{x(hover)}px" class:flip={x(hover) > w / 2}>
              <div class="day">{short(range[hover])}</div>
              {#each tip as t}
                <div class="row"><span class="swatch" style:background={t.s.color}></span><span class="name">{t.s.model}</span><span>{thousands(t.v)}k</span></div>
              {:else}
                <div class="row muted">nothing</div>
              {/each}
            </div>
          {/if}
          <p class="muted">thousands of tokens · a session counts on the day it ended, a running one today</p>
        </div>
      {/if}
    {/if}
  </section>
</div>

<style>
  .overlay { position: fixed; inset: 0; z-index: 40; display: flex; align-items: flex-start; justify-content: center; padding-top: 8vh;
    background: color-mix(in srgb, var(--bg-solid) 55%, transparent); backdrop-filter: blur(6px); }
  .panel { width: min(1040px, calc(100vw - 48px)); max-height: 84vh; overflow: auto; padding: 16px 18px; background: var(--elevated);
    border: 1px solid var(--border); border-radius: var(--radius); font-size: 13px; }
  header { display: flex; align-items: center; gap: 12px; margin-bottom: 14px; }
  h2 { font: 600 15px var(--sans); }
  .tabs { display: flex; gap: 4px; }
  .tabs button { height: 26px; padding: 0 10px; }
  .tabs button.on { color: var(--accent); border-color: color-mix(in srgb, var(--accent) 55%, var(--border)); }
  .grow { flex: 1; }
  .summary { color: var(--text-2); font-size: 12px; }
  .summary b { font-size: 18px; font-weight: 600; color: var(--text); margin-right: 4px; }
  .harnesses { display: flex; gap: 10px; margin-top: 4px; color: var(--text-3); font-size: 11px; }
  .legend { list-style: none; display: flex; flex-wrap: wrap; gap: 6px 16px; margin: 10px 0 6px; font-size: 12px; color: var(--text-2); }
  .legend li { display: flex; align-items: center; gap: 6px; }
  .legend .n { color: var(--text-3); }
  .swatch { width: 10px; height: 10px; border-radius: 3px; flex: none; }
  .chart { position: relative; }
  svg { display: block; }
  .grid { stroke: var(--border-subtle); stroke-width: 1; }
  .axis { fill: var(--text-3); font: 11px var(--mono); }
  .end { fill: var(--text-2); font: 12px var(--mono); }
  .cross { stroke: var(--text-3); stroke-width: 1; stroke-dasharray: 3 3; }
  .hit { fill: transparent; cursor: crosshair; }
  .tip { position: absolute; top: 12px; transform: translateX(12px); pointer-events: none; min-width: 180px; padding: 8px 10px;
    background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius); font-size: 12px; }
  .tip.flip { transform: translateX(calc(-100% - 12px)); }
  .tip .day { color: var(--text); margin-bottom: 4px; }
  .tip .row { display: flex; align-items: center; gap: 6px; color: var(--text-2); }
  .tip .name { flex: 1; }
  .muted { color: var(--text-3); font-size: 12px; margin-top: 6px; }
  .error { color: var(--danger); font-size: 12px; overflow-wrap: anywhere; }
  .table { max-height: 56vh; overflow: auto; }
  table { border-collapse: collapse; font-size: 12px; width: 100%; }
  th, td { text-align: right; padding: 3px 10px; border-bottom: 1px solid var(--border-subtle); white-space: nowrap; }
  th:first-child, td:first-child { text-align: left; }
  th { color: var(--text-3); font-weight: 500; position: sticky; top: 0; background: var(--elevated); }
  td { color: var(--text-2); }
</style>
