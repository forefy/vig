import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render } from '@testing-library/react'
import { ExhaustionDots, ExhaustionList } from './Exhaustion'

const answers = [
  { key: 'entry', done: true, text: 'every path that reaches the same code was tried' },
  { key: 'below', done: false, note: 'one role on this deployment', text: 'tried from the lowest privilege' },
  { key: 'inputs', done: true, text: 'boundary and malformed variants' },
]

describe('Exhaustion', () => {
  it('shows one dot per item, filled when done, and says the count on the button', () => {
    const onToggle = vi.fn()
    const r = render(<ExhaustionDots answers={answers} open={false} onToggle={onToggle} />)
    const button = r.getByRole('button')
    expect(button.getAttribute('aria-label')).toBe('2 of 3 ruled out')
    expect(button.querySelectorAll('span').length).toBe(3)
    expect(button.querySelectorAll('.bg-ok').length).toBe(2)
    fireEvent.click(button)
    expect(onToggle).toHaveBeenCalledTimes(1)
  })

  it('lists the items with their words and the note on what was not done', () => {
    const r = render(<ExhaustionList answers={answers} />)
    expect(r.getAllByRole('listitem').length).toBe(3)
    expect(r.getByText('one role on this deployment')).toBeTruthy()
    expect(r.getByText('every path that reaches the same code was tried')).toBeTruthy()
  })
})
