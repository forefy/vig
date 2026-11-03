import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render } from '@testing-library/react'
import { FindingCode } from './Sites'
import type { components } from '@/api/client'

type Finding = components['schemas']['Finding']

const finding = {
  normal_use: 'A member reads their own board.',
  expected: 'Only members read a board.',
  impact: 'Any user reads every board.',
  sites_kind: 'flow',
  location: { file: 'server/api.go', line_start: 10 },
  sites: [
    { location: { file: 'server/api.go', line_start: 10 }, note: 'the handler' },
    { location: { file: 'server/store.go', line_start: 42 }, note: 'the query' },
  ],
} as unknown as Finding

const tabsOf = (r: ReturnType<typeof render>) => Array.from(r.container.querySelectorAll<HTMLElement>('[role=tab]'))
const selected = (r: ReturnType<typeof render>) => tabsOf(r).findIndex((t) => t.getAttribute('aria-selected') === 'true')

describe('FindingCode', () => {
  it('opens on the step its key names, the first one otherwise', () => {
    expect(selected(render(<FindingCode finding={finding} />))).toBe(0)
    expect(selected(render(<FindingCode finding={finding} open="impact" />))).toBe(4)
    expect(selected(render(<FindingCode finding={finding} open="2" />))).toBe(3)
    expect(selected(render(<FindingCode finding={finding} open="nope" />))).toBe(0)
  })

  it('names each step it switches to by its key', () => {
    const onOpen = vi.fn()
    const r = render(<FindingCode finding={finding} onOpen={onOpen} id="f-1-steps" />)
    const tabs = tabsOf(r)
    fireEvent.click(tabs[1])
    fireEvent.click(tabs[2])
    fireEvent.click(tabs[4])
    expect(onOpen.mock.calls.map((c) => c[0])).toEqual(['expected', '1', 'impact'])
    expect(selected(r)).toBe(4)
    expect(r.container.querySelector('#f-1-steps')).not.toBeNull()
  })
})
