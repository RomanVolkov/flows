<script lang="ts">
 import type {Graph} from './graph'
 import {request} from './workspace'
 import {copy} from './copy'
 import TotalsView from './TotalsView.svelte'
 import Preview from './Preview.svelte'
 import {Totals as fetchTotals} from './usage-api'
 import type {Totals} from './usage'
 let {graph,selected,onattach,onremove}:{graph:Graph;selected:string;onattach:(id:string)=>void;onremove:(id:string)=>void}=$props()
 const node=$derived(graph.nodes.get(selected))
 let totals=$state<Totals>(), error=$state(''), busy=$state(false)
 const sessions=$derived([...graph.sessions.values()].filter(s=>s.node===selected).sort((a,b)=>a.started.localeCompare(b.started)))
 const live=$derived(sessions.some(s=>s.exit==null))
 const stoppable=$derived(live||node?.state==='running'||node?.state==='blocked')
 const resumable=$derived(node?.state==='stopped'||node?.label?.includes('interrupted')||node?.label?.includes('failed'))
 async function stop(){
  busy=true;error=''
  try{await request('stopNode',selected)}catch(e){error=String(e)}finally{busy=false}
 }
 async function resume(){busy=true;error='';try{await request('resumeNode',selected)}catch(e){error=String(e)}finally{busy=false}}
 $effect(()=>{const id=selected;let gone=false;totals=undefined;fetchTotals('',id).then(t=>{if(!gone)totals=t},e=>{if(!gone)error=String(e)});return()=>{gone=true}})
</script>
<aside>{#if node}
 <header><span class="dot" style:background="var(--state-{node.state})"></span><h2>{node.title||node.id}</h2></header>
 <p class="state mono">{node.state}{node.label?` · ${node.label}`:''}</p>
 <dl><dt class="label">kind</dt><dd class="mono">{node.kind}</dd>{#if node.flow}<dt class="label">workflow</dt><dd class="mono" use:copy>{node.flow}</dd>{/if}<dt class="label">id</dt><dd class="mono" use:copy>{node.id}</dd></dl>
 {#if node.detail}<pre class="block">{node.detail}</pre>{/if}
 {#each graph.edges.filter(e=>e.from===node.id||e.to===node.id) as edge}
  <p class="edge mono" use:copy={edge.to===node.id?edge.from:edge.to}>{edge.to===node.id?`← waits for ${edge.from}`:`→ needed by ${edge.to}`}{edge.reason?` · ${edge.reason}`:''}</p>
 {/each}
 <div class="buttons">
  {#if live}<button class="primary" onclick={()=>onattach(node.id)}>Terminal</button>{/if}
  {#if stoppable}<button disabled={busy} onclick={stop}>Stop</button>{/if}
  {#if resumable}<button class="primary" disabled={busy} onclick={resume}>Resume</button>{/if}
  <button disabled={busy} onclick={()=>onremove(node.id)}>Remove</button>
 </div>
 {#if sessions.length}<h3 class="label">Sessions</h3>
  <ul class="sessions">{#each sessions as s}<li class="mono">{s.agent} · {s.mode} · {s.exit==null?'running':`exit ${s.exit}`}</li>{/each}</ul>
  {#if sessions[sessions.length-1].attempt}<h3 class="label">Preview</h3><div class="screen"><Preview attempt={sessions[sessions.length-1].attempt!}/></div>{/if}
 {/if}
 {#if totals?.requests}<h3 class="label">Usage</h3><TotalsView t={totals}/>{/if}
 {#if error}<p class="error">{error}</p>{/if}
 {:else}<p class="hint label">Select a node</p>{/if}</aside>
<style>
 aside{width:440px;flex:none;margin:8px 8px 8px 0;padding:14px;overflow:auto;background:var(--elevated);border:1px solid var(--border-subtle);border-radius:var(--radius);font-size:13px}
 header{display:flex;align-items:center;gap:10px}h2{flex:1;font:600 15px var(--sans);overflow-wrap:anywhere}.dot{width:8px;height:8px;border-radius:50%;flex:none}
 .state{margin:4px 0 0 18px;color:var(--text-2);font-size:12px}dl{display:grid;grid-template-columns:3.5rem 1fr;gap:4px 10px;margin:14px 0;align-items:baseline}dd{overflow-wrap:anywhere;color:var(--text-2);font-size:12px}
 h3{margin:18px 0 6px}.block{white-space:pre-wrap;background:var(--surface);border:1px solid var(--border-subtle);padding:8px 10px;border-radius:var(--radius);font:12px/1.5 var(--mono);color:var(--text-2)}
 .edge{color:var(--text-3);font-size:12px;margin:4px 0}.buttons{display:flex;flex-wrap:wrap;gap:8px;margin-top:14px}.error{color:var(--danger);font-size:12px}.hint{margin-top:8px}
 .screen{margin-top:8px;max-height:190px;overflow:hidden;border:1px solid var(--border-subtle);border-radius:var(--radius)}
 .sessions{list-style:none;display:grid;gap:4px;margin-top:6px}.sessions li{min-width:0;padding:5px 7px;border:1px solid var(--border-subtle);border-radius:var(--radius);background:var(--surface);color:var(--text-2);font-size:11px;overflow-wrap:anywhere}
</style>
