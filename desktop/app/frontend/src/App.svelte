<script lang="ts">
 import {onMount} from 'svelte'
 import {sessionsOf} from './lib/graph'
 import Canvas from './lib/canvas/Canvas.svelte'
 import {roots} from './lib/canvas/select'
 import Sidebar from './lib/Sidebar.svelte'
 import Selection from './lib/Selection.svelte'
 import NewSession from './lib/NewSession.svelte'
 import Spend from './lib/Spend.svelte'
 import Settings from './lib/Settings.svelte'
 import Terminal from './lib/terminal/Terminal.svelte'
 import {request,graphOf,type Workspace,type LiveSession} from './lib/workspace'
 import {configureTerminal} from './lib/theme'
 let snapshot=$state<Workspace>({cursor:-1,nodes:[],edges:[],attempts:[]})
 let sessions=$state<LiveSession[]>([])
 let graph=$derived(graphOf(snapshot,sessions)), selection=$state<string[]>([])
 let selected=$derived(selection.length===1?selection[0]:undefined)
 let term=$state<{id:string;title:string;agent:string}>(),creating=$state(false),settingsOpen=$state(false),error=$state('')
 let attention = $state(new Set<string>())
 function receive(state: {workspace:Workspace;sessions:LiveSession[]}) {
   const previous = new Map((snapshot.nodes ?? []).map(n => [n.id,n.status]))
   const nodes = state.workspace.nodes ?? []
   const present = new Set(nodes.map(n => n.id))
   const next = new Set([...attention].filter(id => present.has(id)))
   for (const node of nodes) {
     if (snapshot.cursor >= 0 && node.id !== selected && node.status === 'waiting_for_input' && previous.get(node.id) !== node.status) next.add(node.id)
   }
   attention = next
   if (selection.some(id => !present.has(id))) selection = selection.filter(id => present.has(id))
   snapshot = state.workspace
   sessions = state.sessions ?? []
 }
 $effect(() => {
   if (selected && attention.has(selected)) {
     const next = new Set(attention)
     next.delete(selected)
     attention = next
   }
 })
 let removing=$state<{ids:string[];title:string;under:number;error?:string}>(),removeBusy=$state(false)
 function attach(id:string) {
   const live = sessionsOf(graph, id).filter(s => s.exit == null && s.attempt).at(-1)
   if (live?.attempt) term = {id:live.attempt, title:graph.nodes.get(id)?.title ?? id, agent:live.agent}
 }
 function shortcut(e:KeyboardEvent) {
   if (!e.metaKey || e.defaultPrevented || term || creating || removing || document.querySelector('[role="dialog"], dialog[open]')) return
   if (e.key === '.') settingsOpen = true
   else if (e.key.toLowerCase() === 'n') creating = true
   else if (e.key === 'Enter' && selected && !(e.target instanceof HTMLElement && e.target.closest('input, textarea, select, [contenteditable]'))) attach(selected)
   else return
   e.preventDefault()
 }
 function askRemove(ids:string[]){const top=roots(graph,ids.filter(id=>graph.nodes.has(id)));if(!top.length)return;const walk=(id:string):number=>{const kids=[...graph.nodes.values()].filter(n=>n.parent===id);return kids.reduce((n,k)=>n+1+walk(k.id),0)};const node=graph.nodes.get(top[0])!;removing={ids:top,title:top.length===1?node.title||node.id:`${top.length} nodes`,under:top.reduce((n,id)=>n+walk(id),0)}}
 async function removeNodes(){if(!removing||removeBusy)return;removeBusy=true;const current=removing;try{await Promise.all(current.ids.map(id=>request('removeNode',id)));selection=[];term=undefined;removing=undefined}catch(e){removing={...current,error:String(e)}}finally{removeBusy=false}}
 function focus(el:HTMLElement){el.focus()}
 onMount(()=>{request<{terminal:{font_family:string;font_size:number;opacity:number;blur:boolean}}>('settings').then(s=>configureTerminal(s.terminal)).catch(()=>{});const offSnapshot=window.runtime.EventsOn('flows:snapshot',receive);const offConnection=window.runtime.EventsOn('flows:connection',(state:{ok:boolean;error?:string})=>{error=state.ok?'':state.error??'daemon is unavailable'});window.go.main.App.Follow();return()=>{offSnapshot();offConnection()}})
</script>
<svelte:window onkeydown={shortcut}/>
<div class="app">{#if error}<div class="banner">The daemon does not answer: {error}. Retrying…</div>{/if}
<header class="top"><span class="brand mono">flows</span><span class="grow"></span><Spend/><Settings bind:open={settingsOpen}/><button class="primary" onclick={()=>creating=true}>New session <span class="kbd">⌘N</span></button></header>
<div class="main"><div class="canvas"><Canvas {graph} {attention} bind:selection onopen={attach} onremove={askRemove}/></div>
{#if selected&&graph.nodes.has(selected)}<Sidebar {graph} {selected} onattach={attach} onremove={id=>askRemove([id])}/>
{:else if selection.length>1}<Selection {graph} ids={selection} onpick={id=>selection=[id]} onremove={askRemove}/>{/if}</div>
{#if term}{#key term.id}<Terminal sessionId={term.id} title={term.title} agent={term.agent} onclose={()=>term=undefined}/>{/key}{/if}
{#if creating}<NewSession started={(id:string)=>{selection=[id];creating=false}} close={()=>creating=false}/>{/if}
{#if removing}<div class="overlay" role="dialog" aria-label="Remove" tabindex="-1" onkeydown={e=>{if(e.key==='Escape'&&!removeBusy){e.preventDefault();removing=undefined}}}><form class="confirm" onsubmit={e=>{e.preventDefault();removeNodes()}}>
 <p>Remove <b>{removing.title}</b>{removing.under?` and ${removing.under} descendant${removing.under===1?'':'s'}`:''}?</p>
 <p class="hint">This permanently removes the selected nodes and their execution history. Active sessions are stopped first; log files stay on disk.</p>
 {#if removing.error}<p class="error">{removing.error}</p>{/if}
 <div class="buttons"><button type="button" disabled={removeBusy} onclick={()=>removing=undefined}>Cancel</button><button class="primary" disabled={removeBusy} use:focus>{removeBusy?'Removing…':'Remove'}</button></div>
 </form></div>{/if}
<footer>{graph.nodes.size} nodes · ⌘N new session · f fits the view · drag selects · drag a node (a group by its title) moves it with the selection · r puts it back · shift adds · space + drag or two fingers pan · pinch zooms · double click opens a terminal · ⌘⌫ removes · Esc clears the selection</footer>
</div>

<style>
  .app { display: flex; flex-direction: column; height: 100vh; }
  .top { display: flex; gap: 8px; align-items: center; padding: 8px 8px 0 8px; --wails-draggable: drag; }
  .top > button { --wails-draggable: no-drag; }
  .grow { flex: 1; }
  .brand { font-weight: 600; letter-spacing: 0.08em; text-transform: uppercase; color: var(--accent); font-size: 12px;
    padding: 0 8px 0 82px; }
  .kbd { opacity: 0.6; margin-left: 6px; font-size: 11px; }
  .main { flex: 1; min-height: 0; display: flex; }
  .canvas { flex: 1; min-width: 0; overflow: hidden; margin: 8px; border-radius: var(--radius);
    border: 1px solid var(--border-subtle); background: color-mix(in srgb, var(--bg-solid) 60%, transparent); }
  .banner { margin: 8px 8px 0; padding: 6px 12px; border-radius: var(--radius); font: 12px var(--mono);
    color: var(--state-blocked); border: 1px solid color-mix(in srgb, var(--state-blocked) 40%, transparent);
    background: color-mix(in srgb, var(--state-blocked) 8%, transparent); }
  .overlay { position: fixed; inset: 0; z-index: 20; display: flex; align-items: center; justify-content: center;
    background: color-mix(in srgb, var(--bg-solid) 55%, transparent); backdrop-filter: blur(6px); }
  .confirm { width: 420px; padding: 18px; background: var(--elevated); border: 1px solid var(--border); border-radius: var(--radius);
    font-size: 13px; display: flex; flex-direction: column; gap: 8px; }
  .confirm b { font-weight: 600; overflow-wrap: anywhere; }
  .confirm .hint { color: var(--text-3); font-size: 12px; }
  .confirm .error { color: var(--danger); font: 12px var(--mono); }
  .confirm .buttons { display: flex; justify-content: flex-end; gap: 8px; margin-top: 6px; }
  footer { font: 11px var(--mono); color: var(--text-3); padding: 0 12px 8px; letter-spacing: 0.02em; }
</style>
