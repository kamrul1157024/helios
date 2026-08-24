/** How a patch is drawn. Side by side reads better for review; unified is
 *  narrower and is what a terminal shows. */
export type DiffLayout = 'split' | 'unified'

interface Row {
  /** Full-width row: a file header or a hunk header. */
  meta?: string
  left?: { n: number | null; text: string; changed: boolean }
  right?: { n: number | null; text: string; changed: boolean }
}

/** A unified patch, coloured. Shared by the working-tree and commit views. */
export function DiffView({
  diff,
  empty,
  layout = 'split',
}: {
  diff: string
  empty?: string
  layout?: DiffLayout
}): JSX.Element {
  if (!diff.trim()) return <p className="empty-note">{empty ?? 'No changes.'}</p>

  if (layout === 'unified') {
    return (
      <pre>
        {diff.split('\n').map((line, index) => (
          <span key={index} className={diffClass(line)}>
            {line || ' '}
            {'\n'}
          </span>
        ))}
      </pre>
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
          <div key={index} className="diff-row">
            <Side cell={row.left} kind="del" />
            <Side cell={row.right} kind="add" />
          </div>
        ),
      )}
    </div>
  )
}

function Side({ cell, kind }: { cell: Row['left']; kind: 'del' | 'add' }): JSX.Element {
  // An absent cell is not an empty line: one side of the pair simply has
  // nothing here, and it is shaded so the eye skips it.
  if (!cell) return <div className="diff-cell diff-cell-absent" />
  return (
    <div className={`diff-cell ${cell.changed ? (kind === 'del' ? 'd-del' : 'd-add') : ''}`}>
      <span className="diff-gutter">{cell.n ?? ''}</span>
      <span className="diff-text">{cell.text || ' '}</span>
    </div>
  )
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
