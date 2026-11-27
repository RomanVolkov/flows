<script lang="ts">
  import type { WorkflowField } from './workspace'
  import { displayWorkflowValue, workflowControl } from './workflow'

  let { fields, values, errors, history, onchange }: {
    fields: WorkflowField[]
    values: Record<string, unknown>
    errors: Record<string, string>
    history: string[]
    onchange: (name: string, value: unknown) => void
  } = $props()
</script>

{#each fields as field (field.name)}
  {@const id = 'workflow-input-' + field.name}
  {@const control = workflowControl(field)}
  {@const value = values[field.name]}
  {@const label = field.name + (field.required ? ' *' : '')}
  {@const choices = control === 'toggle' ? [true, false] : field.enum ?? []}
  {@const description = (field.description ? id + '-description ' : '') + (errors[field.name] ? id + '-error' : '')}
  <div class="field">
    {#if control === 'segmented' || control === 'toggle'}
      <span class="field-label" id={id}>{label}</span>
      <div class="choices" role="group" aria-labelledby={id} aria-describedby={description.trim() || undefined}>
        {#each choices as option}
          {@const selected = JSON.stringify(option) === JSON.stringify(value)}
          <button type="button" class:selected aria-pressed={selected} onclick={()=>onchange(field.name,option)}>
            {control === 'toggle' ? option ? 'Yes' : 'No' : displayWorkflowValue(option)}
          </button>
        {/each}
      </div>
    {:else if control === 'array' || control === 'repos'}
      {@const rows = Array.isArray(value) ? value : []}
      <span class="field-label" id={id}>{label}</span>
      <div class="items" role="group" aria-labelledby={id} aria-describedby={description.trim() || undefined}>
        {#each rows as item, index}
          <div class="item">
            <input aria-label={field.name + ' item ' + (index + 1)} list={control === 'repos' ? 'workflow-recent-folders' : undefined}
              placeholder={field.placeholder} value={displayWorkflowValue(item)}
              oninput={e=>onchange(field.name,rows.map((row,i)=>i===index?e.currentTarget.value:row))}/>
            <button type="button" aria-label={'Remove ' + field.name + ' item ' + (index + 1)}
              onclick={()=>onchange(field.name,rows.filter((_,i)=>i!==index))}>Remove</button>
          </div>
        {/each}
        <button type="button" onclick={()=>onchange(field.name,[...rows,''])}>Add item</button>
      </div>
    {:else}
      <label for={id}>{label}</label>
      {#if control === 'select'}
        <select {id} aria-describedby={description.trim() || undefined}
          value={choices.findIndex(option=>JSON.stringify(option)===JSON.stringify(value))}
          onchange={e=>onchange(field.name,choices[Number(e.currentTarget.value)])}>
          <option value={-1}>{field.placeholder ?? 'Select…'}</option>
          {#each choices as option, index}<option value={index}>{displayWorkflowValue(option)}</option>{/each}
        </select>
      {:else if control === 'multiline'}
        <textarea {id} rows="3" placeholder={field.placeholder} aria-describedby={description.trim() || undefined}
          value={displayWorkflowValue(value)} oninput={e=>onchange(field.name,e.currentTarget.value)}></textarea>
      {:else}
        <input {id} type={control === 'number' ? 'number' : 'text'} step={field.type === 'number' ? 'any' : undefined}
          list={control === 'folder' ? 'workflow-recent-folders' : undefined}
          placeholder={field.placeholder} aria-invalid={!!errors[field.name]} aria-describedby={description.trim() || undefined}
          value={displayWorkflowValue(value)} oninput={e=>onchange(field.name,e.currentTarget.value)}/>
      {/if}
    {/if}
    {#if field.description}<p class="description" id={id + '-description'}>{field.description}</p>{/if}
    {#if errors[field.name]}<p class="error" role="alert" id={id + '-error'}>{errors[field.name]}</p>{/if}
  </div>
{/each}
<datalist id="workflow-recent-folders">
  {#each history as path}<option value={path}></option>{/each}
</datalist>

<style>
 .field{display:grid;gap:5px;margin-top:8px}
 label,.field-label{font:600 11px var(--mono);color:var(--text-3);letter-spacing:.08em;text-transform:uppercase}
 .choices{display:flex;flex-wrap:wrap;gap:4px}.choices button{flex:1;justify-content:center}
 .selected{color:var(--accent);border-color:var(--accent);background:var(--accent-glow)}
 .items{display:grid;gap:4px;justify-items:start}.item{display:flex;gap:4px;width:100%}.item input{min-width:0;flex:1}
 .description,.error{margin:0;font-size:12px;color:var(--text-3)}.error{color:var(--danger)}
</style>
