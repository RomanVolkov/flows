import { describe, expect, it } from 'vitest'
import type { Graph, State } from '../graph'
import { mark, moving } from './render'

const states: State[] = ['pending', 'starting', 'running', 'watching', 'blocked', 'done', 'failed', 'stopped']

describe('marks', () => {
  it('tells every state apart without its colour', () => {
    const seen = new Set(states.map((s) => JSON.stringify(mark(s))))
    expect(seen.size).toBe(states.length)
  })
  it('turns while a node works', () => {
    expect(mark('running').spin).toBe(true)
    expect(mark('done').spin).toBe(false)
    const g = (s: State) => ({ seq: 0, nodes: new Map([['a', { id: 'a', kind: 'session', state: s }]]), edges: [], sessions: new Map() }) as unknown as Graph
    expect(moving(g('running'))).toBe(true)
    expect(moving(g('blocked'))).toBe(false)
  })
})
