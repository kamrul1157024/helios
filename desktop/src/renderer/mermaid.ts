// Drawing the diagram fences that `markdown.ts` marked.
//
// Mermaid lays a diagram out by measuring the text it contains, so it needs a
// document and cannot run where the markdown is turned into HTML. It runs here
// instead, over the DOM that HTML was injected into — the same shape as
// `useInlineImages` in html-preview.tsx, which fills in images the same way and
// for the same reason.
//
// Nothing is fetched. Mermaid's default fonts are the local stack and the build
// asks for no resources, which is what lets this work under `connect-src
// 'none'`. Its inline `<style>` is covered by the window's `style-src
// 'unsafe-inline'`.

import DOMPurify from 'dompurify'
import type { Mermaid } from 'mermaid'
import { useEffect, type RefObject } from 'react'

import { bridge } from './bridge.ts'

/** Marks a fence that has been dealt with, drawn or not. */
const DONE = 'data-mermaid-done'

let loading: Promise<Mermaid | null> | null = null
let counter = 0

/** Mermaid's palette, taken from whichever theme the app is wearing. */
function themeVariables(): Record<string, string> {
  const style = getComputedStyle(document.documentElement)
  const read = (name: string): string => style.getPropertyValue(name).trim()
  return {
    background: read('--surface-container'),
    primaryColor: read('--surface-high'),
    primaryTextColor: read('--on-surface'),
    primaryBorderColor: read('--outline-variant'),
    secondaryColor: read('--surface-low'),
    tertiaryColor: read('--surface-container'),
    lineColor: read('--primary'),
    textColor: read('--on-surface'),
    fontSize: '14px',
  }
}

function configure(mermaid: Mermaid): void {
  mermaid.initialize({
    startOnLoad: false,
    // Labels are an agent's output, so they are markup until proven otherwise.
    securityLevel: 'strict',
    // A fence that will not parse keeps its code block; mermaid's own error
    // diagram would replace prose the reader can still use with a picture of a
    // complaint.
    suppressErrorRendering: true,
    theme: 'base',
    themeVariables: themeVariables(),
    fontFamily: 'inherit',
    // Labels as SVG text rather than HTML in a `<foreignObject>`, which is what
    // mermaid reaches for by default. DOMPurify does not allow that element —
    // it is a classic mXSS vector — so without this the sanitiser drops every
    // label in a flowchart and the diagram arrives as a set of empty boxes.
    //
    // Both keys, and measured that way: `flowchart.htmlLabels` alone is
    // ignored by the renderer mermaid 11 actually uses, and the top-level one
    // is what turns the foreign objects off.
    htmlLabels: false,
    flowchart: { htmlLabels: false },
  })
}

/**
 * The library, fetched the first time a diagram needs it.
 *
 * A sibling script under the window's `script-src 'self'`, the same way
 * index.html loads the renderer itself. Once loaded it is configured, and it
 * follows the theme for as long as the window lives.
 */
function load(): Promise<Mermaid | null> {
  loading ??= new Promise<Mermaid | null>((done) => {
    const script = document.createElement('script')
    script.src = 'mermaid.js'
    script.onload = () => {
      const mermaid = (window as { heliosMermaid?: { default: Mermaid } }).heliosMermaid?.default
      if (!mermaid) {
        done(null)
        return
      }
      configure(mermaid)
      bridge.theme.onChanged(() => {
        configure(mermaid)
        void redrawAll()
      })
      done(mermaid)
    }
    // Nothing to fall back to, and a fence that is never drawn is still a
    // readable code block.
    script.onerror = () => done(null)
    document.head.appendChild(script)
  })
  return loading
}

/**
 * A place with layout for mermaid to measure in, off the side of the window.
 *
 * `getBBox` returns zeroes for an element that is not rendered, so the diagram
 * cannot be built in a detached node.
 */
function stage(): HTMLDivElement {
  const host = document.createElement('div')
  host.style.cssText = 'position:absolute;left:-99999px;top:0;visibility:hidden'
  document.body.appendChild(host)
  return host
}

/** The SVG for a diagram, or null if the source is not one. */
async function draw(mermaid: Mermaid, source: string, host: HTMLDivElement): Promise<string | null> {
  try {
    const { svg } = await mermaid.render(`mermaid-${++counter}`, source, host)
    return DOMPurify.sanitize(svg, { USE_PROFILES: { svg: true, svgFilters: true, html: true } })
  } catch {
    return null
  }
}

/**
 * Draws every fence inside `container` that has not been drawn yet.
 *
 * A drawn fence becomes a `<figure>` holding the SVG and the source it came
 * from, so a theme change can redraw it. One that fails is marked and left as
 * the code block it already is.
 */
export async function renderDiagrams(container: HTMLElement): Promise<void> {
  const pending = [...container.querySelectorAll<HTMLElement>(`pre.mermaid-fence:not([${DONE}])`)]
  if (pending.length === 0) return
  const mermaid = await load()
  if (!mermaid) return

  const host = stage()
  try {
    for (const fence of pending) {
      fence.setAttribute(DONE, '')
      const source = fence.textContent ?? ''
      const svg = await draw(mermaid, source, host)
      if (!svg) continue
      const figure = document.createElement('figure')
      figure.className = 'mermaid'
      figure.dataset.mermaid = source
      figure.innerHTML = svg
      fence.replaceWith(figure)
    }
  } finally {
    host.remove()
  }
}

/** Redraws what is already on screen, after the palette underneath it moved. */
async function redrawAll(): Promise<void> {
  const drawn = [...document.querySelectorAll<HTMLElement>('figure.mermaid[data-mermaid]')]
  if (drawn.length === 0) return
  const mermaid = await load()
  if (!mermaid) return
  const host = stage()
  try {
    for (const figure of drawn) {
      const svg = await draw(mermaid, figure.dataset.mermaid ?? '', host)
      if (svg) figure.innerHTML = svg
    }
  } finally {
    host.remove()
  }
}

/**
 * Draws the diagrams in a rendered block of markdown.
 *
 * `revision` is whatever changing means the HTML was replaced — the rendered
 * string, or the block list — as `useInlineImages` takes it.
 */
export function useMermaid(container: RefObject<HTMLElement | null>, revision: unknown): void {
  useEffect(() => {
    const host = container.current
    if (host) void renderDiagrams(host)
  }, [container, revision])
}
