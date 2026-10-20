import { useEffect, useState, type RefObject } from 'react'

import { highlightCode } from './hljs-core.ts'

/**
 * Highlighting, off the window's thread (spec 65, 2.6).
 *
 * Every request goes to one worker and comes back as one HTML string per text.
 * The last answers are kept, keyed by language and a hash of the texts, so a
 * diff shown again — a tab switched back to, a panel remounted — is coloured at
 * once rather than after a round trip.
 *
 * Where a worker cannot be had, the same call highlights in place. Slower, and
 * still correct, which is the order those two belong in.
 */

/** How many highlighted blocks are kept. A block is a whole diff or one fence. */
const CACHE_LIMIT = 50

const cache = new Map<string, string[]>()
const waiting = new Map<number, (html: string[]) => void>()
let nextId = 1
let worker: Worker | null | undefined

function theWorker(): Worker | null {
  if (worker !== undefined) return worker
  try {
    worker = new Worker(new URL('highlight-worker.js', document.baseURI))
    worker.onmessage = (event: MessageEvent<{ id: number; html: string[] }>) => {
      waiting.get(event.data.id)?.(event.data.html)
      waiting.delete(event.data.id)
    }
    // A worker that fails to load says so once, here. Everything still in
    // flight is answered in place, and nothing is sent to it again.
    worker.onerror = () => {
      worker = null
      for (const [, resolve] of waiting) resolve([])
      waiting.clear()
    }
  } catch {
    worker = null
  }
  return worker
}

/** FNV-1a over the text, with its length, which is plenty for a cache key. */
function hash(texts: readonly string[]): string {
  let h = 0x811c9dc5
  let length = 0
  for (const text of texts) {
    length += text.length + 1
    for (let i = 0; i < text.length; i++) {
      h ^= text.charCodeAt(i)
      h = Math.imul(h, 0x01000193)
    }
    h ^= 10
    h = Math.imul(h, 0x01000193)
  }
  return `${(h >>> 0).toString(36)}:${length}`
}

function remember(key: string, html: string[]): void {
  cache.delete(key)
  cache.set(key, html)
  while (cache.size > CACHE_LIMIT) {
    const oldest = cache.keys().next().value
    if (oldest === undefined) break
    cache.delete(oldest)
  }
}

/** The cached answer, when there is one. Moves it to the front if so. */
export function highlightedNow(language: string, texts: readonly string[]): string[] | null {
  const key = `${language}\u0000${hash(texts)}`
  const held = cache.get(key)
  if (held) remember(key, held)
  return held ?? null
}

/** One HTML string per text, from the worker or, failing that, from here. */
export function highlightTexts(language: string, texts: readonly string[]): Promise<string[]> {
  const key = `${language}\u0000${hash(texts)}`
  const held = cache.get(key)
  if (held) return Promise.resolve(held)
  const inPlace = (): string[] => texts.map((text) => highlightCode(text, language))
  const target = theWorker()
  if (!target) {
    const html = inPlace()
    remember(key, html)
    return Promise.resolve(html)
  }
  const id = nextId++
  return new Promise((resolve) => {
    waiting.set(id, (html) => {
      const answer = html.length === texts.length ? html : inPlace()
      remember(key, answer)
      resolve(answer)
    })
    target.postMessage({ id, language, texts })
  })
}

/**
 * The texts, highlighted, or null until the answer is in. The caller draws
 * plain text meanwhile, which is readable at once and coloured a moment later.
 */
export function useHighlighted(language: string | null | undefined, texts: readonly string[] | null): string[] | null {
  const [state, setState] = useState<{ texts: readonly string[]; html: string[] } | null>(null)
  useEffect(() => {
    if (!language || !texts) return
    let live = true
    void highlightTexts(language, texts).then((html) => {
      if (live) setState({ texts, html })
    })
    return () => {
      live = false
    }
  }, [language, texts])
  if (!language || !texts) return null
  if (state?.texts === texts) return state.html
  return highlightedNow(language, texts)
}

/**
 * Colours the deferred code fences inside an element: every `code[data-hl]`
 * the markdown renderer left plain. `revision` is whatever changes when the
 * markup under the element is replaced.
 */
export function useFenceHighlight(container: RefObject<HTMLElement | null>, revision: unknown): void {
  useEffect(() => {
    const host = container.current
    if (!host) return
    let live = true
    for (const code of host.querySelectorAll<HTMLElement>('code[data-hl]')) {
      const language = code.dataset.hl ?? ''
      const text = code.textContent ?? ''
      void highlightTexts(language, [text]).then(([html]) => {
        // The highlighter escapes everything it does not colour, so its markup
        // is no wider than the text it was handed.
        if (!live || html === undefined || code.textContent !== text) return
        code.innerHTML = html
        code.removeAttribute('data-hl')
      })
    }
    return () => {
      live = false
    }
  }, [container, revision])
}
