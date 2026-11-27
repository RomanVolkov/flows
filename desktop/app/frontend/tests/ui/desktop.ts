import { expect, test as base } from '@playwright/test'
import type { Workspace, LiveSession } from '../../src/lib/workspace'

export interface Snapshot {
  workspace: Workspace
  sessions: LiveSession[]
}

export const empty: Snapshot = {
  workspace: { cursor: 0, nodes: [], edges: [], attempts: [] }, sessions: [],
}

export const running: Snapshot = {
  workspace: {
    cursor: 1,
    nodes: [{ id: 'node-1', name: 'Review changes', kind: 'session', status: 'running', run_id: 'run-1' }],
    edges: [],
    attempts: [{ id: 'attempt-1', node_id: 'node-1', number: 1, status: 'running', started_at: 1_000_000 }],
  },
  sessions: [{ session_id: 'session-1', attempt_id: 'attempt-1', status: 'running', command: 'codex', work_dir: '/repo', child: { pid: 42 } }],
}

interface Call { action: string; args: unknown[] }
declare global {
  interface Window {
    mockDesktop: { emit(event: string, data: unknown): void; calls: Call[] }
  }
}

function installBridge(config: { initial: Snapshot; native?: boolean }) {
  const { initial, native = false } = config
  const listeners = new Map<string, Set<(data: unknown) => void>>()
  const calls: Call[] = []
  function emit(event: string, data: unknown) {
    for (const listener of listeners.get(event) ?? []) listener(data)
  }
  window.mockDesktop = { emit, calls }
  window.runtime = {
    EventsOn(event, listener) {
      const group = listeners.get(event) ?? new Set()
      listeners.set(event, group)
      group.add(listener)
      return () => group.delete(listener)
    },
  }

  const responses: Record<string, unknown> = {
    settings: { default_harness: 'codex', default_directory: '/repo',
      terminal: { font_family: 'IosevkaTerm Nerd Font Mono', font_size: 19, opacity: 0.9, blur: true } },
    harnesses: [{ id: 'codex', version: '1' }],
    usage: { requests: 0, uncached_input_tokens: 0, output_tokens: 0, cache_read_tokens: 0, cache_write_tokens: 0 },
    library: { entries: [] },
    workflows: { preparing: false, workflows: [
      { id: 'external-review', version: '1.0.0', digest: 'first-revision', path: '/workflow', origin: 'user',
        form: { fields: [{ name: 'limit', type: 'integer', required: true }, { name: 'drafts', type: 'boolean', required: true }] } },
    ] },
    runWorkflow: { node_id: 'node-1', run_id: 'run-1', status: 'ready' },
    newSession: { node_id: 'node-1' },
  }
  function frame(kind: number, payload: unknown) {
    return { kind, data: JSON.stringify(payload) }
  }
  const screen = frame(7, { full: true, title: 'Mock shell', cols: 20, rows: 4,
    fg: 0xffffff, bg: 0x101019, cursor: { x: 0, y: 0, visible: false, style: 'block' },
    lines: [{ y: 0, cells: [{ t: '$' }] }] })
  const streams = new Map<string, ReturnType<typeof frame>[]>()
  const readers = new Map<string, () => void>()
  const app: Record<string, unknown> = {
    Follow() { emit('flows:snapshot', initial) },
    async Request(action: string, payload: string) {
      calls.push({ action, args: [payload] })
      if (!(action in responses)) throw Error('Unexpected request: ' + action)
      return JSON.stringify(responses[action])
    },
    async OpenPreview(attempt: string) {
      const id = 'preview:' + attempt
      streams.set(id, [screen])
      return id
    },
    async AttachScreen(attempt: string) {
      const id = 'terminal:' + attempt
      calls.push({ action: 'attachScreen', args: [attempt] })
      streams.set(id, [frame(5, { owner_id: id }), screen])
      return id
    },
    async StreamTerminal(id: string) {
      for (const next of streams.get(id) ?? []) emit('flows:terminal:' + id, next)
      await new Promise<void>(resolve => readers.set(id, resolve))
    },
    async ResizeTerminal() {},
    async WriteTerminal(id: string, data: string) { calls.push({ action: 'input', args: [id, data] }) },
    async Detach(id: string) {
      calls.push({ action: 'detach', args: [id] })
      readers.get(id)?.()
      readers.delete(id)
      streams.delete(id)
    },
  }
  if (native) {
    app.OpenNativeTerminal = async (attempt: string, x: number, y: number, width: number, height: number, fontSize: number) => {
      const id = 'native:' + attempt
      calls.push({ action: 'openNative', args: [attempt, x, y, width, height, fontSize] })
      queueMicrotask(() => {
        emit('flows:terminal-native:' + id, { type: 'ready', scrolled: 0 })
        emit('flows:terminal-native:' + id, { type: 'input_owner', owner_id: id, is_owner: true })
      })
      return id
    }
    app.SetNativeTerminalBounds = async (id: string, x: number, y: number, width: number, height: number) => {
      calls.push({ action: 'setNativeBounds', args: [id, x, y, width, height] })
    }
    app.FocusNativeTerminal = async (id: string) => {
      calls.push({ action: 'focusNative', args: [id] })
    }
    app.ClaimNativeTerminalInput = async (id: string) => {
      calls.push({ action: 'claimNativeInput', args: [id] })
      emit('flows:terminal-native:' + id, { type: 'input_owner', owner_id: id, is_owner: true })
    }
  }
  Object.assign(window, { go: { main: { App: app } } })
}

export const test = base.extend<{ launch: (snapshot?: Snapshot, options?: { native?: boolean }) => Promise<void> }>({
  launch: async ({ page }, use) => {
    const errors: string[] = []
    page.on('pageerror', error => errors.push(error.message))
    await use(async (snapshot = empty, options = {}) => {
      await page.addInitScript(installBridge, { initial: snapshot, native: options.native ?? false })
      await page.goto('/')
      await expect(page.getByRole('button', { name: /New session/ })).toBeVisible()
    })
    expect(errors, 'Uncaught frontend errors').toEqual([])
  },
})
