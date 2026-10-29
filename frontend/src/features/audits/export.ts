import { toast } from 'sonner'
import { api } from '@/api/client'

// The report leaves the page as a file: the findings document as JSON, or
// on paper as a PDF the server draws from that same document. The owner's
// come by audit id; a public report's PDF by its slug.
export async function downloadJSON(id: string, repo: string) {
  const res = await api.GET('/v1/audits/{id}/report.json', { params: { path: { id } } })
  if (!res.data) return toast.error('Export failed')
  save(new Blob([JSON.stringify(res.data, null, 2)], { type: 'application/json' }), `${repo.replace('/', '-')}-findings.json`)
}

// The report on paper, drawn by the server from the same document the page
// shows: the owner's by audit id, a public one by its slug.
export async function downloadPDF(id: string, repo: string) {
  const res = await api.GET('/v1/audits/{id}/report.pdf', { params: { path: { id } }, parseAs: 'blob' })
  if (!res.data) return toast.error('Export failed')
  save(res.data, `${repo.replace('/', '-')}-findings.pdf`)
}

export async function downloadPublicPDF(slug: string, repo: string) {
  const res = await api.GET('/v1/reports/{slug}/report.pdf', { params: { path: { slug } }, parseAs: 'blob' })
  if (!res.data) return toast.error('Export failed')
  save(res.data, `${repo.replace('/', '-')}-findings.pdf`)
}

function save(blob: Blob, name: string) {
  const a = document.createElement('a')
  a.href = URL.createObjectURL(blob)
  a.download = name
  a.click()
  URL.revokeObjectURL(a.href)
}
