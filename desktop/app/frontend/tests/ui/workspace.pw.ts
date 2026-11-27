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
