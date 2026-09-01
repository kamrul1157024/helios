// Transcript rendering: markdown, syntax highlighting and file-path detection.
//
// This mirrors what the mobile app does in lib/widgets/message_card.dart —
// MarkdownBody with atom-one-dark code blocks, and file paths lifted out of the
// prose as chips that open the file. Keeping the two in step matters more than
// the implementation, so the rules below follow that file deliberately.

import DOMPurify from 'dompurify'
import type { LanguageFn } from 'highlight.js'
import hljs from 'highlight.js/lib/core'
import { Marked } from 'marked'

import bash from 'highlight.js/lib/languages/bash'
import c from 'highlight.js/lib/languages/c'
import cpp from 'highlight.js/lib/languages/cpp'
import csharp from 'highlight.js/lib/languages/csharp'
import css from 'highlight.js/lib/languages/css'
import dart from 'highlight.js/lib/languages/dart'
import diff from 'highlight.js/lib/languages/diff'
import dockerfile from 'highlight.js/lib/languages/dockerfile'
import go from 'highlight.js/lib/languages/go'
import ini from 'highlight.js/lib/languages/ini'
import java from 'highlight.js/lib/languages/java'
import javascript from 'highlight.js/lib/languages/javascript'
import json from 'highlight.js/lib/languages/json'
import kotlin from 'highlight.js/lib/languages/kotlin'
import lua from 'highlight.js/lib/languages/lua'
import makefile from 'highlight.js/lib/languages/makefile'
import markdown from 'highlight.js/lib/languages/markdown'
import php from 'highlight.js/lib/languages/php'
import plaintext from 'highlight.js/lib/languages/plaintext'
import python from 'highlight.js/lib/languages/python'
import ruby from 'highlight.js/lib/languages/ruby'
import rust from 'highlight.js/lib/languages/rust'
import scss from 'highlight.js/lib/languages/scss'
import sql from 'highlight.js/lib/languages/sql'
import swift from 'highlight.js/lib/languages/swift'
import typescript from 'highlight.js/lib/languages/typescript'
import xml from 'highlight.js/lib/languages/xml'
import yaml from 'highlight.js/lib/languages/yaml'

// Registered by hand rather than importing highlight.js whole: the full build
// is a megabyte of languages an agent transcript will never contain.
const LANGUAGES: Record<string, LanguageFn> = {
  bash,
  c,
  cpp,
  csharp,
  css,
  dart,
  diff,
  dockerfile,
  go,
  ini,
  java,
  javascript,
  json,
  kotlin,
  lua,
  makefile,
  markdown,
  php,
  plaintext,
  python,
  ruby,
  rust,
  scss,
  sql,
  swift,
  typescript,
  xml,
  yaml,
}

for (const [name, definition] of Object.entries(LANGUAGES)) {
  hljs.registerLanguage(name, definition)
}

/** Fence tags and file extensions that are not their own language name. */
const ALIASES: Record<string, string> = {
  sh: 'bash',
  shell: 'bash',
  zsh: 'bash',
  console: 'bash',
  js: 'javascript',
  jsx: 'javascript',
  mjs: 'javascript',
  cjs: 'javascript',
  ts: 'typescript',
  tsx: 'typescript',
  py: 'python',
  rb: 'ruby',
  rs: 'rust',
  kt: 'kotlin',
  kts: 'kotlin',
  cs: 'csharp',
  'c++': 'cpp',
  cc: 'cpp',
  h: 'cpp',
  hpp: 'cpp',
  html: 'xml',
  htm: 'xml',
  svg: 'xml',
  vue: 'xml',
  yml: 'yaml',
  md: 'markdown',
  mdx: 'markdown',
  toml: 'ini',
  cfg: 'ini',
  conf: 'ini',
  env: 'ini',
  patch: 'diff',
  mk: 'makefile',
  gradle: 'java',
  psql: 'sql',
  text: 'plaintext',
  txt: 'plaintext',
}

/**
 * Whether a fence holds a diagram rather than code.
 *
 * Marked in the markup and drawn afterwards, by `mermaid.ts`, over the DOM the
 * HTML was injected into — mermaid measures text to lay a diagram out, so it
 * needs a document and cannot run here.
 */
export function isMermaidFence(tag?: string | null): boolean {
  return (tag ?? '').trim().toLowerCase().split(/[\s:,]/)[0] === 'mermaid'
}

/** Resolves a fence tag to a registered language, or null to leave it plain. */
export function normalizeLanguage(tag?: string | null): string | null {
  if (!tag) return null
  const key = tag.trim().toLowerCase().split(/[\s:,]/)[0] ?? ''
  const name = ALIASES[key] ?? key
  return hljs.getLanguage(name) ? name : null
}

/** Language for a file path, used when a tool call carries file contents. */
export function languageForPath(path: string): string | null {
  const base = path.split('/').pop() ?? path
  if (base === 'Makefile' || base === 'makefile') return 'makefile'
  if (base === 'Dockerfile') return 'dockerfile'
  const dot = base.lastIndexOf('.')
  return dot === -1 ? null : normalizeLanguage(base.slice(dot + 1))
}

/** Whether a file is prose to be rendered rather than source to be read. */
export function isMarkdownPath(path: string): boolean {
  return /\.(md|markdown|mdown|mkd)$/i.test(path)
}

export function escapeHtml(text: string): string {
  return text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
}

/** Highlighted HTML for a code body, falling back to escaped text. */
export function highlightCode(code: string, language?: string | null): string {
  const name = normalizeLanguage(language)
  if (!name) return escapeHtml(code)
  try {
    return hljs.highlight(code, { language: name, ignoreIllegals: true }).value
  } catch {
    // A malformed grammar match should cost the colours, not the content.
    return escapeHtml(code)
  }
}

/**
 * Two parsers, differing only in what a lone newline means.
 *
 * An agent writing into a transcript ends a line because it meant to end one,
 * so `breaks` honours it. A file on disk is hard-wrapped by its author at
 * whatever column they favour, and honouring those would print the document at
 * the width it was typed at rather than the width it is read at — GitHub
 * reflows them, and so does the preview.
 */
function parser(breaks: boolean): Marked {
  return new Marked({
    gfm: true,
    breaks,
    renderer: {
      code({ text, lang }): string {
        // A diagram keeps the code block it would have had. Drawing replaces
        // it; a fence that will not parse is left exactly as it stands, which
        // is the fallback and costs nothing to arrange.
        if (isMermaidFence(lang)) {
          return `<pre class="code-block mermaid-fence"><code class="hljs">${escapeHtml(text)}</code></pre>`
        }
        const name = normalizeLanguage(lang)
        return (
          `<pre class="code-block"><code class="hljs${name ? ` language-${name}` : ''}">` +
          `${highlightCode(text, name)}</code></pre>`
        )
      },
      link(token): string {
        // The renderer has no network of its own and never navigates: an
        // external link is handed to the OS browser by the main process's
        // window-open handler, which only fires for target=_blank.
        const href = escapeHtml(token.href)
        const title = token.title ? ` title="${escapeHtml(token.title)}"` : ''
        return `<a href="${href}"${title} target="_blank" rel="noreferrer noopener">${this.parser.parseInline(token.tokens)}</a>`
      },
      image(token): string {
        // Deliberately no `src`. The renderer cannot fetch anything, so a
        // relative path resolves against nothing and paints a broken image
        // before it is even tried. The panel fills these in from disk where it
        // knows which file the prose came from; anywhere else the alt text
        // stands in, which is what it is for.
        const src = escapeHtml(token.href)
        const alt = escapeHtml(token.text ?? '')
        const title = token.title ? ` title="${escapeHtml(token.title)}"` : ''
        return `<img data-src="${src}" alt="${alt}"${title}>`
      },
    },
  })
}

const marked = parser(true)
const fileParser = parser(false)

/**
 * Markdown to HTML, sanitized.
 *
 * The output is injected with dangerouslySetInnerHTML, so DOMPurify is not
 * belt-and-braces here: transcript text is whatever the agent read from disk or
 * off the network, and it is not trusted.
 */
export function renderMarkdown(source: string): string {
  const html = marked.parse(source, { async: false })
  return DOMPurify.sanitize(html, { ADD_ATTR: ['target'] })
}

/** A rendered top-level block and the source lines it came from. */
export interface MarkdownBlock {
  html: string
  /** 1-based and inclusive, so a block reads as `L12-18` of the file. */
  startLine: number
  endLine: number
  /** Heading level, which is what a document folds along. */
  depth?: number
}

/**
 * Markdown to HTML one top-level block at a time, each tagged with its source
 * line range. Rendering per block is what lets the reader point at a paragraph
 * and get the lines behind it; `raw` is the only line information marked keeps,
 * so the ranges are counted from it as the tokens go past.
 */
export function renderMarkdownBlocks(source: string): MarkdownBlock[] {
  const blocks: MarkdownBlock[] = []
  let line = 1
  for (const token of fileParser.lexer(source)) {
    const startLine = line
    const newlines = (token.raw.match(/\n/g) ?? []).length
    line += newlines
    // Blank lines between blocks belong to neither, and have nothing to render.
    if (token.type === 'space') continue
    // No options argument. Passing one replaces the parser's own — renderer
    // included — so `{ async: false }` was quietly handing every block to
    // marked's default renderer: no highlighting on a code fence, no
    // `target=_blank` on a link, and an image with a relative `src` that
    // resolves against nothing. The instance is not async, so there was never
    // anything to turn off.
    const rendered = fileParser.parser([token]) as string
    const html = DOMPurify.sanitize(rendered, { ADD_ATTR: ['target'] })
    blocks.push({
      html,
      startLine,
      endLine: Math.max(startLine, startLine + newlines - 1),
      ...(token.type === 'heading' ? { depth: token.depth } : {}),
    })
  }
  return blocks
}

/**
 * Paths mentioned in prose: `~/a/b`, `/a/b`, or a relative path with at least
 * three segments. Same expression as the mobile app, which errs towards missing
 * a path over turning ordinary words into chips.
 */
const FILE_PATH =
  /(^|[\s:,;(`])(~\/[a-zA-Z0-9_.-]+(?:\/[a-zA-Z0-9_.-]+)+|\/[a-zA-Z0-9_.-]+(?:\/[a-zA-Z0-9_.-]+)+|[a-zA-Z0-9_-]+(?:\/[a-zA-Z0-9_.-]+){2,})/gm

/** File paths mentioned in a message, deduped and in order of appearance. */
export function extractFilePaths(text: string): string[] {
  const found: string[] = []
  const seen = new Set<string>()
  for (const match of text.matchAll(FILE_PATH)) {
    // Sentence punctuation is not part of the path, and a path that ends in a
    // stray period is one the daemon will fail to open.
    const path = (match[2] ?? '').replace(/[.,;:)]+$/, '')
    if (!path || seen.has(path)) continue
    seen.add(path)
    found.push(path)
  }
  return found
}

/**
 * Turns a mentioned path into one the daemon can open.
 *
 * Agents write relative paths from the session's directory, but they also write
 * `helios/internal/foo.go` while sitting in `~/workspace/helios` — so a first
 * segment that repeats the last segment of the cwd is joined to the parent
 * instead, which is the same rule the mobile app applies.
 */
export function resolveFilePath(path: string, cwd: string): string {
  if (path.startsWith('/') || path.startsWith('~')) return path
  const base = cwd.replace(/\/+$/, '')
  const first = path.split('/')[0]
  const last = base.split('/').pop()
  if (first && first === last) {
    const parent = base.slice(0, base.length - first.length - 1)
    return parent ? `${parent}/${path}` : `/${path}`
  }
  return `${base}/${path}`
}
