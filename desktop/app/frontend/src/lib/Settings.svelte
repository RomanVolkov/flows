<script lang="ts">
 import {request} from './workspace'
 let open=$state(false), value=$state<any>(), available=$state<{id:string;version:string}[]>([]), error=$state(''), busy=$state(false)
 async function load(){try{[value,available]=await Promise.all([request<any>('settings'),request<{id:string;version:string}[]>('harnesses')]);error=''}catch(e){error=String(e)}}
 async function save(){if(!value)return;busy=true;error='';try{value=await request('saveSettings',JSON.stringify({expected_revision:value.revision,settings:value}))}catch(e){error=String(e)}finally{busy=false}}
 function toggle(){open=!open;if(open)load()}
</script>
<div class="settings"><button class="quiet gear" onclick={toggle} title="Settings" aria-label="Settings">⚙</button>
{#if open}<button class="scrim" onclick={()=>open=false} aria-label="Close"></button><div class="pop" role="dialog" aria-label="Settings">
 <h3 class="label">Harnesses</h3><p class="hint">Available harnesses are detected from this machine.</p>
 {#if value}{#each available as item}<div class="row"><span class="mono">{item.id}</span><span class="version mono">{item.version}</span></div>{/each}
 <label class="field">Default harness<select bind:value={value.default_harness}>{#each available as item}<option value={item.id}>{item.id}</option>{/each}</select></label>
 <h3 class="label next">Terminal</h3><label class="field">Font family<input bind:value={value.terminal.font_family}/></label><label class="field">Font size<input type="number" min="10" max="32" bind:value={value.terminal.font_size}/></label>
 <button class="primary save" disabled={busy||!available.some(x=>x.id===value.default_harness)} onclick={save}>{busy?'Saving…':'Save settings'}</button>{/if}
 {#if error}<p class="error mono">{error}</p>{/if}
</div>{/if}</div>
<style>
.settings{position:relative;--wails-draggable:no-drag}.gear{font-size:18px;line-height:1;padding:0 6px}.scrim{position:fixed;inset:0;z-index:29;height:auto;padding:0;border:0;background:transparent;cursor:default}.pop{position:absolute;right:0;top:calc(100% + 6px);z-index:30;width:340px;padding:12px;background:var(--elevated);border:1px solid var(--border);border-radius:var(--radius);font-size:13px}.hint{color:var(--text-3);font-size:12px;margin:4px 0 10px}.row{display:flex;justify-content:space-between;padding:5px 0}.version{color:var(--text-3)}.field{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-top:10px;font-size:12px}.field input,.field select{max-width:170px}.next{margin-top:18px}.save{margin-top:14px}.error{color:var(--danger);overflow-wrap:anywhere}
</style>
