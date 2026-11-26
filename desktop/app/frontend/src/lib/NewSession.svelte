<script lang="ts">
 import {onMount,tick} from 'svelte'
 import {request} from './workspace'
 let {started,close}:{started:(id:string)=>void;close:()=>void}=$props()
 let scenario=$state('session'), agent=$state('claude'), repo=$state(''), message=$state('')
 let error=$state(''), busy=$state(false), ready=$state(false), history=$state<string[]>([])
 let review=$state<{id:string;version:string;kind:string;status:string}>()
 function show(dialog:HTMLDialogElement){dialog.showModal()}
 let folder:HTMLInputElement
 const id=crypto.randomUUID()
 onMount(async()=>{
  try {
   try {history=JSON.parse(localStorage.getItem('flows.repos')??'[]')}catch{history=[]}
   const config=await request<{default_harness:string;default_directory?:string}>('settings')
   agent=config.default_harness;repo=config.default_directory??''
   ready=true;await tick();folder?.focus()
  }catch(e){error=String(e)}
 })
 async function create(){
  if(busy||!ready)return
  busy=true;error=''
  try {
   if(scenario==='session'){
    const result=await request<{node_id:string}>('newSession',JSON.stringify({request_id:id,harness:agent,work_dir:repo.trim(),prompt:message}))
    if(repo.trim()){
     try{localStorage.setItem('flows.repos',JSON.stringify([repo.trim(),...history.filter(r=>r!==repo.trim())].slice(0,30)))}catch{}
    }
    started(result.node_id)
   }else{
    const body=JSON.stringify({directory:repo.trim()})
    if(!review){review=await request('previewPackage',body);return}
    await request('importPackage',body)
   }
   close()
  }catch(e){error=String(e)}finally{busy=false}
 }
</script>
<dialog use:show oncancel={close} onkeydown={(e)=>{if(e.metaKey&&e.key==='Enter'){e.preventDefault();create()}}}>
 <form onsubmit={(e)=>{e.preventDefault();create()}}>
  <h2>New</h2>
  <label for="scenario">Scenario</label>
  <select id="scenario" bind:value={scenario} onchange={()=>{review=undefined;error=''}}><option value="session">Session · coding harness</option><option value="package">Import a workflow or external tool</option></select>
  <label for="repo">{scenario==='session'?'Repo':'Package directory'}</label>
  <input id="repo" bind:this={folder} bind:value={repo} list="repos" placeholder={scenario==='session'?'A local repo or ~/path; empty for a plain chat':'Directory containing workflow.yaml or tool.yaml'} oninput={()=>review=undefined}/>
  <datalist id="repos">{#each history as path}<option value={path}></option>{/each}</datalist>
  {#if scenario==='session'}
   <p class="hint">{repo.trim()?'Works in this folder.':'A plain chat, in an empty folder of its own.'}</p>
   <label for="agent">Agent</label>
   <select id="agent" bind:value={agent}>{#each ['claude','codex','agy'] as harness}<option value={harness}>{harness}</option>{/each}</select>
   <label for="prompt">First message</label>
   <textarea id="prompt" rows="5" bind:value={message} placeholder="optional"></textarea>
  {:else if review}<p class="hint">{review.kind} · {review.id} · {review.version} · {review.status}</p>{/if}
  {#if error}<p role="alert">{error}</p>{/if}
  <div class="buttons"><small>⌘↩ submits · esc cancels</small><button type="button" onclick={close}>Cancel</button>
   <button class="primary" disabled={busy||!ready||(scenario==='package'&&!repo.trim())}>{busy?'Working…':scenario==='session'?'Create':review?'Import':'Preview'}</button></div>
 </form>
</dialog>
<style>
 dialog{width:min(560px,92vw);margin:12vh auto auto;padding:18px}
 form{margin:0;display:flex;flex-direction:column;gap:6px}
 h2{font:600 15px var(--sans);margin-bottom:4px}
 label,small{font:600 11px var(--mono);color:var(--text-3);letter-spacing:.08em;text-transform:uppercase;margin-top:8px}
 .hint{color:var(--text-3);font-size:12px}
 #agent{align-self:flex-start}
 .buttons{display:flex;align-items:center;justify-content:flex-end;gap:8px;margin-top:10px}
 small{margin:0 auto 0 0}
</style>
