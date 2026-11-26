<script lang="ts">
  import { byModel, tokens, total, type Totals } from './usage'

  // What requests used together: the total, the kinds of tokens, the models.
  let { t }: { t: Totals } = $props()
  const models = $derived(byModel(t))
  const all = $derived(total(t.usage))
</script>

{#if t.requests === 0}
  <p class="none mono">No recorded usage</p>
{:else}
  <div class="total mono"><b>{tokens(all)}</b><span>{t.requests} request{t.requests === 1 ? '' : 's'}</span></div>
  <dl class="mono">
    <dt>input</dt><dd>{tokens(t.usage.input)}</dd>
    <dt>output</dt><dd>{tokens(t.usage.output)}</dd>
    <dt>cache read</dt><dd>{tokens(t.usage.cache_read)}</dd>
    <dt>cache write</dt><dd>{tokens(t.usage.cache_write)}</dd>
  </dl>
  {#if models.length}
    <ul class="mono">
      {#each models as [m, n]}
        <li>
          <span class="name">{m}</span><span class="n">{tokens(n)}</span>
          <span class="bar"><span style:width="{all ? (n / all) * 100 : 0}%"></span></span>
        </li>
      {/each}
    </ul>
  {/if}
{/if}

<style>
  .none { color: var(--text-3); font-size: 12px; }
  .total { display: flex; align-items: baseline; gap: 10px; }
  .total b { font-size: 18px; font-weight: 600; color: var(--text); }
  .total span { color: var(--text-3); font-size: 12px; }
  dl { display: grid; grid-template-columns: 6rem 1fr; gap: 2px 10px; margin-top: 6px; font-size: 12px; }
  dt { color: var(--text-3); }
  dd { color: var(--text-2); }
  ul { list-style: none; margin-top: 10px; display: flex; flex-direction: column; gap: 6px; font-size: 12px; }
  li { display: grid; grid-template-columns: 1fr auto; gap: 2px 10px; }
  .name { color: var(--text-2); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .n { color: var(--text-3); }
  .bar { grid-column: 1 / -1; height: 3px; border-radius: 2px; background: var(--surface); overflow: hidden; }
  .bar span { display: block; height: 100%; background: var(--accent); }
</style>
