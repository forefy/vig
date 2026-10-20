import { defineConfig } from '@playwright/test'

// Runs against the real dev stack (backend with VIG_ENV=dev and the seed,
// frontend on 7111). No mocks: the specs sign in through the dev login.
export default defineConfig({
  testDir: 'e2e',
  timeout: 60_000,
  retries: 0,
  use: {
    baseURL: process.env.PLAYWRIGHT_BASE_URL ?? 'http://localhost:7111',
    trace: 'retain-on-failure',
  },
  reporter: process.env.CI ? 'github' : 'list',
})
