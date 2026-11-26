<script lang="ts">
  import { onMount } from 'svelte'
  import { Terminal } from '@xterm/xterm'
  import '@xterm/xterm/css/xterm.css'
  import { terminalTheme } from './theme'
  import { request } from './workspace'
  let {attempt}:{attempt:string}=$props()
  let container:HTMLDivElement
  let message=$state('Loading preview…')
  onMount(()=>{
    const term=new Terminal({cols:80,rows:24,fontSize:11,fontFamily:'Menlo, monospace',theme:terminalTheme,disableStdin:true,scrollback:500})
    term.open(container)
    let closed=false, timer:number, last=''
    async function refresh(){
      try {
        const {screen}=await request<{screen:string|null}>('preview',attempt)
        if(closed)return
        message=screen?'':'No session output yet.'
        if(screen && screen!==last){
          last=screen
          term.reset()
          term.write(Uint8Array.from(atob(screen),c=>c.charCodeAt(0)))
        }
      } catch(e){if(!closed)message=String(e)}
      if(!closed)timer=window.setTimeout(refresh,1500)
    }
    refresh()
    return ()=>{closed=true;clearTimeout(timer);term.dispose()}
  })
</script>
{#if message}<p>{message}</p>{/if}
<div class="screen" bind:this={container}></div>
<style>.screen{overflow:auto;background:var(--bg);padding:6px}.screen :global(.xterm){width:max-content}</style>
