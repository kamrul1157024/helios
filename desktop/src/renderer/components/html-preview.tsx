// An HTML file, shown as the page it is.
//
// The renderer has no network of its own — `connect-src 'none'` — so a page
// that points at `./chart.png` gets its chart only because this reads that file
// over IPC and inlines it. What arrives is an agent's output, which is to say
// untrusted, so it is rendered inside a frame that cannot do anything.

import { useEffect, useState, type RefObject } from 'react'
import { useQueryClient } from '@tanstack/react-query'

import { bridge } from '../bridge.ts'
import { dataUrl, mimeForPath } from '../filetype.ts'
import { fileAssetQuery } from '../queries.ts'
import { MAX_TOTAL_BYTES, planAssets, type AssetRef } from '../preview.ts'

/** How many reads are in flight at once. Serial would be a page at a time. */
const LANES = 6

/**
 * Renders the page with what it references pulled in.
 *
 * Deliberately not sanitised. The frame it goes into withholds every
 * capability there is, and Chromium builds no JavaScript execution context for
 * such a frame at all — a raw `<script>` in a `sandbox=""` srcdoc does not run,
 * which was measured before this was written. Running DOMPurify over it as well
 * bought nothing and cost the feature: it strips `<style>`, which is precisely
 * the thing a page needs to look like itself.
 *
 * The sanitiser still belongs where markdown is injected into the app's own
 * DOM. Here there is a frame instead, and the frame is stronger.
 */
export function HtmlPreview({
  hostId,
  html,
  path,
  root,
  scripts,
}: {
  hostId: string
  html: string
  path: string
  root: string
  /** Whether the reader has asked for the page's scripts to run. */
  scripts: boolean
}): JSX.Element {
  const client = useQueryClient()
  const [frame, setFrame] = useState<{ srcDoc?: string; src?: string } | null>(null)

  useEffect(() => {
    let cancelled = false

    void (async () => {
      const doc = new DOMParser().parseFromString(html, 'text/html')

      const images = [...doc.querySelectorAll('img[src]')]
      const sheets = [...doc.querySelectorAll('link[rel~="stylesheet"][href]')]
      // A local script is only worth fetching if it is going to be allowed to
      // run; otherwise it is dead weight in the document.
      const code = scripts ? [...doc.querySelectorAll('script[src]')] : []
      const refs: AssetRef[] = [
        ...images.map((el) => ({ kind: 'img' as const, href: el.getAttribute('src') ?? '' })),
        ...sheets.map((el) => ({ kind: 'style' as const, href: el.getAttribute('href') ?? '' })),
        ...code.map((el) => ({ kind: 'script' as const, href: el.getAttribute('src') ?? '' })),
      ]

      const planned = planAssets(refs, path, root)
      const loaded = new Map<string, { content: string; base64: boolean }>()
      let spent = 0

      for (let at = 0; at < planned.length; at += LANES) {
        const batch = planned.slice(at, at + LANES)
        const answers = await Promise.all(
          batch.map(async (asset) => {
            try {
              return { asset, file: await client.fetchQuery(fileAssetQuery(hostId, asset.path)) }
            } catch {
              // A page that names a file the agent has since deleted still
              // renders; the reference simply stays unresolved.
              return null
            }
          }),
        )
        if (cancelled) return
        for (const answer of answers) {
          if (!answer) continue
          spent += answer.file.content.length
          if (spent > MAX_TOTAL_BYTES) break
          loaded.set(answer.asset.href, {
            content: answer.file.content,
            base64: answer.file.encoding === 'base64',
          })
        }
      }

      for (const el of images) {
        const href = el.getAttribute('src') ?? ''
        const asset = loaded.get(href)
        if (!asset) continue
        const resolved = planned.find((one) => one.href === href && one.kind === 'img')
        if (!resolved) continue

        if (asset.base64) {
          el.setAttribute('src', dataUrl(resolved.path, asset.content))
          continue
        }
        // Not base64, so it arrived as text. An SVG is text and can be encoded
        // here; anything else is a picture a daemon older than the encoding
        // parameter has already lost to the JSON encoder, and inlining that
        // would draw a broken image where the reference could just be left as
        // it was.
        if (mimeForPath(resolved.path) === 'image/svg+xml') {
          el.setAttribute('src', `data:image/svg+xml;base64,${base64Of(asset.content)}`)
        }
      }

      // A stylesheet becomes a <style>, not a data URL: the frame inherits the
      // app's `style-src 'self' 'unsafe-inline'`, which has no `data:`, so a
      // linked one would silently not apply. Measured, not assumed.
      for (const el of sheets) {
        const asset = loaded.get(el.getAttribute('href') ?? '')
        if (!asset || asset.base64) continue
        const style = doc.createElement('style')
        style.textContent = asset.content
        el.replaceWith(style)
      }

      // A local script becomes an inline one. The policy the page is served
      // under allows 'unsafe-inline' and nothing else, so a src= would not
      // load even from the preview scheme.
      for (const el of code) {
        const asset = loaded.get(el.getAttribute('src') ?? '')
        if (!asset || asset.base64) continue
        const inline = doc.createElement('script')
        inline.textContent = asset.content
        el.replaceWith(inline)
      }

      // Anything still pointing off the machine is dropped. With scripts off
      // the frame could not fetch it anyway; with them on this is what keeps
      // that true, alongside the policy the page is served under.
      for (const el of doc.querySelectorAll('[src], [href]')) {
        for (const name of ['src', 'href']) {
          const value = el.getAttribute(name)
          if (value && /^[a-z][a-z0-9+.-]*:/i.test(value) && !value.startsWith('data:')) {
            el.removeAttribute(name)
          }
        }
      }

      // <base> would rewrite every relative URL the resolver above did not
      // inline, which is a correctness problem rather than a safety one: the
      // frame cannot reach the network either way.
      for (const base of doc.querySelectorAll('base')) base.remove()

      const page = doc.documentElement.outerHTML
      if (cancelled) return
      // Scripts off is the ordinary path and stays a srcdoc: it inherits the
      // window's policy, which forbids everything, and costs nothing. Scripts
      // on needs a document of its own — see stagePreview in the main process.
      setFrame(scripts ? { src: await bridge.preview.stage(page) } : { srcDoc: page })
    })()

    return () => {
      cancelled = true
    }
  }, [client, hostId, html, path, root, scripts])

  if (frame === null) {
    return (
      <div className="ws-html loading">
        <span className="spinner" />
      </div>
    )
  }

  return (
    <div className="ws-html">
      {/* `allow-same-origin` is never set, with or without scripts. The parent
          is a file:// document, and granting the frame the parent's origin
          would hand markup an agent wrote the same origin as the app itself.
          With scripts off the sandbox is empty and the frame gets no execution
          context at all. */}
      <iframe
        title="Preview"
        key={frame.src ?? 'srcdoc'}
        {...(frame.src ? { src: frame.src } : { srcDoc: frame.srcDoc })}
        sandbox={scripts ? 'allow-scripts' : ''}
        referrerPolicy="no-referrer"
      />
    </div>
  )
}

/**
 * Fills in the images of a rendered markdown document.
 *
 * The renderer emits `data-src` and no `src`, because a relative path resolves
 * against nothing here and would paint a broken image before it was tried. This
 * is the other half: it knows which file the prose came from, so it can turn
 * `./diagram.png` into the bytes on disk.
 *
 * Runs against the mounted DOM rather than the HTML string, so it shares the
 * resolver and the caps with the page preview above and nothing has to parse
 * markup twice.
 */
export function useInlineImages(
  container: RefObject<HTMLElement | null>,
  { hostId, basePath, root, revision }: { hostId: string; basePath: string; root: string; revision: unknown },
): void {
  const client = useQueryClient()

  useEffect(() => {
    const host = container.current
    if (!host) return
    const pending = [...host.querySelectorAll('img[data-src]')].filter(
      (el): el is HTMLImageElement => el instanceof HTMLImageElement && !el.getAttribute('src'),
    )
    if (pending.length === 0) return

    let cancelled = false
    void (async () => {
      const planned = planAssets(
        pending.map((el) => ({ kind: 'img' as const, href: el.dataset.src ?? '' })),
        basePath,
        root,
      )
      const byHref = new Map(planned.map((asset) => [asset.href, asset]))

      for (let at = 0; at < pending.length; at += LANES) {
        const batch = pending.slice(at, at + LANES)
        await Promise.all(
          batch.map(async (el) => {
            const asset = byHref.get(el.dataset.src ?? '')
            if (!asset) return
            try {
              const file = await client.fetchQuery(fileAssetQuery(hostId, asset.path))
              if (cancelled) return
              if (file.encoding === 'base64') {
                el.setAttribute('src', dataUrl(asset.path, file.content))
              } else if (mimeForPath(asset.path) === 'image/svg+xml') {
                el.setAttribute('src', `data:image/svg+xml;base64,${base64Of(file.content)}`)
              }
            } catch {
              // The agent deletes files. The alt text is the fallback.
            }
          }),
        )
        if (cancelled) return
      }
    })()

    return () => {
      cancelled = true
    }
  }, [client, container, hostId, basePath, root, revision])
}

/** btoa refuses anything above U+00FF, and an SVG is often full of them. */
function base64Of(text: string): string {
  const bytes = new TextEncoder().encode(text)
  let binary = ''
  for (const byte of bytes) binary += String.fromCharCode(byte)
  return btoa(binary)
}
