<script lang="ts">
 import {onMount} from 'svelte'
 import { Terminal } from '@xterm/xterm'
 import '@xterm/xterm/css/xterm.css'
 import {terminalThemeFor,monoStack,theme} from './theme'
 let {attempt,title,close}:{attempt:string;title:string;close:()=>void}=$props()
 let box:HTMLDivElement, status=$state('Connecting…')
 onMount(()=>{
  const api=window.go.main.App
  const term=new Terminal({fontFamily:monoStack($theme),fontSize:$theme.font_size,theme:terminalThemeFor($theme),cursorStyle:'block',cursorBlink:true,disableStdin:true})
  term.open(box)
  let closed=false, id='', owner=false
  let pending=Promise.resolve()
  const fail=(e:unknown)=>{if(!closed)status=String(e)}
  const observer=new ResizeObserver(resize)
  observer.observe(box)
  function resize(){
   const cell=box.querySelector('.xterm-screen')?.getBoundingClientRect()
   if(!cell?.width||!cell.height)return
   const cols=Math.max(2,Math.floor(box.clientWidth/(cell.width/term.cols)))
   const rows=Math.max(1,Math.floor(box.clientHeight/(cell.height/term.rows)))
   if(term.cols!==cols||term.rows!==rows)term.resize(cols,rows)
   if(owner)api.ResizeTerminal(id,cols,rows).catch(fail)
  }
  term.onData(data=>{
   if(owner)pending=pending.then(()=>api.WriteTerminal(id,data)).catch(fail)
  })
  async function connect(){
   try{
    id=await api.Attach(attempt)
    if(closed){await api.Detach(id);return}
    term.focus()
    while(!closed){
     const frame=JSON.parse(await api.ReadTerminal(id))
     if(closed)break
     const bytes=Uint8Array.from(atob(frame.Payload??''),c=>c.charCodeAt(0))
     if(frame.Kind===1)await new Promise<void>(resolve=>term.write(bytes,resolve))
     if(frame.Kind===5){
      owner=JSON.parse(new TextDecoder().decode(bytes)).owner_id===id
      term.options.disableStdin=!owner
      status=owner?'':'Viewing · input owned by another terminal'
      if(owner)resize()
     }
     if(frame.Kind===6){status='Session ended';break}
    }
   }catch(e){fail(e)}finally{owner=false;if(!closed)term.options.disableStdin=true}
  }
  connect()
  return ()=>{closed=true;observer.disconnect();if(id)api.Detach(id).catch(()=>{});term.dispose()}
 })
 function key(e:KeyboardEvent){if(e.metaKey&&e.key==='w'){e.preventDefault();close()}}
</script>
<svelte:window onkeydown={key}/>
<div class="overlay" role="dialog" aria-label={title}>
 <div class="window"><header><span>{title}</span><em>{status}</em><button class="quiet" onclick={close} title="Detach terminal">⌘W ✕</button></header>
 <div class="box" bind:this={box}></div></div>
</div>
<style>
 .overlay{position:fixed;inset:0;background:color-mix(in srgb,var(--bg-solid) 55%,transparent);backdrop-filter:blur(6px);display:grid;place-items:center;z-index:20}
 .window{width:88vw;height:84vh;display:flex;flex-direction:column;overflow:hidden;background:color-mix(in srgb,var(--bg-solid) 90%,transparent);backdrop-filter:blur(30px);border:1px solid var(--border);border-radius:var(--radius)}
 header{display:flex;align-items:center;gap:12px;height:36px;padding:0 6px 0 14px;border-bottom:1px solid var(--border-subtle)}
 header span{flex:1;text-transform:uppercase;letter-spacing:.08em;font:600 11px var(--mono)}
 em{font:11px var(--mono);color:var(--text-3)}
 .box{flex:1;min-height:0;overflow:hidden;margin:8px}
</style>
