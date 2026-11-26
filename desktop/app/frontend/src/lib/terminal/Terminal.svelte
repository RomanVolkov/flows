<script lang="ts">
 import {onMount} from 'svelte'
 import {Terminal as XTerminal} from '@xterm/xterm'
 import '@xterm/xterm/css/xterm.css'
 import {terminalTheme,monoStack,theme} from '../theme'
 let {sessionId,title,agent='',onclose}:{sessionId:string;title:string;agent?:string;onclose:()=>void}=$props()
 let box:HTMLDivElement,status=$state('Connecting…')
 onMount(()=>{
  const api=window.go.main.App
  const term=new XTerminal({fontFamily:monoStack($theme),fontSize:$theme.font_size,theme:terminalTheme,scrollback:5000,allowProposedApi:true})
  term.open(box)
  let stopped=false,id='',owner=false,pending=Promise.resolve()
  const fail=(e:unknown)=>{if(!stopped)status=String(e)}
  const resize=()=>{
   const screen=box.querySelector('.xterm-screen') as HTMLElement|null
   if(screen&&screen.clientWidth&&screen.clientHeight){
    const cols=Math.max(2,Math.floor(box.clientWidth/(screen.clientWidth/term.cols)))
    const rows=Math.max(1,Math.floor(box.clientHeight/(screen.clientHeight/term.rows)))
    if(cols!==term.cols||rows!==term.rows)term.resize(cols,rows)
   }
   if(owner&&id)api.ResizeTerminal(id,term.cols,term.rows).catch(fail)
  }
  const observer=new ResizeObserver(resize)
  observer.observe(box)
  term.onData(data=>{if(owner)pending=pending.then(()=>api.WriteTerminal(id,data)).catch(fail)})
  term.onResize(({cols,rows})=>{if(owner&&id)api.ResizeTerminal(id,cols,rows).catch(fail)})
  async function connect(){
   try{
    id=await api.Attach(sessionId)
    while(!stopped){
     const frame=JSON.parse(await api.ReadTerminal(id)) as {kind:number;payload:string}
     const bytes=Uint8Array.from(atob(frame.payload??''),c=>c.charCodeAt(0))
     if(frame.kind===1)await new Promise<void>(resolve=>term.write(bytes,resolve))
     else if(frame.kind===5){owner=JSON.parse(new TextDecoder().decode(bytes)).owner_id===id;term.options.disableStdin=!owner;status=owner?'':`Viewing · input owned by another terminal · ${agent}`;if(owner)resize()}
     else if(frame.kind===6){status='Session ended';break}
    }
   }catch(e){fail(e)}finally{owner=false;term.options.disableStdin=true}
  }
  connect()
  return()=>{stopped=true;observer.disconnect();if(id)api.Detach(id).catch(()=>{});term.dispose()}
 })
 function key(e:KeyboardEvent){if(e.metaKey&&e.key==='w'){e.preventDefault();onclose()}}
</script>
<svelte:window onkeydown={key}/>
<div class="overlay" role="dialog" aria-label={title}><div class="window"><header><span>{title}</span><em>{status}</em><button class="quiet" onclick={onclose} title="Detach terminal">⌘W ✕</button></header><div class="box" bind:this={box}></div></div></div>
<style>
 .overlay{position:fixed;inset:0;background:color-mix(in srgb,var(--bg-solid) 55%,transparent);backdrop-filter:blur(6px);display:grid;place-items:center;z-index:20}
 .window{width:88vw;height:84vh;display:flex;flex-direction:column;overflow:hidden;background:var(--bg);border:1px solid var(--border);border-radius:var(--radius)}
 header{display:flex;align-items:center;gap:12px;height:36px;padding:0 6px 0 14px;border-bottom:1px solid var(--border-subtle)}header span{flex:1;text-transform:uppercase;letter-spacing:.08em;font:600 11px var(--mono)}header em{font:11px var(--mono);color:var(--text-3)}
 .box{flex:1;min-height:0;overflow:hidden;margin:8px}.box :global(.xterm){height:100%}.box :global(.xterm-viewport){overflow-y:auto!important}
</style>
