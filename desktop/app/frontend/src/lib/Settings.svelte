<script lang="ts">
 import {request, type LibraryEntry, type LibrarySnapshot} from './workspace'
 import {configureTerminal} from './theme'
 import WorkflowInputs from './WorkflowInputs.svelte'
 import {coerceWorkflowInputs} from './workflow'
 let {open=$bindable(false)}=$props()
 let value=$state<any>(), available=$state<{id:string;version:string}[]>([])
 let entries=$state<LibraryEntry[]>([])
 const packageIssues=$derived(entries.filter(entry=>entry.status==='invalid'||entry.status==='preparing'))
 let libraryRefresh=0
 let error=$state(''), busy=$state(false)
 let section=$state<'general'|'schedules'>('general')
 let workflows=$state<any[]>([]), schedules=$state<any[]>([]), scheduleWorkflow=$state(''), scheduleCron=$state('0 9 * * 1-5'), scheduleTimezone=$state('Local'), scheduleValues=$state<Record<string,unknown>>({})
 const scheduleDefinition=$derived(workflows.find(w=>w.id===scheduleWorkflow))
 async function refreshLibrary(){
  const refresh=++libraryRefresh
  try{
   const library=await request<LibrarySnapshot>('library')
   if(refresh===libraryRefresh)entries=[...(library.entries??[]),...(library.malformed??[])]
  }catch(e){if(refresh===libraryRefresh)error=String(e)}
 }
 async function load(){
  try{
   const [settings,harnesses,workflowResult,scheduleResult]=await Promise.all([
    request<any>('settings'),request<{id:string;version:string}[]>('harnesses'),request<{workflows:any[]}>('workflows'),request<any[]>('schedules'),
   ])
   value=settings;available=harnesses;workflows=workflowResult.workflows??[];schedules=scheduleResult??[]
   if(!scheduleWorkflow&&workflows.length)chooseScheduleWorkflow(workflows[0].id)
   await refreshLibrary()
   if(value.terminal.font_family==='Iosevka Mono')value.terminal.font_family='IosevkaTerm Nerd Font Mono'
   configureTerminal(value.terminal);error=''
  }catch(e){error=String(e)}
 }
 async function selectHarness(id:string){if(!value||busy||value.default_harness===id)return;busy=true;error='';try{value=await request('saveSettings',JSON.stringify({expected_revision:value.revision,settings:{...value,default_harness:id}}));configureTerminal(value.terminal)}catch(e){error=String(e)}finally{busy=false}}
 function previewOpacity(){if(value)configureTerminal(value.terminal)}
 async function saveOpacity(){if(!value||busy)return;busy=true;error='';try{value=await request('saveSettings',JSON.stringify({expected_revision:value.revision,settings:value}));configureTerminal(value.terminal)}catch(e){error=String(e);void load()}finally{busy=false}}
 function chooseScheduleWorkflow(id:string){scheduleWorkflow=id;scheduleValues=Object.fromEntries((workflows.find(w=>w.id===id)?.form.fields??[]).map((field:any)=>[field.name,field.default]))}
 async function addSchedule(){if(!scheduleDefinition||busy)return;const result=coerceWorkflowInputs(scheduleDefinition.form.fields,scheduleValues);if(Object.keys(result.errors).length){error=Object.entries(result.errors).map(([k,v])=>`${k}: ${v}`).join(' · ');return}busy=true;error='';try{await request('putSchedule',JSON.stringify({workflow_id:scheduleWorkflow,cron:scheduleCron,timezone:scheduleTimezone,input_bindings:result.inputs,enabled:true}));schedules=await request<any[]>('schedules')}catch(e){error=String(e)}finally{busy=false}}
 async function removeSchedule(id:string){if(busy)return;busy=true;error='';try{await request('deleteSchedule',id);schedules=await request<any[]>('schedules')}catch(e){error=String(e)}finally{busy=false}}
 $effect(() => {
   if (!open) return
   void load()
   const timer = window.setInterval(refreshLibrary, 2500)
   return () => {clearInterval(timer);libraryRefresh++}
 })
 function close(){open=false}
 function toggle(){open=!open}
 function key(e:KeyboardEvent){if(open&&e.key==='Escape')close()}
 async function openFolder(kind:string){try{await window.go.main.App.OpenLibrary(kind)}catch(e){error=String(e)}}
 const count=(kind:string)=>entries.filter(x=>x.kind===kind).length
</script>
<svelte:window onkeydown={key}/>
<div class="settings"><button class="quiet gear" onclick={toggle} title="Settings" aria-label="Settings"><svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1Z"/></svg></button>
{#if open}<button class="scrim" aria-label="Close settings" onclick={close}></button><div class="pop" role="dialog" aria-modal="true" aria-label="Settings" tabindex="-1">
 <header class="modal-head"><div><h2>Settings</h2><p>Configure Flows for this machine.</p></div><button class="close" aria-label="Close settings" onclick={close}>×</button></header>
 <div class="modal-body"><nav class="sections" aria-label="Settings sections">
  <button class:active={section==='general'} onclick={()=>section='general'}><strong>General</strong><small>Harness, terminal, library</small></button>
  <button class:active={section==='schedules'} onclick={()=>section='schedules'}><strong>Scheduled workflows</strong><small>Recurring runs and inputs</small></button>
 </nav><main class="settings-content">
 {#if section==='general'}
  {#if value}
   <section><h3>Harnesses</h3><p class="hint">Available harnesses are detected from this machine.</p>
   {#if available.length}<div class="tabs">{#each available as item}<button class:on={item.id===value.default_harness} disabled={busy} title={item.version} onclick={()=>selectHarness(item.id)}>{item.id}</button>{/each}</div>{:else}<p class="hint">No supported harnesses were found.</p>{/if}</section>
   <section><h3>Terminal</h3><label class="opacity">Background opacity <span class="mono">{Math.round(value.terminal.opacity*100)}%</span><input type="range" min="0.45" max="1" step="0.05" bind:value={value.terminal.opacity} disabled={busy} oninput={previewOpacity} onchange={saveOpacity}/></label></section>
   <section><h3>Workflows and tools</h3><p class="hint">Changes in these folders are picked up without restarting Flows.</p>
   <div class="library-row"><span>Workflows <span class="count mono">{count('workflow')}</span></span><button class="folder" onclick={()=>openFolder('workflows')}>Open folder</button></div>
   <div class="library-row"><span>Tools <span class="count mono">{count('tool')}</span></span><button class="folder" onclick={()=>openFolder('tools')}>Open folder</button></div>
   {#if packageIssues.length}<ul class="package-issues" aria-label="Package preparation">{#each packageIssues as entry (entry.path)}<li><span class="mono">{entry.id}</span> <span class="hint">{entry.kind} · {entry.status==='preparing'?'Preparing…':'Invalid'}</span>{#if entry.diagnostics?.length}<ul>{#each entry.diagnostics as diagnostic}<li class="error">{diagnostic.message}</li>{/each}</ul>{/if}</li>{/each}</ul>{/if}</section>
  {/if}
 {:else}
  <section><h3>Scheduled workflows</h3><p class="hint">Run a workflow on a cron schedule with saved input values.</p>
   {#if workflows.length}
    <label class="schedule-field">Workflow<select value={scheduleWorkflow} onchange={e=>chooseScheduleWorkflow(e.currentTarget.value)}>{#each workflows as flow (flow.id)}<option value={flow.id}>{flow.id}</option>{/each}</select></label>
    {#if scheduleDefinition}<WorkflowInputs fields={scheduleDefinition.form.fields} values={scheduleValues} errors={{}} history={[]} onchange={(name,value)=>{scheduleValues[name]=value}}/>{/if}
    <div class="schedule-row"><label>Cron<input class="mono" bind:value={scheduleCron} placeholder="0 9 * * 1-5"/></label><label>Time zone<input bind:value={scheduleTimezone} placeholder="Local or Europe/Berlin"/></label></div>
    <button class="schedule-add" disabled={busy} onclick={addSchedule}>Add schedule</button>
   {:else}<p class="hint">Import a valid workflow to schedule it.</p>{/if}
  </section>
  <section><h3>Active schedules</h3>{#if schedules.length}{#each schedules as item (item.schedule.id)}<div class="schedule-existing"><span><strong>{item.schedule.workflow_id}</strong><small>{item.view.plain_language} · next {item.view.next_tick_utc??'unknown'} · {item.schedule.enabled?'enabled':'disabled'}</small></span><button class="folder" disabled={busy} onclick={()=>removeSchedule(item.schedule.id)}>Remove</button></div>{/each}{:else}<p class="hint">No schedules yet.</p>{/if}</section>
 {/if}
 {#if error}<p class="error mono">{error}</p>{/if}
 </main></div>
</div>{/if}</div>
<style>

  .settings { position: relative; --wails-draggable: no-drag; }
  .gear { width: 30px; padding: 0; justify-content: center; }
  .scrim { position: fixed; inset: 0; z-index: 29; height: auto; padding: 0; border: none; background: #08090c99; backdrop-filter: blur(3px); cursor: default; }
  .scrim:hover { background: #08090c99; }
  .pop { position: fixed; left: 50%; top: 50%; transform: translate(-50%,-50%); z-index: 30; width: min(820px,calc(100vw - 40px)); height: min(690px,calc(100vh - 40px)); overflow: hidden;
    background: var(--elevated); border: 1px solid var(--border); border-radius: var(--radius); font-size: 13px; }
.modal-head{height:64px;display:flex;align-items:center;justify-content:space-between;padding:0 20px;border-bottom:1px solid var(--border)}.modal-head h2{margin:0;font-size:16px}.modal-head p{margin:3px 0 0;color:var(--text-3);font-size:12px}.close{width:28px;height:28px;padding:0;justify-content:center;font-size:19px}
.modal-body{height:calc(100% - 64px);display:grid;grid-template-columns:205px 1fr;min-height:0}.sections{display:flex;flex-direction:column;gap:4px;padding:12px;border-right:1px solid var(--border);background:color-mix(in srgb,var(--surface) 45%,transparent)}.sections button{display:grid;gap:4px;padding:10px;text-align:left;background:transparent;border-color:transparent}.sections button.active{background:var(--surface);border-color:var(--border);color:var(--accent)}.sections small{font-size:11px;color:var(--text-3)}.settings-content{padding:20px 24px;overflow:auto}.settings-content section+section{margin-top:26px;padding-top:20px;border-top:1px solid var(--border)}.settings-content section h3{margin:0 0 7px;font-size:14px}
.hint{color:var(--text-3);font-size:12px;margin:4px 0 10px}.count{color:var(--text-3)}
.tabs{display:flex;gap:4px;margin-top:8px}.tabs button{height:26px;padding:0 10px;flex:1;justify-content:center;white-space:nowrap;text-transform:none;letter-spacing:0}.tabs button.on{color:var(--accent);border-color:color-mix(in srgb,var(--accent) 55%,var(--border))}
.library-row{display:flex;align-items:center;justify-content:space-between;padding:5px 0;color:var(--text-2)}.count{margin-left:6px}.folder{font-size:11px;padding:4px 8px}.error{color:var(--danger);overflow-wrap:anywhere}
.package-issues{padding-left:18px;max-height:220px;overflow:auto}.package-issues ul{padding-left:16px}.package-issues li{margin:5px 0}
.opacity{display:grid;grid-template-columns:1fr auto;gap:8px;align-items:center;margin-top:8px;color:var(--text-2);font-size:12px}.opacity input{grid-column:1/-1;width:100%;accent-color:var(--accent)}
.schedule-field{display:grid;gap:5px;margin:8px 0;color:var(--text-2);font-size:12px}.schedule-field select,.schedule-row input{height:28px;padding:0 7px;border:1px solid var(--border);border-radius:5px;background:var(--surface);color:var(--text);font:inherit}.schedule-row{display:grid;grid-template-columns:1fr 1fr;gap:7px;margin-top:8px}.schedule-row label{display:grid;gap:5px;color:var(--text-2);font-size:12px}.schedule-add{margin-top:8px}.schedule-existing{display:flex;justify-content:space-between;align-items:center;padding:10px 0;border-top:1px solid var(--border)}.schedule-existing span{display:grid;gap:4px}.schedule-existing small{color:var(--text-3);font-size:11px}
@media(max-width:620px){.pop{width:calc(100vw - 20px);height:calc(100vh - 20px)}.modal-body{grid-template-columns:145px 1fr}.sections{padding:7px}.sections button{padding:8px}.settings-content{padding:14px}.schedule-row{grid-template-columns:1fr}}
</style>
