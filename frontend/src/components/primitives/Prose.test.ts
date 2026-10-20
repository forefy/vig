import { describe, expect, it } from 'vitest'
import { proseBlocks } from './Prose'

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
