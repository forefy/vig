import { Link } from 'react-router'
import { Empty } from '@/components/primitives'
import { Seo } from '@/components/primitives/Seo'
import { Button } from '@/components/ui/button'

// The 404: the mark, one line, one action. Rendered inside the public shell.
export function NotFound() {
  return (
    <div className="mx-auto max-w-[560px] px-6 py-16 md:px-10">
      <Seo title="Not found" path="/404" noindex />
      <Empty
        title="Nothing at this address"
        action={
          <Button variant="outline" size="sm" render={<Link to="/" />}>
            Home
          </Button>
        }
      />
    </div>
  )
}
