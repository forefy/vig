import { Helmet } from 'react-helmet-async'

// Site origin for absolute URLs in canonical and OpenGraph tags. Set at build
// time; empty in dev, where relative URLs are fine.
export const SITE_URL = (import.meta.env.VITE_SITE_URL ?? '').replace(/\/$/, '')

type Props = {
  title: string
  description?: string
  path: string
  image?: string
  type?: 'website' | 'article'
  noindex?: boolean
}

// Every public page declares its head through this so titles, canonicals and
// cards are shaped the same way. The prerender step hoists these into <head>.
export function Seo({ title, description, path, image, type = 'website', noindex }: Props) {
  const url = SITE_URL + path
  const img = image ? (image.startsWith('http') ? image : SITE_URL + image) : SITE_URL + '/og/default.png'
  return (
    <Helmet>
      <title>{title}</title>
      {description && <meta name="description" content={description} />}
      <link rel="canonical" href={url} />
      {noindex && <meta name="robots" content="noindex" />}
      <meta property="og:type" content={type} />
      <meta property="og:title" content={title} />
      {description && <meta property="og:description" content={description} />}
      <meta property="og:url" content={url} />
      <meta property="og:image" content={img} />
      <meta name="twitter:card" content="summary_large_image" />
      <meta name="twitter:title" content={title} />
      {description && <meta name="twitter:description" content={description} />}
      <meta name="twitter:image" content={img} />
    </Helmet>
  )
}
