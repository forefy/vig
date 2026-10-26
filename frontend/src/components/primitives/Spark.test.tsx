import { beforeEach, describe, expect, it } from 'vitest'
import { fireEvent, render } from '@testing-library/react'
import { Spark, bolts } from './Spark'

describe('Spark', () => {
  beforeEach(() => localStorage.clear())

  it('sparks the first time, remembers it, and never again', () => {
    const first = render(<Spark id="t"><button>go</button></Spark>)
    expect(first.container.querySelector('[data-spark="t"]')).not.toBeNull()
    expect(first.container.querySelectorAll('.spark-bolt').length).toBeGreaterThan(3)
    expect(localStorage.getItem('vig.spark.t')).not.toBeNull()
    first.unmount()
    const again = render(<Spark id="t"><button>go</button></Spark>)
    expect(again.container.querySelector('[data-spark="t"]')).toBeNull()
  })

  it('ends on a click of the control', () => {
    const r = render(<Spark id="c"><button>go</button></Spark>)
    fireEvent.click(r.getByText('go'))
    expect(r.container.querySelector('[data-spark="c"]')).toBeNull()
  })

  it('draws bolts that leave the box the same way every time', () => {
    const a = bolts(80, 24)
    const b = bolts(80, 24)
    expect(a).toEqual(b)
    expect(a.length).toBe(7)
    for (const bolt of a) expect(bolt.d).toMatch(/^M[\d.]+ [\d.]+( L[\d.]+ [\d.]+){3,5}/)
  })
})
