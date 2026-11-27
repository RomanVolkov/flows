<script lang="ts">
 import {request} from './workspace'
 import {configureTerminal} from './theme'
 let {open=$bindable(false)}=$props()
 let value=$state<any>(), available=$state<{id:string;version:string}[]>([])
 let entries=$state<{kind:string;id:string;version:string;path:string}[]>([])
 let error=$state(''), busy=$state(false)
 async function refreshLibrary(){try{const library=await request<{entries:{kind:string;id:string;version:string;path:string}[]}>('library');entries=library.entries??[]}catch(e){error=String(e)}}
 async function load(){
  try{
   const [settings,harnesses]=await Promise.all([
    request<any>('settings'),request<{id:string;version:string}[]>('harnesses'),
   ])
   value=settings;available=harnesses;await refreshLibrary()
   if(value.terminal.font_family==='Iosevka Mono')value.terminal.font_family='IosevkaTerm Nerd Font Mono'
   configureTerminal(value.terminal);error=''
  }catch(e){error=String(e)}
 }
 async function selectHarness(id:string){if(!value||busy||value.default_harness===id)return;busy=true;error='';try{value=await request('saveSettings',JSON.stringify({expected_revision:value.revision,settings:{...value,default_harness:id}}));configureTerminal(value.terminal)}catch(e){error=String(e)}finally{busy=false}}
 function previewOpacity(){if(value)configureTerminal(value.terminal)}
 async function saveOpacity(){if(!value||busy)return;busy=true;error='';try{value=await request('saveSettings',JSON.stringify({expected_revision:value.revision,settings:value}));configureTerminal(value.terminal)}catch(e){error=String(e);void load()}finally{busy=false}}
 $effect(() => {
   if (!open) return
   void load()
   const timer = window.setInterval(refreshLibrary, 2500)
   return () => clearInterval(timer)
 })
 function close(){open=false}
 function toggle(){open=!open}
 function key(e:KeyboardEvent){if(open&&e.key==='Escape')close()}
 async function openFolder(kind:string){try{await window.go.main.App.OpenLibrary(kind)}catch(e){error=String(e)}}
 const count=(kind:string)=>entries.filter(x=>x.kind===kind).length
</script>
<svelte:window onkeydown={key}/>
<div class="settings"><button class="quiet gear" onclick={toggle} title="Settings" aria-label="Settings">⚙</button>
{#if open}<button class="scrim" aria-label="Close settings" onclick={close}></button><div class="pop" role="dialog" aria-modal="true" aria-label="Settings" tabindex="-1">
 <h3 class="label">Harnesses</h3><p class="hint">Available harnesses are detected from this machine.</p>
 {#if value}{#if available.length}<div class="tabs">{#each available as item}<button class:on={item.id===value.default_harness} disabled={busy} title={item.version} onclick={()=>selectHarness(item.id)}>{item.id}</button>{/each}</div>{:else}<p class="hint">No supported harnesses were found.</p>{/if}
 <h3 class="label next">Terminal</h3><label class="opacity">Background opacity <span class="mono">{Math.round(value.terminal.opacity*100)}%</span><input type="range" min="0.45" max="1" step="0.05" bind:value={value.terminal.opacity} disabled={busy} oninput={previewOpacity} onchange={saveOpacity}/></label>
 <h3 class="label next">Workflows and tools</h3><p class="hint">Changes in these folders are picked up by the library scan without restarting Flows.</p>
 <div class="library-row"><span>Workflows <span class="count mono">{count('workflow')}</span></span><button class="folder" onclick={()=>openFolder('workflows')}>Open folder</button></div>
 <div class="library-row"><span>Tools <span class="count mono">{count('tool')}</span></span><button class="folder" onclick={()=>openFolder('tools')}>Open folder</button></div>
 {/if}
 {#if error}<p class="error mono">{error}</p>{/if}
</div>{/if}</div>
<style>

  .settings { position: relative; --wails-draggable: no-drag; }
  .gear { font-size: 18px; line-height: 1; padding: 0 6px; }
  .scrim { position: fixed; inset: 0; z-index: 29; height: auto; padding: 0; border: none; background: transparent; backdrop-filter: none; cursor: default; }
  .scrim:hover { background: transparent; }
  .pop { position: absolute; right: 0; top: calc(100% + 6px); z-index: 30; width: 380px; padding: 12px;
    background: var(--elevated); border: 1px solid var(--border); border-radius: var(--radius); font-size: 13px; }
.hint{color:var(--text-3);font-size:12px;margin:4px 0 10px}.count{color:var(--text-3)}
.tabs{display:flex;gap:4px;margin-top:8px}.tabs button{height:26px;padding:0 10px;flex:1;justify-content:center;white-space:nowrap;text-transform:none;letter-spacing:0}.tabs button.on{color:var(--accent);border-color:color-mix(in srgb,var(--accent) 55%,var(--border))}.next{margin-top:16px}
.library-row{display:flex;align-items:center;justify-content:space-between;padding:5px 0;color:var(--text-2)}.count{margin-left:6px}.folder{font-size:11px;padding:4px 8px}.error{color:var(--danger);overflow-wrap:anywhere}
.opacity{display:grid;grid-template-columns:1fr auto;gap:8px;align-items:center;margin-top:8px;color:var(--text-2);font-size:12px}.opacity input{grid-column:1/-1;width:100%;accent-color:var(--accent)}
</style>
