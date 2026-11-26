// The tokens chart: a line per model, a day per point, thousands of tokens up.

// Day is what one model used on one day (GET /v1/usage/daily).
export interface Day {
  day: string // 2006-01-02, local
  model: string
  tokens: number
  requests: number
}

// The categorical hues in their fixed order (validated on the app's dark surface); a model past the
// eighth goes into "other", which is gray.
export const HUES = ['#3987e5', '#d95926', '#199e70', '#c98500', '#d55181', '#008300', '#9085e9', '#e66767']
export const OTHER = 'other'

// colors gives each model its hue by its rank over all time, so a period that shows fewer models
// does not repaint the others.
export function colors(models: Record<string, number>): Map<string, string> {
  const ranked = Object.entries(models ?? {}).sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
  return new Map(ranked.slice(0, HUES.length).map(([m], i) => [m, HUES[i]]))
}

const pad = (n: number) => String(n).padStart(2, '0')
export const dayOf = (d: Date) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`

// days lists the local days from first to last, both in.
export function days(first: string, last: string): string[] {
  const [y, m, d] = first.split('-').map(Number)
  const out: string[] = []
  for (let at = new Date(y, m - 1, d); dayOf(at) <= last && out.length < 5000; at.setDate(at.getDate() + 1)) out.push(dayOf(at))
  return out
}

export interface Series {
  model: string
  color: string
  values: number[] // tokens per day of the range
  total: number
}

// series lays the days out over range: one series per model with a hue, the rest summed in
// "other"; the most used first.
export function series(data: Day[], range: string[], hue: Map<string, string>): Series[] {
  const at = new Map(range.map((d, i) => [d, i]))
  const by = new Map<string, Series>()
  for (const d of data) {
    const i = at.get(d.day)
    if (i === undefined) continue
    const model = hue.has(d.model) ? d.model : OTHER
    let s = by.get(model)
    if (!s) {
      s = { model, color: hue.get(model) ?? 'var(--text-3)', values: range.map(() => 0), total: 0 }
      by.set(model, s)
    }
    s.values[i] += d.tokens
    s.total += d.tokens
  }
  return [...by.values()].sort((a, b) => (a.model === OTHER ? 1 : 0) - (b.model === OTHER ? 1 : 0) || b.total - a.total)
}

// ticks are round steps (1, 2 or 5 times a power of ten) from 0 to at least max, about count of them.
export function ticks(max: number, count = 5): number[] {
  if (!(max > 0)) return [0, 1]
  const raw = max / count
  const p = 10 ** Math.floor(Math.log10(raw))
  const step = [1, 2, 5, 10].map((f) => f * p).find((s) => s >= raw)!
  const out: number[] = []
  for (let v = 0; v < max + step; v += step) out.push(v)
  return out
}

// thousands writes a count of thousands for the axis and the tooltip: 0, 850, 1,200, 0.4
export const thousands = (tokens: number) => {
  const k = tokens / 1000
  return k > 0 && k < 10 ? k.toFixed(1) : Math.round(k).toLocaleString('en-US')
}
