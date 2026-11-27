<script lang="ts">
 import type {Graph,State} from './graph'
 import {Totals as fetchTotals} from './usage-api'
 import TotalsView from './TotalsView.svelte'
 import {sum,type Totals} from './usage'
 import {roots} from './canvas/select'
 import {request} from './workspace'
 let {graph,ids,onpick,onremove}:{graph:Graph;ids:string[];onpick:(id:string)=>void;onremove:(ids:string[])=>void}=$props()
 const nodes=$derived(ids.map(id=>graph.nodes.get(id)).filter(n=>!!n))
 const order:State[]=['blocked','running','starting','watching','pending','failed','stopped','done']
 const states=$derived(order.map(s=>[s,nodes.filter(n=>n.state===s).length] as const).filter(([,n])=>n))
 const stoppable=$derived(roots(graph,ids).filter(id=>{const n=graph.nodes.get(id);return n&&(n.state==='running'||n.state==='blocked')}))
 let busy=$state(false),errors=$state<string[]>([])
 async function stop(){busy=true;const out=await Promise.all(stoppable.map(async id=>{try{await request('stopNode',id);return ''}catch(e){return `${graph.nodes.get(id)?.title??id}: ${String(e)}`}}));errors=out.filter(Boolean);busy=false}
 let totals=$state<Totals>()
 $effect(()=>{const selected=[...ids];let stop=false;totals=undefined;Promise.all(selected.map(id=>fetchTotals('',id))).then(list=>{if(!stop)totals=sum(list)},()=>{if(!stop)totals=undefined});return()=>{stop=true}})

 const top=$derived(roots(graph,ids))
</script>

<aside>
  <h2>{ids.length} nodes</h2>
  <ul class="states mono">
    {#each states as [s, n]}
      <li><span class="dot" style:background="var(--state-{s})"></span>{n} {s}</li>
    {/each}
  </ul>
  <div class="buttons">
    {#if stoppable.length}<button disabled={busy} onclick={stop}>Stop {stoppable.length}</button>{/if}
    <button disabled={busy} title="Remove them and everything under them (⌘⌫)" onclick={() => onremove(top)}>Remove {top.length}</button>
  </div>
  {#each errors as e}<p class="error mono">{e}</p>{/each}

  {#if totals && totals.requests > 0}
    <h3 class="label">Tokens</h3>
    <TotalsView t={totals} />
  {/if}

  <h3 class="label">Picked</h3>
  <ul class="nodes">
    {#each nodes as n (n.id)}
      <li><button class="quiet" onclick={() => onpick(n.id)} title="Show only this one">
        <span class="dot" style:background="var(--state-{n.state})"></span><span class="t">{n.title || n.id}</span><span class="s mono">{n.state}</span>
      </button></li>
    {/each}
  </ul>
  <p class="hint">shift-click adds or takes out · ⌘A picks all · Esc clears</p>
</aside>

<style>
  aside { width: 440px; flex: none; margin: 8px 8px 8px 0; padding: 14px; overflow: auto; background: var(--elevated);
    border: 1px solid var(--border-subtle); border-radius: var(--radius); font-size: 13px; }
  h2 { font: 600 15px var(--sans); letter-spacing: -0.01em; }
  h3 { margin: 18px 0 6px; }
  .states { list-style: none; display: flex; flex-wrap: wrap; gap: 4px 14px; margin: 8px 0 0; font-size: 12px; color: var(--text-2); }
  .states li { display: flex; align-items: center; gap: 6px; }
  .dot { width: 8px; height: 8px; border-radius: 50%; flex: none; }
  .buttons { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 14px; }
  .error { color: var(--danger); font-size: 12px; margin-top: 6px; overflow-wrap: anywhere; }
  .nodes { list-style: none; display: flex; flex-direction: column; gap: 2px; }
  .nodes button { width: 100%; height: auto; min-height: 28px; display: flex; align-items: center; gap: 8px; text-align: left; }
  .nodes .t { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .nodes .s { color: var(--text-3); font-size: 11px; }
  .hint { color: var(--text-3); font-size: 11px; margin-top: 12px; }
</style>
