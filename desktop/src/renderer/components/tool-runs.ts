import type { TranscriptMessage } from '../../shared/models.ts'

/**
 * A run of shell commands, drawn as one line.
 *
 * An agent checking its work issues six greps and a build in a row, and the
 * transcript spends seven lines saying so before the sentence that explains
 * why. Consecutive shell calls collapse into "Ran 6 shell commands", which
 * opens to the rows it stands for.
 *
 * Only consecutive ones, and only shell: anything the agent says breaks a run,
 * because the prose is what the reader is following and a group that swallowed
 * it would be hiding the argument rather than the noise. A read or a write
 * breaks it too — those rows carry a filename worth seeing.
 */
export type Item =
  | { kind: 'message'; index: number }
  /** The tool_use messages the run holds, in the order they were called. */
  | { kind: 'run'; indices: number[] }

const SHELL_TOOLS = new Set(['Bash', 'BashOutput'])

/** Two commands are two rows; three is where a group starts paying. */
const MIN_RUN = 3

function isShellCall(message: TranscriptMessage | undefined): boolean {
  return message?.role === 'tool_use' && SHELL_TOOLS.has(message.tool ?? '')
}

function isResultFor(message: TranscriptMessage | undefined): boolean {
  return message?.role === 'tool_result'
}

/**
 * The transcript as rows: single messages, and runs standing for several.
 *
 * `live` says the agent is still working, which keeps the run at the end of
 * the transcript ungrouped — that one is what the reader is watching, and
 * folding the thing in progress is the one place a summary is not wanted.
 */
export function groupRuns(messages: TranscriptMessage[], live: boolean): Item[] {
  const items: Item[] = []

  for (let at = 0; at < messages.length; ) {
    if (!isShellCall(messages[at])) {
      items.push({ kind: 'message', index: at })
      at++
      continue
    }

    // Collect the run: each call, and the result that answered it.
    const indices: number[] = []
    let end = at
    while (isShellCall(messages[end])) {
      indices.push(end)
      end++
      if (isResultFor(messages[end])) end++
    }

    const trailing = end >= messages.length
    if (indices.length >= MIN_RUN && !(live && trailing)) {
      items.push({ kind: 'run', indices })
    } else {
      // Left as they were: the results still follow their calls, and the
      // renderer folds each one onto the row above it as it always has.
      for (let index = at; index < end; index++) items.push({ kind: 'message', index })
    }
    at = end
  }

  return items
}

/** How a run went: false if any call in it failed. */
export function runSucceeded(messages: TranscriptMessage[], indices: number[]): boolean {
  return indices.every((index) => messages[index + 1]?.success !== false)
}
