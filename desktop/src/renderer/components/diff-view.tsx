import { useEffect, useMemo, useRef, useState } from 'react'

import { useHighlighted } from '../highlighter.ts'
import { ChunkedRows } from './windowed.tsx'

/** How a patch is drawn. Side by side reads better for review; unified is
 *  narrower and is what a terminal shows. */
export type DiffLayout = 'split' | 'unified'

/** One side of a split row. `at` is the line of the patch it came from. */
interface Cell {
  n: number | null
  text: string
  changed: boolean
  at: number
}

interface Row {
  /** Full-width row: a file header or a hunk header. */
  meta?: string
  /** The patch line a meta row came from. */
  at?: number
  left?: Cell
  right?: Cell
}

interface UnifiedRow {
  /** The patch line this row is: unified rows are the patch, one for one. */
  at: number
  meta?: string
  sign?: string
  /** Null where the line does not exist on that side of the patch. */
  oldN?: number | null
  newN?: number | null
  text?: string
  cls?: string
}

/** A unified patch, coloured. Shared by the working-tree and commit views. */
export function DiffView({
  diff,
  empty,
  language,
  layout = 'split',
  line,
  maxLines,
  picked = null,
  onPick,
  current = -1,
  onChanges,
  stack = true,
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
   * one file — a file tab's Diff view — passes nothing and draws it whole.
   */
  maxLines?: number
  /** Patch lines picked for a review comment, 0-based and inclusive. */
  picked?: { from: number; to: number } | null
  /** A click on a line, with shift held to extend. Absent means not pickable. */
  onPick?: (at: number, extend: boolean) => void
  /** Which run of changed lines is the current one, for F7, or -1 for none. */
  current?: number
  /** How many runs of changed lines the patch has, reported as it is laid out. */
  onChanges?: (count: number) => void
  /**
   * Whether split may fall back to one column in a narrow pane. A view where
   * the reader chose the layout passes false: their Split means two columns.
   */
  stack?: boolean
}): JSX.Element {
  const marked = useRef<HTMLDivElement | null>(null)
  const currentRow = useRef<HTMLDivElement | null>(null)
  const [expanded, setExpanded] = useState(false)

  /**
   * Each line highlighted on its own, in the highlighting worker.
   *
   * A patch is not a file: the lines either side of a hunk are missing, so
   * there is no whole document to hand the grammar. Line by line costs the
   * constructs that span lines — a block comment reads as code after its first
   * line — and gets every other token right, which is the trade GitHub makes
   * too. Each distinct line goes once, however often it repeats.
   *
   * There used to be a cap here, past which a patch was drawn plain because the
   * grammar runs held the window. They run elsewhere now, and only the rows on
   * screen are drawn, so a long patch is coloured like a short one.
   */
  const texts = useMemo(() => (language ? distinctLines(diff) : null), [diff, language])
  const html = useHighlighted(language, texts)
  const paint = useMemo<Painter>(() => {
    if (!texts || !html) return null
    const byText = new Map<string, string>()
    texts.forEach((text, index) => byText.set(text, html[index] ?? ''))
    return (text: string) => byText.get(text)
  }, [texts, html])
  const unified = useMemo(() => (layout === 'unified' ? toUnifiedRows(diff) : null), [diff, layout])
  const split = useMemo(() => (layout === 'unified' ? null : toRows(diff)), [diff, layout])

  // A patch is long and the interesting line is rarely at the top, so being
  // pointed at one is worthless unless the pane goes there.
  useEffect(() => {
    marked.current?.scrollIntoView({ block: 'center' })
  }, [line, diff])

  // Where each run of changed lines starts, for F7 and ⇧F7.
  const runs = useMemo(
    () => (unified ? changeRuns(unified.rows, (row) => Boolean(row.cls)) : changeRuns(split ?? [], isChanged)),
    [unified, split],
  )
  useEffect(() => onChanges?.(runs.length), [runs, onChanges])
  const run = runs[current]
  useEffect(() => {
    currentRow.current?.scrollIntoView({ block: 'center' })
  }, [current, run])
  const inRun = (index: number): boolean => run !== undefined && index >= run.start && index < run.end
  const pickedAt = (at: number | undefined): boolean =>
    picked !== null && at !== undefined && at >= picked.from && at <= picked.to
  const pickHandler = (at: number | undefined) =>
    onPick && at !== undefined
      ? (event: React.MouseEvent) => {
          event.preventDefault()
          onPick(at, event.shiftKey)
        }
      : undefined

  if (!diff.trim()) return <p className="empty-note">{empty ?? 'No changes.'}</p>

  if (unified) {
    const { rows, numbered } = unified
    // A line asked for by name is somewhere in the patch, and cutting the
    // patch would be cutting the thing the reader was sent to.
    const capped = maxLines !== undefined && line === undefined && !expanded && rows.length > maxLines
    const shown = capped ? rows.slice(0, maxLines) : rows
    const hidden = rows.length - shown.length
    const pinned = run ? run.start : line === undefined ? undefined : shown.findIndex((row) => row.newN === line)
    const draw = (index: number): JSX.Element => {
      const row = shown[index] as UnifiedRow
      const extra = `${pickedAt(row.at) ? ' diff-picked' : ''}${inRun(index) ? ' diff-row-current' : ''}`
      return row.meta !== undefined ? (
        <div
          key={index}
          className={`diff-line diff-line-meta ${diffClass(row.meta)}${extra}`}
          onMouseDown={pickHandler(row.at)}
        >
          {row.meta || ' '}
        </div>
      ) : (
        <div
          key={index}
          className={`diff-line ${row.cls}${line !== undefined && row.newN === line ? ' diff-row-marked' : ''}${extra}`}
          ref={
            run?.start === index ? currentRow : line !== undefined && row.newN === line ? marked : undefined
          }
          onMouseDown={pickHandler(row.at)}
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
      )
    }
    return (
      <div className="diff-unified">
        <ChunkedRows count={shown.length} render={draw} pinned={pinned !== -1 ? pinned : undefined} />
        {(capped || expanded) && (
          <button className="diff-more" onClick={() => setExpanded(!expanded)}>
            {capped ? `Show ${hidden} more ${hidden === 1 ? 'line' : 'lines'}` : 'Show less'}
          </button>
        )}
      </div>
    )
  }

  const rows = split ?? []
  const pinned = run ? run.start : line === undefined ? -1 : rows.findIndex((row) => row.right?.n === line)
  const draw = (index: number): JSX.Element => {
    const row = rows[index] as Row
    return row.meta !== undefined ? (
      <div
        key={index}
        className={`diff-row diff-row-meta ${diffClass(row.meta)}${pickedAt(row.at) ? ' diff-picked' : ''}`}
        onMouseDown={pickHandler(row.at)}
      >
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
          inRun(index) ? 'diff-row-current' : '',
        ]
          .filter(Boolean)
          .join(' ')}
        ref={run?.start === index ? currentRow : line !== undefined && row.right?.n === line ? marked : undefined}
      >
        <Side cell={row.left} kind="del" paint={paint} picked={pickedAt(row.left?.at)} onPick={pickHandler(row.left?.at)} />
        <Side cell={row.right} kind="add" paint={paint} picked={pickedAt(row.right?.at)} onPick={pickHandler(row.right?.at)} />
      </div>
    )
  }
  return (
    <div className={stack ? 'diff-split' : 'diff-split diff-split-fixed'}>
      <ChunkedRows count={rows.length} render={draw} pinned={pinned !== -1 ? pinned : undefined} />
    </div>
  )
}

/** Every line's text once, as the rows below will ask for it. */
function distinctLines(diff: string): string[] {
  const seen = new Set<string>()
  for (const line of diff.split('\n')) {
    const text = line.startsWith('+') || line.startsWith('-') || line.startsWith(' ') ? line.slice(1) : line
    if (text) seen.add(text)
  }
  return [...seen]
}

/** A run of changed rows: [start, end) in row indices. */
interface ChangeRun {
  start: number
  end: number
}

/** Every run of consecutive changed rows, in order. What F7 steps through. */
function changeRuns<T>(rows: T[], changed: (row: T) => boolean): ChangeRun[] {
  const out: ChangeRun[] = []
  rows.forEach((row, index) => {
    if (!changed(row)) return
    const last = out[out.length - 1]
    if (last && last.end === index) last.end = index + 1
    else out.push({ start: index, end: index + 1 })
  })
  return out
}

function isChanged(row: Row): boolean {
  return Boolean(row.left?.changed || row.right?.changed)
}

function Side({
  cell,
  kind,
  paint,
  picked,
  onPick,
}: {
  cell: Cell | undefined
  kind: 'del' | 'add'
  paint: Painter
  picked: boolean
  onPick?: (event: React.MouseEvent) => void
}): JSX.Element {
  // An absent cell is not an empty line: one side of the pair simply has
  // nothing here, and it is shaded so the eye skips it.
  if (!cell) return <div className="diff-cell diff-cell-absent" />
  return (
    <div
      className={`diff-cell ${cell.changed ? (kind === 'del' ? 'd-del' : 'd-add') : ''}${picked ? ' diff-picked' : ''}`}
      onMouseDown={onPick}
    >
      <span className="diff-gutter">{cell.n ?? ''}</span>
      <Code text={cell.text} paint={paint} />
    </div>
  )
}

/** What turns a line of source into coloured markup, or null for plain text. */
type Painter = ((text: string) => string | undefined) | null

/**
 * One line of the patch.
 *
 * The markup carries hljs token classes but not `.hljs` itself: that class sets
 * a background, and the row's own add/delete tint is what has to show through.
 */
function Code({ text, paint }: { text: string; paint: Painter }): JSX.Element {
  const html = paint && text ? paint(text) : undefined
  if (html === undefined) return <span className="diff-text">{text || ' '}</span>
  return <span className="diff-text" dangerouslySetInnerHTML={{ __html: html }} />
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

  walkPatch(diff, (line, at) => {
    if (line === BINARY) {
      rows.push({ at, meta: line })
      return
    }
    const hunk = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/.exec(line)
    if (hunk) {
      oldN = Number(hunk[1])
      newN = Number(hunk[2])
      numbered = true
      rows.push({ at, meta: line })
      return
    }
    if (line.startsWith('@@')) {
      rows.push({ at, meta: line })
      return
    }

    if (line.startsWith('-')) {
      rows.push({ at, sign: '-', oldN: oldN++, newN: null, text: line.slice(1), cls: 'd-del' })
      return
    }
    if (line.startsWith('+')) {
      rows.push({ at, sign: '+', oldN: null, newN: newN++, text: line.slice(1), cls: 'd-add' })
      return
    }
    rows.push({
      at,
      sign: ' ',
      oldN: oldN++,
      newN: newN++,
      text: line.startsWith(' ') ? line.slice(1) : line,
      cls: '',
    })
  })

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
  let dels: Cell[] = []
  let adds: Cell[] = []
  let leftNo = 0
  let rightNo = 0

  const flush = (): void => {
    for (let i = 0; i < Math.max(dels.length, adds.length); i++) {
      rows.push({ left: dels[i], right: adds[i] })
    }
    dels = []
    adds = []
  }

  walkPatch(diff, (line, at) => {
    if (line === BINARY) {
      flush()
      rows.push({ at, meta: line })
      return
    }
    const hunk = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/.exec(line)
    if (hunk) {
      flush()
      leftNo = Number(hunk[1])
      rightNo = Number(hunk[2])
      rows.push({ at, meta: line })
      return
    }
    if (line.startsWith('@@')) {
      flush()
      rows.push({ at, meta: line })
      return
    }

    if (line.startsWith('-')) {
      dels.push({ n: leftNo++, text: line.slice(1), changed: true, at })
      return
    }
    if (line.startsWith('+')) {
      adds.push({ n: rightNo++, text: line.slice(1), changed: true, at })
      return
    }

    flush()
    const text = line.startsWith(' ') ? line.slice(1) : line
    rows.push({
      left: { n: leftNo++, text, changed: false, at },
      right: { n: rightNo++, text, changed: false, at },
    })
  })
  flush()
  return rows
}

/** What a binary file's header becomes: there are no lines to show. */
const BINARY = 'Binary file changed'

/**
 * The lines that start a file's section in `git diff`, which say which file
 * and how — none of them is a line of the file. `--- ` and `+++ ` only count
 * here, before the first hunk: inside one they are a removed `-- ` line and an
 * added `++ ` one.
 */
const FILE_HEADER =
  /^(diff --git |index |--- |\+\+\+ |new file mode|deleted file mode|old mode|new mode|similarity index|dissimilarity index|rename from|rename to|copy from|copy to|Binary files )/

/**
 * Hands every line of a patch that belongs in the view to `visit`, with its
 * index in the patch, and skips each file's header. A binary file's header is
 * all there is of it, so it comes through as one line that says so. Patches
 * with no header at all — the transcript builds its own from a tool call —
 * pass through untouched.
 */
function walkPatch(diff: string, visit: (line: string, at: number) => void): void {
  let header = true
  diff.split('\n').forEach((line, at) => {
    if (line.startsWith('diff --git ')) {
      header = true
      return
    }
    if (header && FILE_HEADER.test(line)) {
      if (line.startsWith('Binary files ')) visit(BINARY, at)
      return
    }
    header = false
    visit(line, at)
  })
}

export function diffClass(line: string): string {
  if (line.startsWith('+++') || line.startsWith('---')) return 'd-meta'
  if (line.startsWith('@@')) return 'd-hunk'
  if (line.startsWith('+')) return 'd-add'
  if (line.startsWith('-')) return 'd-del'
  if (line.startsWith('diff --git') || line.startsWith('index ')) return 'd-meta'
  return ''
}
