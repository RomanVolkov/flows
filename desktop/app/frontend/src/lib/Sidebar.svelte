<script lang="ts">
 import type {Graph} from './graph'
 import {request} from './workspace'
 import {copy} from './copy'
 import TotalsView from './TotalsView.svelte'
 import Preview from './Preview.svelte'
 import {Totals as fetchTotals} from './usage-api'
 import type {Totals} from './usage'
 let {graph,selected,onattach}:{graph:Graph;selected:string;onattach:(id:string)=>void}=$props()
 const node=$derived(graph.nodes.get(selected))
 let totals=$state<Totals>(), error=$state(''), busy=$state(false)
 async function stop(){
  const s=[...graph.sessions.values()].find(x=>x.node===selected&&x.exit==null)
  if(!s)return
  busy=true;error=''
  try{await request('stopSession',s.id)}catch(e){error=String(e)}finally{busy=false}
 }
 $effect(()=>{const id=selected;let gone=false;totals=undefined;fetchTotals('',id).then(t=>{if(!gone)totals=t},e=>{if(!gone)error=String(e)});return()=>{gone=true}})
</script>
<aside>{#if node}
 <header><span class="dot" style:background="var(--state-{node.state})"></span><h2>{node.title||node.id}</h2></header>
 <p class="state mono">{node.state}{node.label?` · ${node.label}`:''}</p>
 <dl><dt class="label">kind</dt><dd class="mono">{node.kind}</dd>{#if node.flow}<dt class="label">workflow</dt><dd class="mono" use:copy>{node.flow}</dd>{/if}<dt class="label">id</dt><dd class="mono" use:copy>{node.id}</dd></dl>
 {#if node.detail}<pre class="block">{node.detail}</pre>{/if}
 {#each [...graph.sessions.values()].filter(s=>s.node===node.id) as s}
  <section><h3 class="label">Harness session</h3><p class="mono">{s.agent} · {s.mode} · {s.exit==null?'running':`exit ${s.exit}`}</p>
   {#if s.exit==null}<div class="buttons"><button class="primary" onclick={()=>onattach(node.id)}>Open terminal</button><button disabled={busy} onclick={stop}>Stop</button></div>{/if}
   {#if s.attempt}<div class="screen"><Preview attempt={s.attempt}/></div>{/if}
  </section>
 {/each}
 {#if totals?.requests}<h3 class="label">Usage</h3><TotalsView t={totals}/>{/if}
 {#if error}<p class="error">{error}</p>{/if}
 {:else}<p class="hint label">Select a node</p>{/if}</aside>
<style>
 aside{width:440px;flex:none;margin:8px 8px 8px 0;padding:14px;overflow:auto;background:var(--elevated);border:1px solid var(--border-subtle);border-radius:var(--radius);font-size:13px}
 header{display:flex;align-items:center;gap:10px}h2{flex:1;font:600 15px var(--sans);overflow-wrap:anywhere}.dot{width:8px;height:8px;border-radius:50%;flex:none}
 .state{margin:4px 0 0 18px;color:var(--text-2);font-size:12px}dl{display:grid;grid-template-columns:3.5rem 1fr;gap:4px 10px;margin:14px 0}dd{overflow-wrap:anywhere;color:var(--text-2);font-size:12px}
 h3{margin:18px 0 6px}.block{white-space:pre-wrap;background:var(--surface);border:1px solid var(--border-subtle);padding:8px 10px;border-radius:var(--radius);font:12px/1.5 var(--mono);color:var(--text-2)}
 .buttons{display:flex;gap:8px;margin-top:10px}.error{color:var(--danger)}.hint{margin-top:8px}
 .screen{margin-top:8px;max-height:190px;overflow:hidden;border:1px solid var(--border-subtle);border-radius:var(--radius)}
</style>
