<script lang="ts">
 import {onMount} from 'svelte'
 import Canvas from './lib/canvas/Canvas.svelte'
 import {roots} from './lib/canvas/select'
 import Sidebar from './lib/Sidebar.svelte'
 import Selection from './lib/Selection.svelte'
 import NewSession from './lib/NewSession.svelte'
 import Spend from './lib/Spend.svelte'
 import Settings from './lib/Settings.svelte'
 import Terminal from './lib/terminal/Terminal.svelte'
 import {request,graphOf,type Workspace,type LiveSession} from './lib/workspace'
 import {applyTheme,configureTerminal} from './lib/theme'
 let snapshot=$state<Workspace>({cursor:-1,nodes:[],edges:[],attempts:[]})
 let sessions=$state<LiveSession[]>([])
 let graph=$derived(graphOf(snapshot,sessions)), selection=$state<string[]>([])
 let selected=$derived(selection.length===1?selection[0]:undefined)
 let term=$state<{id:string;title:string;agent:string}>(),creating=$state(false),settingsOpen=$state(false),error=$state('')
 let removing=$state<{ids:string[];title:string;under:number;error?:string}>(),removeBusy=$state(false)
 function attach(id:string){const n=snapshot.nodes?.find(x=>x.id===id); const at=snapshot.attempts?.filter(x=>x.node_id===id).sort((a,b)=>b.number-a.number)[0];if(n&&at)term={id:at.id,title:n.name,agent:sessions.find(s=>s.attempt_id===at.id)?.command.split('/').pop()??'harness'}}
 function shortcut(e:KeyboardEvent){if(e.metaKey&&e.key==='.') {e.preventDefault();settingsOpen=true}else if(e.metaKey&&e.key.toLowerCase()==='n'){e.preventDefault();creating=true}else if(e.metaKey&&e.key==='Enter'&&selected)attach(selected)}
 function askRemove(ids:string[]){const top=roots(graph,ids.filter(id=>graph.nodes.has(id)));if(!top.length)return;const walk=(id:string):number=>{const kids=[...graph.nodes.values()].filter(n=>n.parent===id);return kids.reduce((n,k)=>n+1+walk(k.id),0)};const node=graph.nodes.get(top[0])!;removing={ids:top,title:top.length===1?node.title||node.id:`${top.length} nodes`,under:top.reduce((n,id)=>n+walk(id),0)}}
 async function removeNodes(){if(!removing||removeBusy)return;removeBusy=true;const current=removing;try{await Promise.all(current.ids.map(id=>request('removeNode',id)));selection=[];term=undefined;removing=undefined}catch(e){removing={...current,error:String(e)}}finally{removeBusy=false}}
 function focus(el:HTMLElement){el.focus()}
 onMount(()=>{applyTheme();request<{terminal:{font_family:string;font_size:number;opacity:number;blur:boolean}}>('settings').then(s=>configureTerminal(s.terminal)).catch(()=>{});const offSnapshot=window.runtime.EventsOn('flows:snapshot',(state:{workspace:Workspace;sessions:LiveSession[]})=>{snapshot=state.workspace;sessions=state.sessions??[]});const offConnection=window.runtime.EventsOn('flows:connection',(state:{ok:boolean;error?:string})=>{error=state.ok?'':state.error??'daemon is unavailable'});window.go.main.App.Follow();return()=>{offSnapshot();offConnection()}})
</script>
<svelte:window onkeydown={shortcut}/>
<div class="app">{#if error}<div class="banner">The daemon does not answer: {error}. Retrying…</div>{/if}
<div class="native-controls-space" aria-hidden="true"></div>
<header class="top"><span class="brand mono">flows</span><span class="grow"></span><Spend/><Settings bind:open={settingsOpen}/><button class="primary" onclick={()=>creating=true}>New session <span class="kbd">⌘N</span></button></header>
<div class="main"><div class="canvas"><Canvas {graph} bind:selection onopen={attach} onremove={askRemove}/>
{#if !graph.nodes.size}<div class="empty"><h1>{snapshot.cursor<0?'Connecting to Flows…':'Your workspace is empty'}</h1>{#if snapshot.cursor>=0}<button class="primary" onclick={()=>creating=true}>Start a session</button>{/if}</div>{/if}</div>
{#if selected&&graph.nodes.has(selected)}<Sidebar {graph} {selected} onattach={attach} onremove={id=>askRemove([id])}/>
{:else if selection.length>1}<Selection {graph} ids={selection} onpick={id=>selection=[id]} onremove={askRemove}/>{/if}</div>
{#if term}<Terminal sessionId={term.id} title={term.title} agent={term.agent} onclose={()=>term=undefined}/>{/if}
{#if creating}<NewSession started={(id:string)=>{selection=[id];creating=false}} close={()=>creating=false}/>{/if}
{#if removing}<div class="overlay" role="dialog" aria-label="Remove" tabindex="-1" onkeydown={e=>{if(e.key==='Escape'&&!removeBusy){e.preventDefault();removing=undefined}}}><form class="confirm" onsubmit={e=>{e.preventDefault();removeNodes()}}>
 <p>Remove <b>{removing.title}</b>{removing.under?` and ${removing.under} descendant${removing.under===1?'':'s'}`:''}?</p>
 <p class="hint">This permanently removes the selected nodes and their execution history. Active sessions are stopped first; log files stay on disk.</p>
 {#if removing.error}<p class="error">{removing.error}</p>{/if}
 <div class="buttons"><button type="button" disabled={removeBusy} onclick={()=>removing=undefined}>Cancel</button><button class="primary" disabled={removeBusy} use:focus>{removeBusy?'Removing…':'Remove'}</button></div>
 </form></div>{/if}
<footer>{graph.nodes.size} nodes · ⌘N new session · f fits the view · shift adds · space + drag pans · pinch zooms · double click opens terminal · Esc clears selection</footer></div>
<style>
 .overlay{position:fixed;inset:0;z-index:20;display:flex;align-items:center;justify-content:center;background:color-mix(in srgb,var(--bg-solid) 55%,transparent);backdrop-filter:blur(6px)}
 .confirm{width:min(420px,92vw);padding:18px;background:var(--elevated);border:1px solid var(--border);border-radius:var(--radius);font-size:13px;display:flex;flex-direction:column;gap:8px}
 .confirm b{font-weight:600;overflow-wrap:anywhere}.confirm .hint{color:var(--text-3);font-size:12px}.confirm .error{color:var(--danger);font:12px var(--mono)}
 .confirm .buttons{display:flex;justify-content:flex-end;gap:8px;margin-top:6px}
</style>
