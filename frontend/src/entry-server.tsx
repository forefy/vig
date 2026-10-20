import { renderToString } from 'react-dom/server'
import { createStaticHandler, createStaticRouter, StaticRouterProvider } from 'react-router'
import { HelmetProvider, type HelmetServerState } from 'react-helmet-async'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TooltipProvider } from '@/components/ui/tooltip'
import { publicRoutes } from './app/publicRoutes'

// Prerender entry: public routes only, no wallet, no session. Output is static
// HTML with the page's own head tags; the client hydrates on load.
export async function render(url: string): Promise<{ html: string; head: string }> {
  const handler = createStaticHandler(publicRoutes)
  const context = await handler.query(new Request(`http://prerender${url}`))
  if (context instanceof Response) throw new Error(`redirect for ${url}`)
  const router = createStaticRouter(handler.dataRoutes, context)
  const helmet: { helmet?: HelmetServerState } = {}
  const html = renderToString(
    <HelmetProvider context={helmet}>
      <QueryClientProvider client={new QueryClient()}>
        <TooltipProvider>
          <StaticRouterProvider router={router} context={context} />
        </TooltipProvider>
      </QueryClientProvider>
    </HelmetProvider>,
  )
  const h = helmet.helmet
  const head = h ? [h.title.toString(), h.meta.toString(), h.link.toString()].join('\n') : ''
  return { html, head }
}
