/**
 * Where in the file an edit landed.
 *
 * An Edit tool call records the text it looked for and the text it wrote, and
 * nothing about where either sat — so a patch built from it has no line numbers
 * to show. The file does have them: finding the written text in the file it was
 * written to gives the offset the gutter needs.
 *
 * It can fail, and quietly returning nothing is the right answer when it does.
 * The file moves on — later edits shift the lines, and a session read back a
 * day later may not contain the text at all. A number that is merely plausible
 * is worse than none, because a gutter looks authoritative.
 */

/**
 * The 1-based line the text starts on, or null when the file does not settle
 * the question.
 *
 * Null for absent, and null for more than one match: two candidates mean the
 * edit could have been either, and picking the first would be a guess.
 */
export function lineOf(content: string, needle: string): number | null {
  if (needle === '' || content === '') return null

  const first = content.indexOf(needle)
  if (first === -1) return null
  if (content.indexOf(needle, first + 1) !== -1) return null

  let line = 1
  for (let at = content.indexOf('\n'); at !== -1 && at < first; at = content.indexOf('\n', at + 1)) {
    line++
  }
  return line
}

/**
 * The `@@` header a patch needs to carry numbers, or '' when there is none to
 * give it.
 *
 * Written as a header rather than passed as a start line because that is what
 * every other patch in the app already carries: the diff view reads numbers
 * from `@@` and nothing else, so a resolved offset joins the same path as a
 * patch that came from git.
 */
export function hunkHeader(diff: string, start: number | null): string {
  if (start === null || diff === '') return ''
  let before = 0
  let after = 0
  for (const line of diff.split('\n')) {
    if (line.startsWith('-')) before++
    else if (line.startsWith('+')) after++
    else {
      before++
      after++
    }
  }
  return `@@ -${start},${before} +${start},${after} @@`
}
