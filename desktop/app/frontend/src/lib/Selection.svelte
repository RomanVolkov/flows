<script lang="ts">
 import type {Graph,State} from './graph'
 import {Totals as fetchTotals} from './usage-api'
 import TotalsView from './TotalsView.svelte'
 import {sum,type Totals} from './usage'
 let {graph,ids,onpick}:{graph:Graph;ids:string[];onpick:(id:string)=>void}=$props()
 const nodes=$derived(ids.map(id=>graph.nodes.get(id)).filter(n=>!!n))
 const order:State[]=['blocked','running','starting','watching','pending','failed','stopped','done']
 const states=$derived(order.map(s=>[s,nodes.filter(n=>n.state===s).length] as const).filter(([,n])=>n))
 let totals=$state<Totals>()
 $effect(()=>{const selected=[...ids];let stop=false;totals=undefined;Promise.all(selected.map(id=>fetchTotals('',id))).then(list=>{if(!stop)totals=sum(list)},()=>{if(!stop)totals=undefined});return()=>{stop=true}})
</script>
<aside><h2>{nodes.length} selected</h2><ul class="states mono">{#each states as [state,count]}<li><i style:background="var(--state-{state})"></i>{count} {state}</li>{/each}</ul>
 {#if totals?.requests}<h3 class="label">Combined usage</h3><TotalsView t={totals}/>{/if}
 <h3 class="label">Nodes</h3><ul class="nodes">{#each nodes as n}<li><button class="quiet" onclick={()=>onpick(n.id)}><i style:background="var(--state-{n.state})"></i><span>{n.title||n.id}</span><small>{n.state}</small></button></li>{/each}</ul>
 <p class="hint">Click a node to inspect it individually.</p></aside>
<style>
 aside{width:440px;flex:none;margin:8px 8px 8px 0;padding:14px;overflow:auto;background:var(--elevated);border:1px solid var(--border-subtle);border-radius:var(--radius);font-size:13px}h2{font:600 15px var(--sans)}h3{margin:18px 0 6px}.states,.nodes{list-style:none;padding:0}.states{display:flex;flex-wrap:wrap;gap:5px 14px;color:var(--text-2);font-size:12px}.states li,.nodes button{display:flex;align-items:center;gap:8px}i{display:inline-block;width:8px;height:8px;border-radius:50%;flex:none}.nodes li{margin:2px 0}.nodes button{width:100%;height:auto;min-height:28px;text-align:left}.nodes button span{flex:1;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.nodes small{color:var(--text-3)}.hint{color:var(--text-3);font-size:11px;margin-top:12px}
</style>
