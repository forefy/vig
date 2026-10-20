import { useMemo } from 'react'
import QRCode from 'qrcode'

// A QR code drawn as one SVG path. Rendered here, never fetched.
export function Qr({ value, size = 168, className }: { value: string; size?: number; className?: string }) {
  const { n, d } = useMemo(() => {
    const code = QRCode.create(value, { errorCorrectionLevel: 'M' })
    const n = code.modules.size
    const data = code.modules.data
    let d = ''
    for (let y = 0; y < n; y++) {
      for (let x = 0; x < n; x++) {
        if (data[y * n + x]) d += `M${x} ${y}h1v1h-1z`
      }
    }
    return { n, d }
  }, [value])
  return (
    <svg viewBox={`0 0 ${n} ${n}`} width={size} height={size} shapeRendering="crispEdges" role="img" aria-label="QR" className={className}>
      <rect width={n} height={n} fill="#fff" />
      <path d={d} fill="#000" />
    </svg>
  )
}
