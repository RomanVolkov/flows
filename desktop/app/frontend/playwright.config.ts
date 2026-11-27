import { defineConfig } from '@playwright/test'

const baseURL = 'http://127.0.0.1:4197'

export default defineConfig({
  testDir: './tests/ui',
  testMatch: '**/*.pw.ts',
  use: { browserName: 'webkit', baseURL, trace: 'retain-on-failure' },
  webServer: {
    command: 'npm run preview -- --host 127.0.0.1 --port 4197 --strictPort',
    url: baseURL,
  },
})
