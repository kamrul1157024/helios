import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'

import { App } from './app.tsx'
// The code theme first: the app's own sheet overrides pieces of it.
import './hljs-vars.css'
import './styles.css'

const container = document.getElementById('root')
if (!container) throw new Error('missing #root')

// Marks the document while nothing is on screen, which is what pauses the
// stylesheet's animations. A spinner left running behind a hidden window still
// costs a full-window recomposite every frame.
const markAtRest = (): void => {
  document.documentElement.classList.toggle('at-rest', document.visibilityState !== 'visible')
}
markAtRest()
document.addEventListener('visibilitychange', markAtRest)

// No StrictMode double-invoke in production, but in development it is what
// catches an effect that leaks a terminal connection on remount.
createRoot(container).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
