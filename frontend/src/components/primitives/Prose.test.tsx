import { describe, expect, it } from 'vitest'
import { render } from '@testing-library/react'
import { Prose, proseBlocks } from './Prose'

describe('proseBlocks', () => {
  it('splits paragraphs, bullets and fences like the server', () => {
    const b = proseBlocks("- use `Signer`\n```rust\npub a: Signer<'info>,\n\nx\n```\n\n- or check it")
    expect(b).toEqual([
      { code: false, lines: ['- use `Signer`'] },
      { code: true, lang: 'rust', text: "pub a: Signer<'info>,\n\nx" },
      { code: false, lines: ['- or check it'] },
    ])
  })
  it('keeps an unclosed fence as code rather than dropping it', () => {
    expect(proseBlocks('a\n```\nb')).toEqual([{ code: false, lines: ['a'] }, { code: true, lang: '', text: 'b' }])
  })
})

describe('Prose links', () => {
  const links = [{ ordinal: 1, title: 'Owner check missing' }, { ordinal: 3, title: 'Arbitrary CPI target' }]
  it('sets [[Title]] as a link to that finding and a stranger as its words', () => {
    const r = render(<Prose text="After [[owner check   missing]] and [[Gone]], see `[[Arbitrary CPI target]]`." links={links} />)
    const a = r.getByRole('link')
    expect(a.getAttribute('href')).toBe('#f-1')
    expect(a.textContent).toBe('01Owner check missing')
    expect(r.container.textContent).toContain('and Gone,')
    expect(r.container.querySelector('code')?.textContent).toBe('[[Arbitrary CPI target]]')
  })
})
