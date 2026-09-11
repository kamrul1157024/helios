import { useEffect, useMemo, useRef, useState } from 'react'

import { highlightCode } from '../markdown.ts'

/** How a patch is drawn. Side by side reads better for review; unified is
 *  narrower and is what a terminal shows. */
export type DiffLayout = 'split' | 'unified'

interface Row {
  /** Full-width row: a file header or a hunk header. */
  meta?: string
  left?: { n: number | null; text: string; changed: boolean }
  right?: { n: number | null; text: string; changed: boolean }
}

interface UnifiedRow {
  meta?: string
  sign?: string
  /** Null where the line does not exist on that side of the patch. */
  oldN?: number | null
  newN?: number | null
  text?: string
  cls?: string
}

/**
 * Past this many lines the patch is drawn plain.
 *
 * Highlighting is a grammar run per line, and a patch this long is being
 * skimmed rather than read — the colours are not worth a second of a locked
 * window to get them.
 */
const MAX_HIGHLIGHT_LINES = 3000

/** A unified patch, coloured. Shared by the working-tree and commit views. */
export function DiffView({
  diff,
  empty,
  language,
  layout = 'split',
  line,
  maxLines,
}: {
  diff: string
  empty?: string
  /** The file's language, for the syntax colours. Plain text without one. */
  language?: string | null
  layout?: DiffLayout
  /** A line of the new file to scroll to and mark, when one was asked for. */
  line?: number
  /**
   * Show this many lines and offer the rest.
   *
   * For a patch sitting inline in a conversation: a four-hundred-line rewrite
   * is one message among many, and scrolling past it to reach the next
   * sentence is most of what reading that transcript becomes. A pane showing
   * one file — the git panel — passes nothing and draws it whole.
   */
  maxLines?: number
}): JSX.Element {
  const marked = useRef<HTMLDivElement | null>(null)
  const [expanded, setExpanded] = useState(false)

  /**
   * Each line highlighted on its own.
   *
   * A patch is not a file: the lines either side of a hunk are missing, so
   * there is no whole document to hand the grammar. Line by line costs the
   * constructs that span lines — a block comment reads as code after its first
   * line — and gets every other token right, which is the trade GitHub makes
   * too.
   */
  const paint = useMemo(() => {
    const lines = diff.split('\n')
    if (!language || lines.length > MAX_HIGHLIGHT_LINES) return null
    const cache = new Map<string, string>()
    return (text: string): string => {
      const held = cache.get(text)
      if (held !== undefined) return held
      const html = highlightCode(text, language)
      cache.set(text, html)
      return html
    }
  }, [diff, language])

  // A patch is long and the interesting line is rarely at the top, so being
  // pointed at one is worthless unless the pane goes there.
  useEffect(() => {
    marked.current?.scrollIntoView({ block: 'center' })
  }, [line, diff])

  if (!diff.trim()) return <p className="empty-note">{empty ?? 'No changes.'}</p>

  if (layout === 'unified') {
    const { rows, numbered } = toUnifiedRows(diff)
    // A line asked for by name is somewhere in the patch, and cutting the
    // patch would be cutting the thing the reader was sent to.
    const capped = maxLines !== undefined && line === undefined && !expanded && rows.length > maxLines
    const shown = capped ? rows.slice(0, maxLines) : rows
    const hidden = rows.length - shown.length
    return (
      <div className="diff-unified">
        {shown.map((row, index) =>
          row.meta !== undefined ? (
            <div key={index} className={`diff-line diff-line-meta ${diffClass(row.meta)}`}>
              {row.meta || ' '}
            </div>
          ) : (
            <div
              key={index}
              className={`diff-line ${row.cls}${
                line !== undefined && row.newN === line ? ' diff-row-marked' : ''
              }`}
              ref={line !== undefined && row.newN === line ? marked : undefined}
            >
              <span className="diff-nums">
                {numbered && (
                  <>
                    <span className="diff-gutter">{row.oldN ?? ''}</span>
                    <span className="diff-gutter">{row.newN ?? ''}</span>
                  </>
                )}
                <span className="diff-sign">{row.sign}</span>
              </span>
              <Code text={row.text ?? ''} paint={paint} />
            </div>
          ),
        )}
        {(capped || expanded) && (
          <button className="diff-more" onClick={() => setExpanded(!expanded)}>
            {capped ? `Show ${hidden} more ${hidden === 1 ? 'line' : 'lines'}` : 'Show less'}
          </button>
        )}
      </div>
    )
  }

  return (
    <div className="diff-split">
      {toRows(diff).map((row, index) =>
        row.meta !== undefined ? (
          <div key={index} className={`diff-row diff-row-meta ${diffClass(row.meta)}`}>
            {row.meta || ' '}
          </div>
        ) : (
          <div
            key={index}
            className={[
              'diff-row',
              // Both halves of a context row are the same text. Stacked into
              // one column they would read as the line appearing twice.
              !row.left?.changed && !row.right?.changed ? 'diff-row-context' : '',
              line !== undefined && row.right?.n === line ? 'diff-row-marked' : '',
            ]
              .filter(Boolean)
              .join(' ')}
            ref={line !== undefined && row.right?.n === line ? marked : undefined}
          >
            <Side cell={row.left} kind="del" paint={paint} />
            <Side cell={row.right} kind="add" paint={paint} />
          </div>
        ),
      )}
    </div>
  )
}

function Side({
  cell,
  kind,
  paint,
}: {
  cell: Row['left']
  kind: 'del' | 'add'
  paint: Painter
}): JSX.Element {
  // An absent cell is not an empty line: one side of the pair simply has
  // nothing here, and it is shaded so the eye skips it.
  if (!cell) return <div className="diff-cell diff-cell-absent" />
  return (
    <div className={`diff-cell ${cell.changed ? (kind === 'del' ? 'd-del' : 'd-add') : ''}`}>
      <span className="diff-gutter">{cell.n ?? ''}</span>
      <Code text={cell.text} paint={paint} />
    </div>
  )
}

/** What turns a line of source into coloured markup, or null for plain text. */
type Painter = ((text: string) => string) | null

/**
 * One line of the patch.
 *
 * The markup carries hljs token classes but not `.hljs` itself: that class sets
 * a background, and the row's own add/delete tint is what has to show through.
 */
function Code({ text, paint }: { text: string; paint: Painter }): JSX.Element {
  if (!paint || !text) return <span className="diff-text">{text || ' '}</span>
  return <span className="diff-text" dangerouslySetInnerHTML={{ __html: paint(text) }} />
}

/**
 * Turns a unified patch into one row per line.
 *
 * The numbers come from the `@@` headers, so a patch without one is reported as
 * unnumbered rather than counted from 1: the transcript's diffs are built from
 * the strings an Edit call carried, which say nothing about where in the file
 * they sat, and a gutter that invents the offsets is worse than no gutter.
 */
function toUnifiedRows(diff: string): { rows: UnifiedRow[]; numbered: boolean } {
  const rows: UnifiedRow[] = []
  let oldN = 0
  let newN = 0
  let numbered = false

  for (const line of diff.split('\n')) {
    const hunk = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/.exec(line)
    if (hunk) {
      oldN = Number(hunk[1])
      newN = Number(hunk[2])
      numbered = true
      rows.push({ meta: line })
      continue
    }
    if (
      line.startsWith('@@') ||
      line.startsWith('diff --git') ||
      line.startsWith('index ') ||
      line.startsWith('+++') ||
      line.startsWith('---')
    ) {
      rows.push({ meta: line })
      continue
    }

    if (line.startsWith('-')) {
      rows.push({ sign: '-', oldN: oldN++, newN: null, text: line.slice(1), cls: 'd-del' })
      continue
    }
    if (line.startsWith('+')) {
      rows.push({ sign: '+', oldN: null, newN: newN++, text: line.slice(1), cls: 'd-add' })
      continue
    }
    rows.push({
      sign: ' ',
      oldN: oldN++,
      newN: newN++,
      text: line.startsWith(' ') ? line.slice(1) : line,
      cls: '',
    })
  }

  return { rows, numbered }
}

/**
 * Turns a unified patch into aligned pairs.
 *
 * Removals and additions arrive as consecutive runs, so a run is paired off
 * position by position and whichever side runs out gets blanks. That is what
 * puts a changed line opposite the line it replaced instead of below it.
 */
function toRows(diff: string): Row[] {
  const rows: Row[] = []
  let dels: Row['left'][] = []
  let adds: Row['right'][] = []
  let leftNo = 0
  let rightNo = 0

  const flush = (): void => {
    for (let i = 0; i < Math.max(dels.length, adds.length); i++) {
      rows.push({ left: dels[i], right: adds[i] })
    }
    dels = []
    adds = []
  }

  for (const line of diff.split('\n')) {
    const hunk = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/.exec(line)
    if (hunk) {
      flush()
      leftNo = Number(hunk[1])
      rightNo = Number(hunk[2])
      rows.push({ meta: line })
      continue
    }
    if (
      line.startsWith('diff --git') ||
      line.startsWith('index ') ||
      line.startsWith('+++') ||
      line.startsWith('---')
    ) {
      flush()
      rows.push({ meta: line })
      continue
    }

    if (line.startsWith('-')) {
      dels.push({ n: leftNo++, text: line.slice(1), changed: true })
      continue
    }
    if (line.startsWith('+')) {
      adds.push({ n: rightNo++, text: line.slice(1), changed: true })
      continue
    }

    flush()
    const text = line.startsWith(' ') ? line.slice(1) : line
    rows.push({
      left: { n: leftNo++, text, changed: false },
      right: { n: rightNo++, text, changed: false },
    })
  }
  flush()
  return rows
}

export function diffClass(line: string): string {
  if (line.startsWith('+++') || line.startsWith('---')) return 'd-meta'
  if (line.startsWith('@@')) return 'd-hunk'
  if (line.startsWith('+')) return 'd-add'
  if (line.startsWith('-')) return 'd-del'
  if (line.startsWith('diff --git') || line.startsWith('index ')) return 'd-meta'
  return ''
}
