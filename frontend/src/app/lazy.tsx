import { lazy, Suspense, type ComponentType } from 'react'
import { Loading } from '@/features/auth/guards'

// Code-split a route element. The fallback is the mark rotating: something is
// running, so it may move.
export function lazyRoute<M extends Record<K, ComponentType>, K extends keyof M & string>(loader: () => Promise<M>, name: K) {
  const C = lazy(async () => ({ default: (await loader())[name] as ComponentType }))
  return (
    <Suspense fallback={<Loading />}>
      <C />
    </Suspense>
  )
}
