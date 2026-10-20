import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'node:path'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: { alias: { '@': path.resolve(__dirname, 'src') } },
  ssr: { noExternal: ['react-helmet-async'] },
  server: {
    port: Number(process.env.PORT) || 7111,
    strictPort: true,
    proxy: { '/v1': 'http://localhost:7112', '/openapi.json': 'http://localhost:7112', '/docs': 'http://localhost:7112', '/llms.txt': { target: 'http://localhost:7112', changeOrigin: false } },
  },
})
