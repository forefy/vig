import { test, expect } from '@playwright/test'

// The wizard against the real backend and GitHub: sign in as the seeded pro
// user, resolve a public repository, reach the programs step.
test('source step resolves a public repository', async ({ page }) => {
  await page.goto('/v1/auth/dev/login?user=pro')
  await page.goto('/app/audits/new')
  await expect(page.getByRole('heading', { name: 'Start an audit' })).toBeVisible()
  await page.getByRole('button', { name: 'Continue' }).click()
  await page.getByRole('textbox', { name: 'Repository URL' }).fill('https://github.com/vig-demo/vault-programs')
  await page.getByRole('button', { name: 'Continue' }).click()
  await expect(page.getByPlaceholder('program id')).toBeVisible({ timeout: 30_000 })
})

// The EVM path: the contract card, a public Solidity repository, the
// chain, and one contract fetched from the chain with its panel.
test('evm contract step fetches a verified contract', async ({ page }) => {
  await page.goto('/v1/auth/dev/login?user=pro')
  await page.goto('/app/audits/new')
  await expect(page.getByRole('heading', { name: 'Start an audit' })).toBeVisible()
  await page.getByText('EVM contract', { exact: true }).click()
  await page.getByRole('button', { name: 'Continue' }).click()
  await page.getByRole('textbox', { name: 'Repository URL' }).fill('https://github.com/vig-demo/orderbook')
  await page.getByRole('button', { name: 'Continue' }).click()
  await expect(page.getByRole('textbox', { name: 'Program id' })).toBeVisible({ timeout: 30_000 })
  await page.getByText('Ethereum', { exact: true }).click()
  await page.getByRole('textbox', { name: 'Program id' }).fill('0x000000fF3D7A2d373615141d7489Ca66683DbecF')
  await page.getByRole('button', { name: 'Fetch' }).click()
  await expect(page.getByText('verified', { exact: true }).first()).toBeVisible({ timeout: 60_000 })
  await expect(page.getByText('no proxy', { exact: true }).first()).toBeVisible()
})
