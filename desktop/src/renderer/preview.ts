// Turning the links inside a preview into files on disk.
//
// A preview cannot fetch anything: the renderer has `connect-src 'none'` and no
// network of its own, so a page that says `<img src="./chart.png">` gets its
// chart only if the panel reads that file and inlines it. This module decides
// which references are allowed to become a read, and how many.
//
// Pure on purpose — the test runner cannot load anything that reaches
// `window.helios`, and the interesting cases here are the ones that must be
// refused. The DOM walking lives with the component.

/** Where a reference was found and what it asked for. */
export interface AssetRef {
  /**
   * 'img' becomes a data URL, 'style' an inline <style>, and 'script' an
   * inline <script> — the last only when the reader has turned scripts on.
   */
  kind: 'img' | 'style' | 'script'
  /** The href or src exactly as written in the document. */
  href: string
}

/** A reference that resolved to a file worth reading. */
export interface PlannedAsset {
  kind: 'img' | 'style' | 'script'
  href: string
  /** Absolute, inside the root, and safe to ask the daemon for. */
  path: string
}

/**
 * How much a preview may pull in behind the file the user opened.
 *
 * Opening one file should not turn into a hundred reads. The caps are generous
 * for a report and mean nothing for a hand-written page.
 */
export const MAX_ASSETS = 24
export const MAX_TOTAL_BYTES = 8 * 1024 * 1024

/** Absolute, and free of `.` and `..`. Not `path.posix`, which is Node's. */
function normalise(path: string): string {
  const out: string[] = []
  for (const part of path.split('/')) {
    if (part === '' || part === '.') continue
    if (part === '..') {
      out.pop()
      continue
    }
    out.push(part)
  }
  return '/' + out.join('/')
}

/** Whether `path` is `root` or sits underneath it. */
export function withinRoot(root: string, path: string): boolean {
  const base = normalise(root)
  if (path === base) return true
  return path.startsWith(base === '/' ? '/' : `${base}/`)
}

/**
 * The file a reference points at, or null if it must not be read.
 *
 * Refused: anything with a scheme, protocol-relative `//host`, bare fragments
 * and queries, and — the one that matters — anything that climbs out of the
 * root with `..`. A preview is allowed to read the checkout it belongs to and
 * nothing else, and `../../../.ssh/id_rsa` is exactly the request this exists
 * to turn down.
 */
export function resolveAsset(basePath: string, href: string, root: string): string | null {
  const raw = href.trim()
  if (!raw) return null
  if (raw.startsWith('#')) return null
  if (raw.startsWith('//')) return null
  // A scheme, including data:, http: and file:. Matched before anything else
  // so a windows-style `c:\` is refused here rather than treated as relative.
  if (/^[a-z][a-z0-9+.-]*:/i.test(raw)) return null

  // The query and fragment belong to a server, and there is not one.
  const clean = raw.split(/[?#]/)[0] ?? ''
  if (!clean) return null

  let decoded = clean
  try {
    decoded = decodeURIComponent(clean)
  } catch {
    // A stray % is not worth refusing the file over; use it as written.
  }

  // A leading slash in a page means the site root, and there is no site. The
  // checkout is the nearest honest reading — and it is also the safe one, since
  // treating it as the filesystem would make `<img src="/etc/passwd">` a read
  // request that only the check below turns down.
  const base = basePath.slice(0, basePath.lastIndexOf('/'))
  const joined = decoded.startsWith('/') ? `${root}/${decoded}` : `${base}/${decoded}`
  const path = normalise(joined)

  if (!withinRoot(root, path)) return null
  if (path === normalise(basePath)) return null
  return path
}

/**
 * Which references to read, in order, within the caps.
 *
 * Deduped by path: a page that uses one icon forty times is one read. The size
 * cap cannot be applied here — nothing has been read yet — so it is the
 * caller's, and `MAX_TOTAL_BYTES` is exported for it.
 */
export function planAssets(refs: AssetRef[], basePath: string, root: string): PlannedAsset[] {
  const seen = new Set<string>()
  const planned: PlannedAsset[] = []

  for (const ref of refs) {
    if (planned.length >= MAX_ASSETS) break
    const path = resolveAsset(basePath, ref.href, root)
    if (!path) continue
    const key = `${ref.kind}:${path}`
    if (seen.has(key)) continue
    seen.add(key)
    planned.push({ kind: ref.kind, href: ref.href, path })
  }

  return planned
}
