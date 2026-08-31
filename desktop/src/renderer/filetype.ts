// What a file is, from its name.
//
// By extension rather than by sniffing the bytes, because the answer is needed
// before there are any: a tab picks its view when it opens, from the path
// alone. Sniffing also cannot tell an .html from a .txt, which is the case that
// matters most here.
//
// The mirror of this lives in the mobile app. Two small maps that agree beat a
// field on the wire that has to be kept in step with both.

/** Formats a browser can draw, and the mime each needs in a data URL. */
const IMAGE_MIME: Record<string, string> = {
  apng: 'image/apng',
  avif: 'image/avif',
  bmp: 'image/bmp',
  gif: 'image/gif',
  ico: 'image/x-icon',
  jpeg: 'image/jpeg',
  jpg: 'image/jpeg',
  png: 'image/png',
  svg: 'image/svg+xml',
  webp: 'image/webp',
}

const HTML_EXTENSIONS = new Set(['html', 'htm', 'xhtml'])

/** How a file is shown: as a picture, as a page, or as text in the editor. */
export type FileKind = 'image' | 'html' | 'text'

/** Lowercased, and empty for a dotfile or a name with no dot in it. */
export function extensionOf(path: string): string {
  const name = path.slice(path.lastIndexOf('/') + 1)
  const dot = name.lastIndexOf('.')
  // `> 0` rather than `>= 0`: `.gitignore` is a name, not an extension.
  return dot > 0 ? name.slice(dot + 1).toLowerCase() : ''
}

export function isImagePath(path: string): boolean {
  return extensionOf(path) in IMAGE_MIME
}

export function isHtmlPath(path: string): boolean {
  return HTML_EXTENSIONS.has(extensionOf(path))
}

export function kindOf(path: string): FileKind {
  if (isImagePath(path)) return 'image'
  if (isHtmlPath(path)) return 'html'
  return 'text'
}

/**
 * The mime for a data URL. Only the ones a preview inlines are named; anything
 * else is served as bytes, which is what a browser assumes anyway.
 */
export function mimeForPath(path: string): string {
  const extension = extensionOf(path)
  if (extension in IMAGE_MIME) return IMAGE_MIME[extension] as string
  if (HTML_EXTENSIONS.has(extension)) return 'text/html'
  if (extension === 'css') return 'text/css'
  return 'application/octet-stream'
}

/** An image the renderer can show without leaving the page. */
export function dataUrl(path: string, base64: string): string {
  return `data:${mimeForPath(path)};base64,${base64}`
}
