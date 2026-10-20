import { describe, expect, it } from 'vitest'
import { render, waitFor } from '@testing-library/react'
import { HelmetProvider } from 'react-helmet-async'
import { Seo } from './Seo'

describe('Seo', () => {
  it('writes title, canonical and card tags into the head', async () => {
    render(
      <HelmetProvider>
        <Seo title="Audits" description="desc" path="/x" />
      </HelmetProvider>,
    )
    await waitFor(() => expect(document.title).toBe('Audits'))
    expect(document.querySelector('link[rel="canonical"]')?.getAttribute('href')).toBe('/x')
    expect(document.querySelector('meta[property="og:title"]')?.getAttribute('content')).toBe('Audits')
    expect(document.querySelector('meta[name="twitter:card"]')?.getAttribute('content')).toBe('summary_large_image')
    expect(document.querySelector('meta[name="robots"]')).toBeNull()
  })
})
