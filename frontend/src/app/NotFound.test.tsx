import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { createMemoryRouter, RouterProvider } from 'react-router'
import { HelmetProvider } from 'react-helmet-async'
import { NotFound } from './NotFound'

describe('NotFound', () => {
  it('renders one line and one action for an unknown path', () => {
    const router = createMemoryRouter([{ path: '*', element: <NotFound /> }], { initialEntries: ['/nowhere'] })
    render(
      <HelmetProvider>
        <RouterProvider router={router} />
      </HelmetProvider>,
    )
    expect(screen.getByText('Nothing at this address')).toBeInTheDocument()
    const home = screen.getByText('Home')
    expect(home.closest('a')).toHaveAttribute('href', '/')
  })
})
