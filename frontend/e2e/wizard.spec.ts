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

// The web path: the site card, an origin read once for its panel, a
// subdomain and a prefix added to the scope, and the review pinned to the
// scope's URL prefixes. No repository: the site is live only.
test('web app steps reach the review with the authorised scope', async ({ page }) => {
  await page.goto('/v1/auth/dev/login?user=pro')
  await page.goto('/app/audits/new')
  await expect(page.getByRole('heading', { name: 'Start an audit' })).toBeVisible()
  await page.getByText('Web app', { exact: true }).click()
  await expect(page.getByText('live site', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Continue' }).click()
  await page.getByRole('textbox', { name: 'Origin' }).fill('https://example.com')
  await expect(page.getByText('Ownership', { exact: true })).toBeVisible({ timeout: 30_000 })
  await expect(page.getByText('not proven', { exact: true }).first()).toBeVisible()
  await page.getByRole('button', { name: 'Continue' }).click()
  await page.getByRole('textbox', { name: 'hosts' }).fill('api.example.com')
  await page.getByRole('button', { name: 'Add' }).first().click()
  await expect(page.getByText('subdomain', { exact: true })).toBeVisible()
  await page.getByRole('textbox', { name: 'path prefixes' }).fill('/v1/')
  await page.getByRole('button', { name: 'Add' }).last().click()
  await page.getByRole('button', { name: 'Review', exact: true }).click()
  await expect(page.getByText('https://api.example.com/v1/', { exact: true })).toBeVisible({ timeout: 15_000 })
  await expect(page.getByText('https://example.com/v1/', { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Queue audit' })).toBeVisible()
})
