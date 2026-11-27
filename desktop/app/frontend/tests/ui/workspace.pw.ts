import { expect } from '@playwright/test'
import { empty, running, test } from './desktop'

test('boots with an empty workspace and recovers from a lost connection', async ({ page, launch }) => {
  await launch()
  await expect(page.locator('footer')).toContainText('0 nodes')
  await expect(page.locator('.canvas canvas')).toBeVisible()
  await page.evaluate(() => window.mockDesktop.emit('flows:connection', { ok: false, error: 'offline' }))
  await expect(page.getByText(/offline/)).toBeVisible()
  await page.evaluate(() => window.mockDesktop.emit('flows:connection', { ok: true }))
  await expect(page.getByText(/offline/)).toHaveCount(0)
})

test('creates a session with the selected harness, folder and prompt', async ({ page, launch }) => {
  await launch()
  await page.keyboard.press('Meta+n')
  const dialog = page.getByRole('dialog', { name: 'New session' })
  await expect(dialog.getByLabel('Agent')).toHaveValue('codex')
  await expect(dialog.getByLabel('First message')).toBeFocused()
  await dialog.getByLabel('Repo', { exact: true }).fill('/work/example')
  await dialog.getByLabel('First message').fill('Review the diff')
  await dialog.getByRole('button', { name: 'Create', exact: true }).click()
  await expect(dialog).toHaveCount(0)
  const payload = await page.evaluate(() => window.mockDesktop.calls.find(c => c.action === 'newSession')?.args[0])
  expect(JSON.parse(String(payload))).toMatchObject({
    harness: 'codex', work_dir: '/work/example', prompt: 'Review the diff', request_id: expect.any(String),
  })
  await page.evaluate(data => window.mockDesktop.emit('flows:snapshot', data), running)
  await expect(page.getByRole('heading', { name: 'Review changes' })).toBeVisible()
})

test('selects a canvas node, opens its terminal, sends input and detaches', async ({ page, launch }) => {
  await launch(running)
  await expect(page.locator('footer')).toContainText('1 nodes')
  await page.keyboard.press('ArrowRight')
  await expect(page.getByRole('heading', { name: 'Review changes' })).toBeVisible()
  await page.keyboard.press('Meta+Enter')
  const terminal = page.getByRole('dialog', { name: 'Review changes' })
  await expect(terminal.getByText('Mock shell')).toBeVisible()
  await expect(terminal.locator('canvas')).toBeVisible()
  await page.evaluate(() => {
    window.mockDesktop.emit('flows:terminal:terminal:attempt-1', {
      kind: 7,
      data: JSON.stringify({
        title: 'Updated shell', cols: 20, rows: 4, fg: 0xffffff, bg: 0x101019,
        cursor: { x: 1, y: 0, visible: true, style: 'block' },
        lines: [{ y: 0, cells: [{ t: 'x' }] }],
      }),
    })
  })
  await expect(terminal.getByText('Updated shell')).toBeVisible()
  await terminal.getByLabel('terminal input').press('x')
  await expect.poll(() => page.evaluate(() => window.mockDesktop.calls)).toContainEqual({
    action: 'input', args: ['terminal:attempt-1', 'x'],
  })
  await page.keyboard.press('Meta+w')
  await expect(terminal).toHaveCount(0)
  await expect.poll(() => page.evaluate(() => window.mockDesktop.calls)).toContainEqual({
    action: 'detach', args: ['terminal:attempt-1'],
  })
  await page.evaluate(data => window.mockDesktop.emit('flows:snapshot', data), empty)
  await expect(page.getByRole('heading', { name: 'Review changes' })).toHaveCount(0)
  await expect(page.locator('footer')).toContainText('0 nodes')
})

test('uses the native terminal bridge without opening a duplicate canvas attach', async ({ page, launch }) => {
  await launch(running, { native: true })
  await page.keyboard.press('ArrowRight')
  await page.keyboard.press('Meta+Enter')
  const terminal = page.getByRole('dialog', { name: 'Review changes' })
  await expect(terminal).toHaveAttribute('data-renderer', 'native')
  await expect.poll(() => page.evaluate(() => window.mockDesktop.calls.some(c => c.action === 'openNative'))).toBe(true)
  expect(await page.evaluate(() => window.mockDesktop.calls.some(c => c.action === 'attachScreen'))).toBe(false)

  await page.evaluate(() => {
    window.mockDesktop.emit('flows:terminal-native:native:attempt-1', { type: 'title', title: 'Native Ghostty' })
    window.mockDesktop.emit('flows:terminal-native:native:attempt-1', { type: 'scrollbar', scrolled: 12 })
    window.mockDesktop.emit('flows:terminal-native:native:attempt-1', { type: 'input_owner', owner_id: 'other', is_owner: false })
  })
  await expect(terminal.getByText('Native Ghostty')).toBeVisible()
  await expect(terminal.getByText(/↑ 12 lines back/)).toBeVisible()
  await expect(terminal.getByText(/Viewing · input owned by another terminal/)).toBeVisible()

  await terminal.getByRole('button', { name: 'Claim input' }).click()
  await expect.poll(() => page.evaluate(() => window.mockDesktop.calls)).toContainEqual({
    action: 'claimNativeInput', args: ['native:attempt-1'],
  })
  await expect(terminal.getByText(/Viewing · input owned by another terminal/)).toHaveCount(0)

  await page.evaluate(() => {
    window.mockDesktop.emit('flows:terminal-native:native:attempt-1', { type: 'close_requested' })
  })
  await expect(terminal).toHaveCount(0)
  await expect.poll(() => page.evaluate(() => window.mockDesktop.calls)).toContainEqual({
    action: 'detach', args: ['native:attempt-1'],
  })
})

test('falls back cleanly to canvas when native attach fails without duplicating ownership', async ({ page, launch }) => {
  await launch(running, { native: true })
  await page.evaluate(() => {
    window.go.main.App.OpenNativeTerminal = async () => {
      throw new Error('native terminal unavailable')
    }
  })
  await page.keyboard.press('ArrowRight')
  await page.keyboard.press('Meta+Enter')
  const terminal = page.getByRole('dialog', { name: 'Review changes' })
  await expect(terminal).toHaveAttribute('data-renderer', 'canvas')
  await expect(terminal.getByText('Mock shell')).toBeVisible()
  expect(await page.evaluate(() => window.mockDesktop.calls.filter(c => c.action === 'attachScreen').length)).toBe(1)
})

for (const kind of ['Workflow', 'Tool']) {
  test(`starts the authoring interview for the selected ${kind.toLowerCase()}`, async ({ page, launch }) => {
    await launch()
    await page.getByRole('button', { name: /New session/ }).click()
    const dialog = page.getByRole('dialog', { name: 'New session' })
    await dialog.getByLabel('Scenario').selectOption('authoring')
    await dialog.getByRole('button', { name: kind, exact: true }).click()
    const start = dialog.getByRole('button', { name: 'Create', exact: true })
    await expect(start).toBeDisabled()
    await dialog.getByLabel('What’s the main idea?').fill('Review open merge requests')
    await start.click()
    await expect(dialog).toHaveCount(0)
    const body = await page.evaluate(() => window.mockDesktop.calls.find(c => c.action === 'newSession')?.args[0])
    const { prompt } = JSON.parse(String(body))
    expect(prompt).toContain('one decision per turn')
    expect(prompt).toContain('core.harness')
    expect(prompt).toContain('flows validate "<package-directory>" --json')
    expect(prompt).toContain('The user selected: ' + kind.toLowerCase())
    expect(prompt).toContain('Their main idea: Review open merge requests')
  })
}

test('launches a ready imported workflow with typed inputs', async ({ page, launch }) => {
  await launch(running)
  await page.getByRole('button', { name: /New session/ }).click()
  const dialog = page.getByRole('dialog', { name: 'New session' })
  await dialog.getByLabel('Scenario').selectOption('workflow')
  const workflow = dialog.getByLabel('Workflow', { exact: true })
  await expect(workflow.getByRole('option')).toHaveText(['Select a workflow', 'external-review · 1.0.0'])
  await workflow.selectOption('external-review')
  await dialog.getByLabel('limit').fill('7')
  await dialog.getByRole('group', { name: /drafts/ }).getByRole('button', { name: 'No' }).click()
  await dialog.getByRole('button', { name: 'Create', exact: true }).click()
  await expect(dialog).toHaveCount(0)
  const body = await page.evaluate(() => window.mockDesktop.calls.find(c => c.action === 'runWorkflow')?.args[0])
  expect(JSON.parse(String(body))).toMatchObject({
    workflow_id: 'external-review', expected_digest: 'first-revision', work_dir: '/repo',
    inputs: { limit: 7, drafts: false },
  })
})

test('refreshes the launcher when a selected workflow becomes stale', async ({ page, launch }) => {
  await launch()
  await page.evaluate(() => {
    const app = window.go.main.App
    const request = app.Request
    let stale = false
    app.Request = async (action, payload) => {
      if (action === 'runWorkflow') {
        stale = true
        throw Error('Workflow changed since it was selected')
      }
      if (action === 'workflows' && stale) return JSON.stringify({ workflows: [], preparing: false })
      return request(action, payload)
    }
  })
  await page.getByRole('button', { name: /New session/ }).click()
  const dialog = page.getByRole('dialog', { name: 'New session' })
  await dialog.getByLabel('Scenario').selectOption('workflow')
  const workflow = dialog.getByLabel('Workflow', { exact: true })
  await workflow.selectOption('external-review')
  await dialog.getByLabel('limit').fill('7')
  await dialog.getByRole('group', { name: /drafts/ }).getByRole('button', { name: 'Yes' }).click()
  await dialog.getByRole('button', { name: 'Create', exact: true }).click()
  await expect(dialog.getByRole('alert')).toHaveText(/Workflow changed/)
  await expect(workflow.getByRole('option')).toHaveText(['No ready workflows'])
  await expect(dialog.getByRole('button', { name: 'Create', exact: true })).toBeDisabled()
})

test('shows invalid packages in Settings and clears resolved diagnostics', async ({ page, launch }) => {
  await launch()
  await page.evaluate(() => {
    const app = window.go.main.App
    const request = app.Request
    let reads = 0
    app.Request = async (action, payload) => {
      if (action !== 'library') return request(action, payload)
      if (++reads > 1) return JSON.stringify({ entries: [] })
      return JSON.stringify({
        entries: [{ id: 'broken-tool', kind: 'tool', status: 'invalid', path: '/tools/broken-tool',
          diagnostics: [{ message: 'Go build failed' }] }],
        malformed: [{ id: 'broken-workflow', kind: 'workflow', status: 'invalid', path: '/workflows/broken-workflow',
          diagnostics: [{ message: 'Malformed workflow YAML' }] }],
      })
    }
  })
  await page.getByRole('button', { name: 'Settings', exact: true }).click()
  const settings = page.getByRole('dialog', { name: 'Settings' })
  await expect(settings.getByText('broken-tool')).toBeVisible()
  await expect(settings.getByText('Go build failed')).toBeVisible()
  await expect(settings.getByText('broken-workflow')).toBeVisible()
  await expect(settings.getByText('Malformed workflow YAML')).toBeVisible()
  await expect(settings.getByRole('list', { name: 'Package preparation' })).toHaveCount(0)
})

test('renders adaptive workflow controls and reports missing required inputs', async ({ page, launch }) => {
  await launch(running)
  await page.evaluate(() => {
    localStorage.setItem('flows.repos', JSON.stringify(['/recent']))
    const app = window.go.main.App
    const request = app.Request
    app.Request = async (action, payload) => action === 'workflows' ? JSON.stringify({ preparing: false, workflows: [{
      id: 'external-review', version: '1.0.0', digest: 'revision', form: { fields: [
        { name: 'directory', type: 'string', required: true, control: 'folder' },
        { name: 'provider', type: 'string', enum: ['github', 'gitlab'], control: 'segmented' },
        { name: 'mode', type: 'string', enum: ['one', 'two'], control: 'select' },
        { name: 'repositories', type: 'array', control: 'repos' },
      ] },
    }] }) : request(action, payload)
  })
  await page.getByRole('button', { name: /New session/ }).click()
  const dialog = page.getByRole('dialog', { name: 'New session' })
  await dialog.getByLabel('Scenario').selectOption('workflow')
  await dialog.getByLabel('Workflow', { exact: true }).selectOption('external-review')
  const create = dialog.getByRole('button', { name: 'Create', exact: true })
  await create.click()
  await expect(dialog.getByRole('alert')).toHaveText('This field is required.')
  const folder = dialog.getByLabel('directory')
  await expect(folder).toHaveAttribute('list', 'workflow-recent-folders')
  await folder.fill('/recent')
  await dialog.getByRole('button', { name: 'gitlab' }).click()
  await dialog.getByLabel('mode').selectOption({ label: 'two' })
  const rows = dialog.getByRole('group', { name: 'repositories' })
  await rows.getByRole('button', { name: 'Add item' }).click()
  await rows.getByLabel('repositories item 1', { exact: true }).fill(' /recent ')
  await rows.getByRole('button', { name: 'Add item' }).click()
  await rows.getByRole('button', { name: 'Remove repositories item 2' }).click()
  await create.click()
  await expect(dialog).toHaveCount(0)
  const body = await page.evaluate(() => window.mockDesktop.calls.find(c => c.action === 'runWorkflow')?.args[0])
  expect(JSON.parse(String(body)).inputs).toEqual({
    directory: '/recent', provider: 'gitlab', mode: 'two', repositories: ['/recent'],
  })
})

test('answers a pending workflow approval from the Canvas inspector', async ({ page, launch }) => {
  const snapshot = {
    workspace: { cursor: 1, nodes: [{ id: 'approval', name: 'Approve the change', kind: 'tool',
      status: 'waiting_for_input', run_id: 'run', wait: { kind: 'human', ref: 'question' } }], edges: [], attempts: [] },
    sessions: [],
  }
  await launch(snapshot)
  await page.evaluate(() => {
    const app = window.go.main.App
    const request = app.Request
    app.Request = async (action, payload) => {
      if (action === 'notifications') return JSON.stringify([{ node_id: 'approval', question: {
        id: 'question', prompt: 'Continue with the requested change?', options: ['approve', 'reject'],
      } }])
      if (action === 'answerQuestion') {
        window.mockDesktop.calls.push({ action, args: [payload] })
        return '{}'
      }
      return request(action, payload)
    }
  })
  await page.keyboard.press('ArrowRight')
  const question = page.getByRole('region', { name: 'Workflow question' })
  await expect(question.getByText('Continue with the requested change?')).toBeVisible()
  await question.getByRole('button', { name: 'Approve', exact: true }).click()
  await expect(question).toHaveCount(0)
  const body = await page.evaluate(() => window.mockDesktop.calls.find(c => c.action === 'answerQuestion')?.args[0])
  expect(JSON.parse(String(body))).toEqual({ id: 'question', answer: 'approve' })
})
