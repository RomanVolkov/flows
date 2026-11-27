import { describe, expect, test } from 'vitest'
import type { WorkflowField } from './workspace'
import { coerceWorkflowInputs, workflowControl } from './workflow'

describe('workflow inputs', () => {
  const fields: WorkflowField[] = [
    { name: 'count', type: 'integer', required: true },
    { name: 'drafts', type: 'boolean', required: true },
    { name: 'repositories', type: 'array', required: true },
    { name: 'provider', type: 'string', required: true, default: 'github', enum: ['github', 'gitlab'] },
  ]

  test('keeps boolean and numeric types, trims arrays, and applies defaults', () => {
    expect(coerceWorkflowInputs(fields, { count: '3', drafts: false, repositories: [' /repo ', ''] })).toEqual({
      inputs: { count: 3, drafts: false, repositories: ['/repo'], provider: 'github' }, errors: {},
    })
  })

  test('reports missing values, invalid JSON and enum constraints inline', () => {
    expect(coerceWorkflowInputs(fields, { count: '3.5', repositories: '[broken', provider: 'other' }).errors).toEqual({
      count: 'Enter a whole number.', drafts: 'This field is required.',
      repositories: 'Enter valid JSON.', provider: 'Choose one of the available values.',
    })
  })

  test('infers controls for older forms and respects an override', () => {
    expect(fields.map(workflowControl)).toEqual(['number', 'toggle', 'repos', 'segmented'])
    expect(workflowControl({ ...fields[3], control: 'select' })).toBe('select')
    expect(workflowControl({ name: 'directory', type: 'string', required: true })).toBe('folder')
  })
})
