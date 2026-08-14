import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'

import { App } from './app.tsx'
// The code theme first: the app's own sheet overrides pieces of it.
import './hljs-vars.css'
import './styles.css'

const container = document.getElementById('root')
if (!container) throw new Error('missing #root')

// No StrictMode double-invoke in production, but in development it is what
// catches an effect that leaks a terminal connection on remount.
createRoot(container).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
