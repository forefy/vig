import { PublicShell } from './PublicShell'
import { NotFound } from './NotFound'
import { Landing } from '@/features/landing/Landing'
import { ScanPage } from '@/features/scan/ScanPage'
import { ScanResult } from '@/features/scan/ScanResult'
import { PublicReport } from '@/features/report/PublicReport'
import { Terms, Privacy } from '@/features/legal/Legal'

// Public routes. Shared by the browser router and the prerender entry, so this
// module must not import anything that needs a wallet or a session. The
// catch-all lives here so an unknown address renders inside the public shell.
export const publicRoutes = [
  {
    element: <PublicShell />,
    children: [
      { path: '/', element: <Landing /> },
      { path: '/scan', element: <ScanPage /> },
      { path: '/s/:token', element: <ScanResult /> },
      { path: '/r/:slug', element: <PublicReport /> },
      { path: '/terms', element: <Terms /> },
      { path: '/privacy', element: <Privacy /> },
      { path: '*', element: <NotFound /> },
    ],
  },
]
