<script lang="ts">
 import {onMount} from 'svelte'
 import Canvas from './lib/canvas/Canvas.svelte'
 import Sidebar from './lib/Sidebar.svelte'
 import Selection from './lib/Selection.svelte'
 import NewSession from './lib/NewSession.svelte'
 import Spend from './lib/Spend.svelte'
 import Settings from './lib/Settings.svelte'
 import Terminal from './lib/terminal/Terminal.svelte'
 import {request,graphOf,type Workspace,type LiveSession} from './lib/workspace'
 import {applyTheme} from './lib/theme'
 let snapshot=$state<Workspace>({cursor:-1,nodes:[],edges:[],attempts:[]})
 let sessions=$state<LiveSession[]>([])
 let graph=$derived(graphOf(snapshot,sessions)), selection=$state<string[]>([])
 let selected=$derived(selection.length===1?selection[0]:undefined)
 let term=$state<{id:string;title:string;agent:string}>(),creating=$state(false),error=$state('')
 function attach(id:string){const n=snapshot.nodes?.find(x=>x.id===id); const at=snapshot.attempts?.filter(x=>x.node_id===id).sort((a,b)=>b.number-a.number)[0];if(n&&at)term={id:at.id,title:n.name,agent:sessions.find(s=>s.attempt_id===at.id)?.command.split('/').pop()??'harness'}}
 function shortcut(e:KeyboardEvent){if(e.metaKey&&e.key.toLowerCase()==='n'){e.preventDefault();creating=true}else if(e.metaKey&&e.key==='Enter'&&selected)attach(selected)}
 onMount(()=>{let stop=false,timer:number;applyTheme();async function poll(){try{const [next,live]=await Promise.all([request<Workspace>('workspace'),request<LiveSession[]>('sessions')]);if(!stop){snapshot=next;sessions=live;error=''}}catch(e){error=String(e)}if(!stop)timer=setTimeout(poll,1000)}poll();return()=>{stop=true;clearTimeout(timer)}})
</script>
<svelte:window onkeydown={shortcut}/>
<div class="app">{#if error}<div class="banner">The daemon does not answer: {error}. Retrying…</div>{/if}
<header class="top"><span class="brand mono">flows</span><span class="grow"></span><Spend/><Settings/><button class="primary" onclick={()=>creating=true}>New session <span class="kbd">⌘N</span></button></header>
<div class="main"><div class="canvas"><Canvas {graph} bind:selection onopen={attach}/>
{#if !graph.nodes.size}<div class="empty"><h1>{snapshot.cursor<0?'Connecting to Flows…':'Your workspace is empty'}</h1>{#if snapshot.cursor>=0}<button class="primary" onclick={()=>creating=true}>Start a session</button>{/if}</div>{/if}</div>
{#if selected&&graph.nodes.has(selected)}<Sidebar {graph} {selected} onattach={attach}/>
{:else if selection.length>1}<Selection {graph} ids={selection} onpick={id=>selection=[id]}/>{/if}</div>
{#if term}<Terminal sessionId={term.id} title={term.title} agent={term.agent} onclose={()=>term=undefined}/>{/if}
{#if creating}<NewSession started={(id:string)=>{selection=[id];creating=false}} close={()=>creating=false}/>{/if}
<footer>{graph.nodes.size} nodes · ⌘N new session · f fits the view · shift adds · space + drag pans · pinch zooms · double click opens terminal · Esc clears selection</footer></div>
