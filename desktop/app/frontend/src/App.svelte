<script lang="ts">
 import {onMount} from 'svelte'
 import Terminal from './lib/Terminal.svelte'
 import Spend from './lib/Spend.svelte'
 import Canvas from './lib/canvas/Canvas.svelte'
 import NewSession from './lib/NewSession.svelte'
 import Controls from './lib/Controls.svelte'
 import Preview from './lib/Preview.svelte'
 import {request,graphOf,type Workspace} from './lib/workspace'
 let snapshot=$state<Workspace>({cursor:-1,nodes:[],edges:[],attempts:[]})
 let graph=$derived(graphOf(snapshot))
 let selection=$state<string[]>([])
 let nodes=$derived((snapshot.nodes??[]).filter(n=>selection.includes(n.id)))
 let node=$derived(nodes.length===1?nodes[0]:undefined)
 let attempts=$derived((snapshot.attempts??[]).filter(a=>a.node_id===node?.id).sort((a,b)=>b.number-a.number))
 let links=$derived((snapshot.edges??[]).filter(e=>e.from_id===node?.id||e.to_id===node?.id))
 let page=$state(''),error=$state(''),file=$state('')
 let terminal=$state<{attempt:string;title:string}>()
 function open(id:string){
  selection=[id]
  const selected=snapshot.nodes?.find(n=>n.id===id)
  if(!selected || !['session','harness'].includes(selected.kind))return
  const latest=snapshot.attempts?.filter(a=>a.node_id===id).sort((a,b)=>b.number-a.number)[0]
  if(latest)terminal={attempt:latest.id,title:selected.name}
 }
 function shortcut(e:KeyboardEvent){
  if(!e.metaKey||page||terminal||document.querySelector('[role="dialog"],dialog[open]'))return
  if(e.key.toLowerCase()==='n'){e.preventDefault();page='New session'}
  else if(e.key==='Enter'&&node){e.preventDefault();open(node.id)}
 }
 onMount(()=>{
  let closed=false,timer:number
  async function poll(){
   try{
    const opened=await window.go.main.App.OpenedFile()
    if(opened&&!closed)file=opened
    const next=await request<Workspace>('workspace')
    if(!closed){
     if(next.cursor!==snapshot.cursor)snapshot=next
     error=''
    }
   }catch(e){if(!closed)error=String(e)}
   if(!closed)timer=window.setTimeout(poll,1500)
  }
  poll()
  return ()=>{closed=true;clearTimeout(timer)}
 })
</script>
<svelte:window onkeydown={shortcut}/>
<div class="app">
 <header class="top"><strong>Flows</strong><nav aria-label="Workspace"><Spend/><button onclick={()=>page='Settings'}>Settings</button><button class="primary" onclick={()=>page='New session'}>New session <small>⌘N</small></button></nav></header>
 {#if file}<div class="notice">Import preview · {file}<button class="quiet" onclick={()=>file=''}>Dismiss</button></div>{/if}
 {#if error}<p class="notice" role="status">Reconnecting to Flows… {error}</p>{/if}
 <main>
  <section class="canvas" aria-label="Workflow canvas">
   <Canvas {graph} bind:selection onopen={open}/>
   {#if !graph.nodes.size}<div class="empty">
    <h1>{snapshot.cursor<0?'Connecting to Flows…':'Your workspace is empty'}</h1>
    {#if snapshot.cursor>=0}<button class="primary" onclick={()=>page='New session'}>Start a session</button>{/if}
   </div>{/if}
  </section>
  {#if nodes.length}
   <aside aria-label="Node inspector">
    <header><span class="label">{node?.kind??'Selection'}</span><button class="quiet" aria-label="Clear selection" onclick={()=>selection=[]}>✕</button></header>
    {#if node}
     <h2>{node.name||node.id}</h2>
     <dl><dt>Status</dt><dd>{node.status.replaceAll('_',' ')}</dd><dt>Run</dt><dd>{node.run_id}</dd>
     {#if node.wait}<dt>Waiting for</dt><dd>{node.wait.kind} {node.wait.ref??''}</dd>{/if}</dl>
     {#if links.length}<h3>Connections</h3>{#each links as link}
      {@const other=link.from_id===node.id?link.to_id:link.from_id}
      <button class="row" onclick={()=>selection=[other]}>{link.from_id===node.id?'→':'←'} {graph.nodes.get(other)?.title??other} {link.milestone??''}</button>
     {/each}{/if}
     {#if attempts.length}<h3>Attempts</h3>{#each attempts as attempt}<div class="row">#{attempt.number} <span>{attempt.status}</span></div>{/each}{/if}
     {#if ['harness','session'].includes(node.kind)&&attempts.length}<button class="primary" onclick={()=>open(node!.id)}>Terminal</button><h3>Screen</h3>{#key attempts[0].id}<Preview attempt={attempts[0].id}/>{/key}{/if}
    {:else}<h2>{nodes.length} nodes selected</h2>{#each nodes as n}<button class="row" onclick={()=>selection=[n.id]}>{n.name}<span>{n.status}</span></button>{/each}{/if}
   </aside>
  {/if}
 </main>
 <footer>{graph.nodes.size} nodes · ⌘N new session · Double-click for terminal · F to fit · Shift-click to select · Space-drag to pan · Pinch to zoom</footer>
</div>
{#if page==='New session'}<NewSession close={()=>page=''} started={(id)=>selection=[id]}/>{:else if page}<Controls {page} close={()=>page=''} started={(id)=>selection=[id]}/>{/if}

{#if terminal}<Terminal {...terminal} close={()=>terminal=undefined}/>{/if}
