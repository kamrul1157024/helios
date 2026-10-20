/**
 * A unified patch for an edit the agent made, built from the strings the tool
 * call carried. The daemon does not send one: Edit reports the text it looked
 * for and the text it wrote, and reading those as two code blocks means
 * spotting the difference by eye.
 *
 * The output is the format DiffView already colours, so the transcript and the
 * Files panel's Diff view render changes the same way.
 */
export function unifiedDiff(before: string, after: string): string {
  const a = before === '' ? [] : before.split('\n')
  const b = after === '' ? [] : after.split('\n')

  // Both sides come from one tool call, so they are small. A quadratic LCS is
  // fine here and stays exact, which a heuristic would not.
  if (a.length * b.length > 250_000) {
    return [...a.map((line) => `-${line}`), ...b.map((line) => `+${line}`)].join('\n')
  }

  const lcs: number[][] = Array.from({ length: a.length + 1 }, () => new Array<number>(b.length + 1).fill(0))
  for (let i = a.length - 1; i >= 0; i--) {
    for (let j = b.length - 1; j >= 0; j--) {
      lcs[i]![j] = a[i] === b[j] ? lcs[i + 1]![j + 1]! + 1 : Math.max(lcs[i + 1]![j]!, lcs[i]![j + 1]!)
    }
  }

  const out: string[] = []
  let i = 0
  let j = 0
  while (i < a.length && j < b.length) {
    if (a[i] === b[j]) {
      out.push(` ${a[i]}`)
      i++
      j++
    } else if (lcs[i + 1]![j]! >= lcs[i]![j + 1]!) {
      out.push(`-${a[i]}`)
      i++
    } else {
      out.push(`+${b[j]}`)
      j++
    }
  }
  while (i < a.length) out.push(`-${a[i++]}`)
  while (j < b.length) out.push(`+${b[j++]}`)

  return out.join('\n')
}

/** The edits a MultiEdit call carried, in the order it applied them. */
export function multiEditDiff(edits: unknown): string {
  if (!Array.isArray(edits)) return ''
  return edits
    .map((edit) => {
      if (typeof edit !== 'object' || edit === null) return ''
      const { old_string: before, new_string: after } = edit as Record<string, unknown>
      if (typeof before !== 'string' || typeof after !== 'string') return ''
      return unifiedDiff(before, after)
    })
    .filter(Boolean)
    .join('\n@@\n')
}

/**
 * The 1-based lines of `after` that an edit added or changed, for the tint a
 * tab shows when the agent writes to it.
 *
 * The lines both versions share at the top and the bottom are taken off first.
 * An agent's edit is usually a few lines somewhere in the middle of a long
 * file, and the LCS below is quadratic: trimmed, it runs on those few lines
 * rather than on the whole file, and stays exact.
 */
export function changedLines(before: string, after: string): number[] {
  const a = before.split('\n')
  const b = after.split('\n')
  let head = 0
  while (head < a.length && head < b.length && a[head] === b[head]) head++
  let tail = 0
  while (tail < a.length - head && tail < b.length - head && a[a.length - 1 - tail] === b[b.length - 1 - tail]) tail++

  const middle = unifiedDiff(a.slice(head, a.length - tail).join('\n'), b.slice(head, b.length - tail).join('\n'))
  const out: number[] = []
  let line = head + 1
  for (const row of middle ? middle.split('\n') : []) {
    if (row.startsWith('-')) continue
    if (row.startsWith('+')) out.push(line)
    line++
  }
  return out
}

/** What the gutter marks on each line of the file as it stands. */
export interface GutterMarks {
  added: number[]
  changed: number[]
  /** Lines below which something was taken out. 0 means above the first. */
  removed: number[]
}

/**
 * Reads a file's working-tree patch against HEAD into gutter marks.
 *
 * Within a run of removals and additions, as many added lines as there were
 * removed ones count as changed and the rest as added; a run of removals with
 * nothing added marks the line it was cut from. That is how an editor's change
 * bar reads, and a patch from `git diff` carries everything it needs.
 */
export function gutterMarks(diff: string): GutterMarks {
  const marks: GutterMarks = { added: [], changed: [], removed: [] }
  let line = 0
  let dels = 0
  let adds: number[] = []
  const flush = (): void => {
    adds.forEach((at, index) => (index < dels ? marks.changed : marks.added).push(at))
    if (dels > 0 && adds.length === 0) marks.removed.push(Math.max(line - 1, 0))
    dels = 0
    adds = []
  }
  for (const row of diff.split('\n')) {
    const hunk = /^@@ -\d+(?:,\d+)? \+(\d+)(?:,\d+)? @@/.exec(row)
    if (hunk) {
      flush()
      line = Number(hunk[1])
      continue
    }
    if (line === 0 || row.startsWith('+++') || row.startsWith('---') || row.startsWith('\\')) continue
    if (row.startsWith('-')) {
      dels++
    } else if (row.startsWith('+')) {
      adds.push(line++)
    } else {
      flush()
      line++
    }
  }
  flush()
  return marks
}

/** Unchanged lines kept either side of a change in `fileDiff`. */
const CONTEXT = 3

/**
 * A patch between two whole versions of one file, numbered, for reading.
 *
 * The lines both share at the top and bottom are trimmed down to a little
 * context first, for the same reason as `changedLines`: the LCS only has to run
 * over the part that differs, and the `@@` header is what gives the diff view
 * its line numbers.
 */
export function fileDiff(before: string, after: string): string {
  if (before === after) return ''
  const a = before.split('\n')
  const b = after.split('\n')
  let head = 0
  while (head < a.length && head < b.length && a[head] === b[head]) head++
  let tail = 0
  while (tail < a.length - head && tail < b.length - head && a[a.length - 1 - tail] === b[b.length - 1 - tail]) tail++
  const from = Math.max(head - CONTEXT, 0)
  const keepTail = Math.max(tail - CONTEXT, 0)
  const left = a.slice(from, a.length - keepTail)
  const right = b.slice(from, b.length - keepTail)
  const body = unifiedDiff(left.join('\n'), right.join('\n'))
  return `@@ -${from + 1},${left.length} +${from + 1},${right.length} @@\n${body}`
}
