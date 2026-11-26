<script lang="ts">
 import { onMount } from 'svelte'
 import { request } from './workspace'
 let {page,close,started}:{page:string;close:()=>void;started:(id:string)=>void}=$props()
 interface Settings {revision:number;default_harness:string;default_directory?:string;concurrency:number}
 interface Usage {requests:number;output_tokens:number;uncached_input_tokens:number;cache_read_tokens:number;coverage:string}
 let dialog:HTMLDialogElement
 let settings=$state<Settings>(), usage=$state<Usage>(), error=$state(''), busy=$state(false)
 let harness=$state('claude'), directory=$state(''), prompt=$state('')
 const requestID=crypto.randomUUID()
 onMount(()=>{
  dialog.showModal()
  async function load(){
   try{
    if(page==='Analytics') usage=await request<Usage>('usage')
    else {settings=await request<Settings>('settings'); harness=settings.default_harness; directory=settings.default_directory??''}
   } catch(e){error=String(e)}
  }
  load()
 })
 async function submit(e:SubmitEvent){
  e.preventDefault()
  if(!settings||busy)return
  busy=true;error=''
  try{
   if(page==='Settings') await request('saveSettings',JSON.stringify({expected_revision:settings.revision,settings:{...settings,default_harness:harness,default_directory:directory}}))
   else {
    const result=await request<{node_id:string}>('newSession',JSON.stringify({request_id:requestID,harness,work_dir:directory,prompt}))
    started(result.node_id)
   }
   close()
  }catch(e){error=String(e)}finally{busy=false}
 }
</script>
<dialog bind:this={dialog} oncancel={close}>
 <header><h2>{page}</h2><button class="quiet" aria-label="Close" onclick={close}>✕</button></header>
 {#if error}<p role="alert">{error}</p>{/if}
 {#if usage}
  <dl>{#each [['Requests',usage.requests],['Input tokens (uncached)',usage.uncached_input_tokens],['Cached input tokens',usage.cache_read_tokens],['Output tokens',usage.output_tokens],['Coverage',usage.coverage]] as [label,val]}
   <dt>{label}</dt><dd>{val.toLocaleString()}</dd>{/each}</dl>
 {:else if settings}
  <form onsubmit={submit}>
   <label>Harness<select bind:value={harness}><option value="claude">Claude</option><option value="codex">Codex</option><option value="agy">Agy</option></select></label>
   <label>Working directory<input bind:value={directory} placeholder="Default directory" /></label>
   {#if page==='Settings'}<label>Concurrency<input type="number" min="1" required bind:value={settings.concurrency} /></label>
   {:else}<label>Prompt<textarea rows="4" bind:value={prompt}></textarea></label>{/if}
   <button class="primary" disabled={busy}>{page==='Settings'?'Save settings':'Start session'}</button>
  </form>
 {:else if !error}<p>Loading…</p>{/if}
</dialog>
