// The tokens a claude or codex session used (the daemon reads the agent's transcripts).
export interface Usage {
  model?: string
  input: number // not read from the cache
  output: number
  cache_read: number
  cache_write: number
  reasoning?: number // a part of output (codex)
  requests: number
  context: number // the tokens of the latest request
  window?: number // the model's context window, when known
  transcripts: number // 0: the agent has written none yet
}

export const counted = (agent: string) => agent === 'claude' || agent === 'codex'

export const total = (u: Usage) => u.input + u.output + u.cache_read + u.cache_write

// tokens, short: 950, 12.3k, 1.24M
export function tokens(n: number): string {
  if (n < 1000) return String(n)
  if (n < 1e6) return `${(n / 1e3).toFixed(n < 1e4 ? 1 : 0)}k`
  return `${(n / 1e6).toFixed(n < 1e7 ? 2 : 1)}M`
}

// What sessions used together (GET /v1/usage): models holds all the tokens by model.
export interface Totals {
  usage: Usage
  requests: number
  models: Record<string, number>
  harnesses: Record<string, number>
}

// The periods the totals are shown for: since is RFC 3339, '' for all time. Days begin at local
// midnight; "7 days" is today and the six before.
export function periods(now: Date): { label: string; since: string }[] {
  const day = (back: number) => new Date(now.getFullYear(), now.getMonth(), now.getDate() - back).toISOString()
  return [
    { label: 'Today', since: day(0) },
    { label: '7 days', since: day(6) },
    { label: '30 days', since: day(29) },
    { label: 'All', since: '' },
  ]
}

// the models, the most used first
export const byModel = (t: Totals) => Object.entries(t.models ?? {}).sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))

// sum adds totals up: the tokens, the sessions and each model's.
export function sum(list: Totals[]): Totals {
  const out: Totals = { usage: { input: 0, output: 0, cache_read: 0, cache_write: 0, requests: 0, context: 0, transcripts: 0 }, requests: 0, models: {}, harnesses: {} }
  for (const t of list) {
    for (const k of ['input', 'output', 'cache_read', 'cache_write', 'requests', 'transcripts'] as const) out.usage[k] += t.usage[k] ?? 0
    out.requests += t.requests
    for (const [m, n] of Object.entries(t.models ?? {})) out.models[m] = (out.models[m] ?? 0) + n
    for (const [h, n] of Object.entries(t.harnesses ?? {})) out.harnesses[h] = (out.harnesses[h] ?? 0) + n
  }
  return out
}
