import type { FileContent, RawFile } from '../shared/models.ts'

/**
 * A raw read, turned into the shape the cache has always held.
 *
 * The cache entry stays a `FileContent` so that everything reading it — the
 * editor, the dirty check, a tool row finding its edit — keeps working without
 * knowing how the bytes travelled. Text is decoded here with `TextDecoder`;
 * anything else keeps its bytes as they are, for a Blob URL, with no base64 in
 * either direction.
 *
 * A 304 means the bytes in hand are still the bytes on disk, so the held entry
 * is the answer. Returned as the same object, which is what lets React Query
 * see that nothing changed and skip the render.
 *
 * Free of the bridge, so the decoding can be asserted directly.
 */
export function contentFromRaw(path: string, raw: RawFile, held?: FileContent): FileContent {
  if (raw.status === 304 && held) return held
  const base = { path, size: raw.size || raw.body.byteLength, mod_time: raw.modTime, etag: raw.etag }
  if (raw.encoding === 'binary') return { ...base, content: '', encoding: 'binary', bytes: raw.body }
  return { ...base, content: new TextDecoder('utf-8').decode(raw.body), encoding: 'utf8' }
}

/**
 * What to compare two reads of one file by, for the rule that decides whether
 * an event means anything to the tab showing it (`fileEffectFor`).
 *
 * Text compares as text, which is what drops the echo of this window's own
 * save. Bytes held raw have no text to compare, so their validator stands in:
 * a binary file that changed has a new size or mod time, and so a new ETag.
 */
export function comparable(content: FileContent): string {
  if (content.encoding !== 'binary') return content.content
  return `binary:${content.etag ?? `${content.size}-${content.mod_time}`}`
}
