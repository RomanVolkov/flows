<script lang="ts">
 import {onMount,tick} from 'svelte'
 import {request} from './workspace'
 import authoringPrompt from './prompts/create-flow.md?raw'
 let {started,close}:{started:(id:string)=>void;close:()=>void}=$props()
 let scenario=$state('session'), agent=$state('claude'), repo=$state(''), message=$state('')
 let authoringType=$state<'workflow'|'tool'>('workflow'), idea=$state('')
 let error=$state(''), busy=$state(false), ready=$state(false), history=$state<string[]>([])
 let harnessesLoading=$state(true)
 let review=$state<{id:string;version:string;kind:string;status:string;valid:boolean;diagnostics:{file:string;line:number;column:number;message:string;severity:string}[]}>()
 let harnesses=$state<{id:string;version:string}[]>([])
 const harnessCacheKey='flows.available-harnesses.v1'
 let folder=$state<HTMLInputElement>()
 let firstMessage=$state<HTMLTextAreaElement>()
 let listOpen=$state(false), pick=$state(-1)
 const matches=$derived(history.filter(path=>path.toLowerCase().includes(repo.trim().toLowerCase())))
 function choose(path:string){repo=path;listOpen=false;pick=-1}
 function repoKey(e: KeyboardEvent) {
    if (!listOpen || matches.length === 0) return
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault()
      const n = matches.length
      pick = e.key === 'ArrowDown' ? (pick + 1) % n : (pick - 1 + n) % n
    } else if (e.key === 'Enter' && pick >= 0) {
      e.preventDefault()
      choose(matches[pick])
    }
  }

 const id=crypto.randomUUID()
 function changeScenario(next:string){scenario=next;review=undefined;error=''}
 onMount(()=>{
  let active=true, preferred='claude'
  try {history=JSON.parse(localStorage.getItem('flows.repos')??'[]')}catch{history=[]}
  try {
   const cached=JSON.parse(localStorage.getItem(harnessCacheKey)??'null') as {id:string;version:string}[]|null
   if(Array.isArray(cached)&&cached.every(x=>typeof x?.id==='string'&&typeof x?.version==='string'))harnesses=cached
  }catch{}
  const chooseAgent=()=>{if(harnesses.some(x=>x.id===preferred))agent=preferred;else if(!harnesses.some(x=>x.id===agent))agent=harnesses[0]?.id??''}
  void request<{id:string;version:string}[]>('harnesses').then(detected=>{
   if(!active)return
   harnesses=detected
   try{localStorage.setItem(harnessCacheKey,JSON.stringify(detected))}catch{}
   harnessesLoading=false;chooseAgent()
  }).catch(e=>{if(active){harnessesLoading=false;if(!harnesses.length)error=String(e)}})
  const focusInitial = () => {
   const target = scenario === 'session' ? (firstMessage ?? folder) : folder
   const dialog = target?.closest('.dialog')
   if (target && (!dialog || !dialog.contains(document.activeElement))) target.focus()
  }
  void tick().then(() => { if (active) focusInitial() })
  void request<{default_harness:string;default_directory?:string}>('settings').then(async config=>{
   if(!active)return
   preferred=config.default_harness;repo=config.default_directory??'';chooseAgent();ready=true;await tick();focusInitial()
  }).catch(e=>{if(active)error=String(e)})
  return()=>{active=false}
 })
 async function create(){
  if(busy||!ready)return
  busy=true;error=''
  try {
   if(scenario==='session'||scenario==='authoring'){
    const prompt=scenario==='authoring'?`${authoringPrompt}\n\nThe user selected: ${authoringType}.\nTheir main idea: ${idea.trim()}`:message
    const result=await request<{node_id:string}>('newSession',JSON.stringify({request_id:id,harness:agent,work_dir:repo.trim(),prompt}))
    if(repo.trim()){
     try{localStorage.setItem('flows.repos',JSON.stringify([repo.trim(),...history.filter(r=>r!==repo.trim())].slice(0,30)))}catch{}
    }
    started(result.node_id)
   }else{
    const body=JSON.stringify({directory:repo.trim()})
    if(!review||!review.valid){
     const validation=await request<{valid:boolean;kind:string;id:string;diagnostics:{file:string;line:number;column:number;message:string;severity:string}[]}>('validatePackage',body)
     if(!validation.valid){review={...validation,version:'',status:'invalid'};return}
     const preview=await request<{version:string;status:string}>('previewPackage',body)
     review={...validation,...preview};return
    }
    await request('importPackage',body)
   }
   close()
  }catch(e){error=String(e)}finally{busy=false}
 }
</script>
<div class="overlay" role="dialog" aria-modal="true" aria-label="New session" tabindex="-1" onclick={e=>{if(e.target===e.currentTarget)close()}} onkeydown={(e)=>{if(e.key==='Escape'){e.preventDefault();if(listOpen && matches.length)listOpen=false;else close()}else if(e.metaKey&&e.key==='Enter'){e.preventDefault();e.currentTarget.querySelector<HTMLButtonElement>('button.primary')?.click()}}}>
 <form class="dialog" onsubmit={(e)=>{e.preventDefault();create()}}>
  <h2>New</h2>
  <label for="scenario">Scenario</label>
  <select id="scenario" value={scenario} onchange={e=>changeScenario(e.currentTarget.value)}><option value="session">Session · coding harness</option><option value="authoring">Create a workflow or tool</option><option value="package">Import a workflow or external tool</option></select>
  {#if scenario==='authoring'}
   <fieldset class="choice-set"><legend>What are you creating?</legend>
    <div class="kind-choice" role="group" aria-label="What are you creating?"><button type="button" class:selected={authoringType==='workflow'} aria-pressed={authoringType==='workflow'} onclick={()=>authoringType='workflow'}>Workflow</button><button type="button" class:selected={authoringType==='tool'} aria-pressed={authoringType==='tool'} onclick={()=>authoringType='tool'}>Tool</button></div>
   </fieldset>
   <label for="idea">What’s the main idea?</label>
   <textarea id="idea" rows="3" bind:value={idea} placeholder="Describe what you want it to do, in your own words"></textarea>
   <p class="hint">Answer one question at a time, then review {authoringType==='workflow'?'the tool and agent nodes':'the operation contract'} before implementation.</p>
  {/if}
  {#if scenario==='session'||scenario==='authoring'}
   <label for="repo">{scenario==='authoring'?'Working folder':'Repo'}</label>
   <div class="combo">
      <input id="repo" bind:this={folder} bind:value={repo} autocomplete="off" spellcheck="false"
        placeholder="A local repo or ~/path; empty for a plain chat"
        onfocus={() => (listOpen = true)} oninput={() => { listOpen = true; pick = -1 }} onkeydown={repoKey}
        onblur={() => setTimeout(() => (listOpen = false), 120)} />
      {#if listOpen && matches.length}
        <ul class="list" role="listbox">
          {#each matches as r, i}
            <li role="option" aria-selected={i === pick} class:on={i === pick}
              onmousedown={(e) => { e.preventDefault(); choose(r) }}>
              <span class="mono">{r}</span>
              <span class="kind">folder</span>
            </li>
          {/each}
        </ul>
      {/if}
   </div>
   {#if scenario==='session'}<p class="hint">{repo.trim()?'Works in this folder.':'A plain chat, in an empty folder of its own.'}</p>{/if}
   <label for="agent">Agent</label>
   <select id="agent" bind:value={agent}>{#if !harnesses.length}<option value="">{harnessesLoading?'Checking installed agents…':'No supported agents found'}</option>{/if}{#each harnesses as harness}<option value={harness.id}>{harness.id}</option>{/each}</select>
   {#if harnessesLoading&&harnesses.length}<p class="hint refresh">Refreshing agent list…</p>{/if}
   {#if scenario==='session'}<label for="prompt">First message</label><textarea id="prompt" bind:this={firstMessage} rows="5" bind:value={message} placeholder="optional"></textarea>{/if}
  {:else}
   <label for="repo">Package directory</label>
   <input id="repo" bind:this={folder} bind:value={repo} list="repos" placeholder="Directory containing workflow.yaml or tool.yaml" oninput={()=>review=undefined}/>
   <datalist id="repos">{#each history as path}<option value={path}></option>{/each}</datalist>
   {#if review}<p class:valid={review.valid} class:invalid={!review.valid} class="hint">{review.valid?'Valid':'Needs fixes'} · {review.kind} · {review.id}{#if review.version} · {review.version} · {review.status}{/if}</p>
   {#if review.diagnostics?.length}<ul class="diagnostics">{#each review.diagnostics as diagnostic}<li><code>{diagnostic.file}:{diagnostic.line}:{diagnostic.column}</code> {diagnostic.message}</li>{/each}</ul>{/if}{/if}
  {/if}
  {#if error}<p role="alert">{error}</p>{/if}
  <div class="buttons"><small>⌘↩ submits · esc cancels</small><button type="button" onclick={close}>Cancel</button>
   <button class="primary" disabled={busy||!ready||((scenario==='session'||scenario==='authoring')&&(!harnesses.length||!harnesses.some(x=>x.id===agent)))||(scenario==='authoring'&&!idea.trim())||(scenario==='package'&&!repo.trim())}>{busy?'Working…':scenario==='package'?review?.valid?'Import':'Validate':'Create'}</button></div>
 </form>
</div>
<style>
 .overlay{position:fixed;inset:0;z-index:20;display:flex;align-items:flex-start;justify-content:center;padding:12vh 16px 16px;background:color-mix(in srgb,var(--bg-solid) 55%,transparent);backdrop-filter:blur(6px)}
 .dialog{width:min(560px,100%);max-height:calc(88vh - 16px);overflow:auto;margin:0;padding:18px;background:var(--elevated);border:1px solid var(--border);border-radius:var(--radius);display:flex;flex-direction:column;gap:6px;font-size:13px}
 h2{font:600 15px var(--sans);margin-bottom:4px}
 label,small{font:600 11px var(--mono);color:var(--text-3);letter-spacing:.08em;text-transform:uppercase;margin-top:8px}
 .hint{color:var(--text-3);font-size:12px}
 .choice-set{border:0;margin:0;padding:0;min-width:0}.choice-set legend{font:600 11px var(--mono);color:var(--text-3);letter-spacing:.08em;text-transform:uppercase;margin:8px 0}.kind-choice{display:flex;gap:4px}.kind-choice button{height:30px;flex:1;justify-content:center}.kind-choice button.selected{color:var(--accent);border-color:color-mix(in srgb,var(--accent) 55%,var(--border));background:var(--accent-glow)}
 .combo { position: relative; }
  .combo input { width: 100%; }
  .list { position: absolute; left: 0; right: 0; top: calc(100% + 4px); z-index: 1; list-style: none; padding: 4px;
    background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius); max-height: 260px; overflow: auto; }
  .list li { display: flex; justify-content: space-between; gap: 12px; padding: 5px 8px; border-radius: 4px; cursor: pointer; }
  .list li:hover, .list li.on { background: color-mix(in srgb, var(--accent) 16%, transparent); }
  .list .mono { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 12px; }
  .list .kind { color: var(--text-3); font-size: 11px; white-space: nowrap; }
 .refresh{margin:0;font-size:11px}
 .diagnostics{margin:2px 0;padding-left:22px;color:var(--danger);font:11px/1.5 var(--mono);max-height:150px;overflow:auto}.diagnostics code{color:var(--text-3)}.valid{color:var(--green)}.invalid{color:var(--danger)}
 #agent{align-self:flex-start}
 .buttons{display:flex;align-items:center;justify-content:flex-end;gap:8px;margin-top:10px}
 small{margin:0 auto 0 0}
</style>
