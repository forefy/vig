import { createBrowserRouter } from 'react-router'
import { publicRoutes } from './publicRoutes'
import { Shell } from './Shell'
import { RouteError } from './RouteError'
import { lazyRoute } from './lazy'
import { RequireAuth, RequireAdmin } from '@/features/auth/guards'
import { WalletGate } from '@/features/auth/WalletGate'

// Everything behind the session is code-split. Only routes that sign
// something sit under the wallet gate: login, the wizard, billing, and
// wallet linking in settings. The public routes stay eager for the prerender.
const login = () => import('@/features/auth/Login')
const dashboard = () => import('@/features/dashboard/Dashboard')
const audits = () => import('@/features/audits/AuditsList')
const auditDetail = () => import('@/features/audits/AuditDetail')
const newAudit = () => import('@/features/audits/NewAudit')
const report = () => import('@/features/audits/Report')
const billing = () => import('@/features/billing/Billing')
const settings = () => import('@/features/settings/Settings')
const panels = () => import('@/features/settings/Panels')
const admin = () => import('@/features/admin/Admin')
const verify = () => import('@/features/admin/Verify')

export const routes = [
  {
    errorElement: <RouteError />,
    children: [
      ...publicRoutes,
      {
        element: <WalletGate />,
        children: [{ path: '/login', element: lazyRoute(login, 'Login') }],
      },
      {
        element: <RequireAuth />,
        children: [
          {
            element: <Shell />,
            children: [
              { path: '/app', element: lazyRoute(dashboard, 'Dashboard') },
              { path: '/app/audits', element: lazyRoute(audits, 'AuditsList') },
              { path: '/app/audits/:id', element: lazyRoute(auditDetail, 'AuditDetail') },
              { path: '/app/audits/:id/surface', element: lazyRoute(auditDetail, 'AuditSurfaceView') },
              { path: '/app/audits/:id/report', element: lazyRoute(report, 'ReportPage') },
              {
                element: <WalletGate />,
                children: [
                  { path: '/app/audits/new', element: lazyRoute(newAudit, 'NewAudit') },
                  { path: '/app/billing', element: lazyRoute(billing, 'Billing') },
                ],
              },
              {
                path: '/app/settings',
                element: lazyRoute(settings, 'Settings'),
                children: [
                  { index: true, element: lazyRoute(panels, 'ProfilePanel') },
                  { element: <WalletGate />, children: [{ path: 'identities', element: lazyRoute(panels, 'IdentitiesPanel') }] },
                  { path: 'notifications', element: lazyRoute(panels, 'NotificationsPanel') },
                  { path: 'security', element: lazyRoute(panels, 'SecurityPanel') },
                  { path: 'api-keys', element: lazyRoute(panels, 'ApiKeysPanel') },
                  { path: 'data', element: lazyRoute(panels, 'DataPanel') },
                ],
              },
              { path: '/admin/verify', element: lazyRoute(verify, 'AdminVerify') },
              {
                element: <RequireAdmin />,
                children: [
                  {
                    path: '/admin',
                    element: lazyRoute(admin, 'Admin'),
                    children: [
                      { index: true, element: lazyRoute(admin, 'AdminQueue') },
                      { path: 'audits', element: lazyRoute(admin, 'AdminAudits') },
                      { path: 'audits/:id', element: lazyRoute(admin, 'AdminAuditDetail') },
                      { path: 'audits/:id/surface', element: lazyRoute(admin, 'AdminAuditSurfaceView') },
                      { path: 'claims', element: lazyRoute(admin, 'AdminClaims') },
                      { path: 'users', element: lazyRoute(admin, 'AdminUsers') },
                      { path: 'users/:id', element: lazyRoute(admin, 'AdminUserDetail') },
                      { path: 'actions', element: lazyRoute(admin, 'AdminActions') },
                      { path: 'lessons', element: lazyRoute(admin, 'AdminLessons') },
                    ],
                  },
                ],
              },
            ],
          },
        ],
      },
    ],
  },
]

export const router = createBrowserRouter(routes)
