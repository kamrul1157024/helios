/**
 * A unified patch for an edit the agent made, built from the strings the tool
 * call carried. The daemon does not send one: Edit reports the text it looked
 * for and the text it wrote, and reading those as two code blocks means
 * spotting the difference by eye.
 *
 * The output is the format DiffView already colours, so the transcript and the
 * git panel render changes the same way.
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
