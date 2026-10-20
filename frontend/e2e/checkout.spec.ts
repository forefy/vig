import { test, expect } from '@playwright/test'

// Checkout up to the wallet step: the Pro plan shows the monthly price on
// the card and the full amount on the pay button, with the one mandated line.
test('pro checkout shows the total and the terms line', async ({ page }) => {
  await page.goto('/v1/auth/dev/login?user=free')
  await page.goto('/app/billing')
  await expect(page.getByRole('heading', { name: 'Credits' })).toBeVisible()
  await page.getByRole('button', { name: /^Pro/ }).click()
  await expect(page.getByText('$540', { exact: true }).first()).toBeVisible()
  await expect(page.getByText('Billed once, no refunds.')).toBeVisible()
})
