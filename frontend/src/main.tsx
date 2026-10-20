import './polyfills'
import { StrictMode } from 'react'
import { createRoot, hydrateRoot } from 'react-dom/client'
import './styles/tokens.css'
import App from './app/App'

const root = document.getElementById('root')!
const app = (
  <StrictMode>
    <App />
  </StrictMode>
)
// Prerendered public pages hydrate; the app shell mounts fresh.
if (root.hasChildNodes()) hydrateRoot(root, app)
else createRoot(root).render(app)
