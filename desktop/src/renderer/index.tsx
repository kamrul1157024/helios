import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClientProvider } from '@tanstack/react-query'

import { App } from './app.tsx'
import { DesktopWindow } from './components/desktop.tsx'
import { queryClient } from './query-client.ts'
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

// Half a hertz, and only while a dot is actually on screen: each toggle
// damages the window once, where an animation damages it every frame.
const BLINK_INTERVAL = 1_000
setInterval(() => {
  if (document.visibilityState !== 'visible') return
  if (!document.querySelector('.pulse')) return
  document.documentElement.classList.toggle('dim-pulse')
}, BLINK_INTERVAL)

// A popped-out desktop is this same page, told which host it is for. It gets
// the view and nothing else: no store, no sidebar, no terminals.
const desktopHost = new URLSearchParams(window.location.search).get('desktop')

// No StrictMode double-invoke in production, but in development it is what
// catches an effect that leaks a terminal connection on remount.
createRoot(container).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      {desktopHost ? <DesktopWindow hostId={desktopHost} /> : <App />}
    </QueryClientProvider>
  </StrictMode>,
)
