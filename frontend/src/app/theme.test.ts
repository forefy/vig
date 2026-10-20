import { describe, expect, it, beforeEach } from 'vitest'
import { readTheme, setTheme, applyTheme } from './theme'

describe('theme', () => {
  beforeEach(() => {
    localStorage.clear()
    document.documentElement.className = ''
  })

  it('defaults to dark without a stored choice or a light OS preference', () => {
    expect(readTheme()).toBe('dark')
  })

  it('honours a stored choice', () => {
    localStorage.setItem('theme', 'light')
    expect(readTheme()).toBe('light')
  })

  it('setTheme stores and applies the class', () => {
    setTheme('light')
    expect(localStorage.getItem('theme')).toBe('light')
    expect(document.documentElement.classList.contains('dark')).toBe(false)
    setTheme('dark')
    expect(document.documentElement.classList.contains('dark')).toBe(true)
  })

  it('applyTheme toggles only the dark class', () => {
    applyTheme('dark')
    expect(document.documentElement.classList.contains('dark')).toBe(true)
    applyTheme('light')
    expect(document.documentElement.classList.contains('dark')).toBe(false)
  })
})
