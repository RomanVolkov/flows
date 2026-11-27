import type { WorkflowField } from './workspace'

export function workflowControl(field: WorkflowField): NonNullable<WorkflowField['control']> {
  if (field.control) return field.control
  if (field.type === 'boolean') return 'toggle'
  if (field.enum?.length) return field.enum.length <= 3 ? 'segmented' : 'select'
  const folder = /repo|folder|directory/i.test(field.name)
  if (field.type === 'array') return folder ? 'repos' : 'array'
  if (field.type === 'number' || field.type === 'integer') return 'number'
  return folder && field.type === 'string' ? 'folder' : 'text'
}

export function displayWorkflowValue(value: unknown): string {
  if (value === undefined || value === null) return ''
  return typeof value === 'object' ? JSON.stringify(value) : String(value)
}

export function coerceWorkflowInputs(fields: WorkflowField[], values: Record<string, unknown>) {
  const inputs: Record<string, unknown> = {}
  const errors: Record<string, string> = {}
  for (const field of fields) {
    const { name, type } = field
    try {
      let value = values[name]
      if (value === undefined || value === null || (typeof value === 'string' && !value.trim())) {
        value = field.default
      }
      if (value === undefined || value === null) {
        if (field.required) throw Error('This field is required.')
        continue
      }
      switch (type) {
        case 'number':
        case 'integer': {
          const number = typeof value === 'number' || typeof value === 'string' ? Number(value) : NaN
          if (!Number.isFinite(number)) throw Error('Enter a number.')
          if (type === 'integer' && !Number.isInteger(number)) throw Error('Enter a whole number.')
          value = number
          break
        }
        case 'boolean':
          if (typeof value !== 'boolean') throw Error('Choose Yes or No.')
          break
        case 'array':
        case 'object':
          if (typeof value === 'string') {
            try { value = JSON.parse(value) } catch { throw Error('Enter valid JSON.') }
          }
          if (type === 'array') {
            if (!Array.isArray(value)) throw Error('Enter a list.')
            value = value.map(item => typeof item === 'string' ? item.trim() : item).filter(item => item !== '')
          } else if (!value || typeof value !== 'object' || Array.isArray(value)) {
            throw Error('Enter a JSON object.')
          }
          break
        case 'string':
          if (typeof value !== 'string') throw Error('Enter text.')
      }
      if (field.enum?.length && !field.enum.some(option => JSON.stringify(option) === JSON.stringify(value))) {
        throw Error('Choose one of the available values.')
      }
      inputs[name] = value
    } catch (error) {
      errors[name] = error instanceof Error ? error.message : String(error)
    }
  }
  return { inputs, errors }
}
