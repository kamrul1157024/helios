import type { TranscriptMessage } from '../../shared/models.ts'

/**
 * A turn's tool calls, drawn as one line.
 *
 * An agent answering a question reads four files, runs six commands and writes
 * two — and the sentence explaining why is a paragraph away by the time the
 * rows are done. The calls between two things the agent said collapse into
 * "12 steps — 6 shell, 2 writes, 4 reads", which opens to the rows it stands
 * for.
 *
 * Anything the agent says breaks a turn, because the prose is what the reader
 * is following; a group that swallowed it would be hiding the argument rather
 * than the noise.
 */
export type Item =
  | { kind: 'message'; index: number }
  /** The tool_use messages the turn holds, in the order they were called. */
  | { kind: 'turn'; indices: number[] }

/** A single call is already one line, and its own row says more than a count. */
const MIN_TURN = 2

const KINDS: { label: string; plural: string; tools: Set<string> }[] = [
  { label: 'shell', plural: 'shell', tools: new Set(['Bash', 'BashOutput']) },
  {
    label: 'write',
    plural: 'writes',
    tools: new Set(['Edit', 'MultiEdit', 'Write', 'NotebookEdit']),
  },
  { label: 'read', plural: 'reads', tools: new Set(['Read']) },
  { label: 'search', plural: 'searches', tools: new Set(['Grep', 'Glob']) },
]

function isCall(message: TranscriptMessage | undefined): boolean {
  return message?.role === 'tool_use'
}

function isResult(message: TranscriptMessage | undefined): boolean {
  return message?.role === 'tool_result'
}

/**
 * The transcript as rows: single messages, and turns standing for several.
 *
 * `live` says the agent is still working, which leaves the turn at the end
 * ungrouped — that one is what the reader is watching, and folding the thing
 * in progress is the one place a summary is not wanted.
 */
export function groupRuns(messages: TranscriptMessage[], live: boolean): Item[] {
  const items: Item[] = []

  for (let at = 0; at < messages.length; ) {
    if (!isCall(messages[at])) {
      items.push({ kind: 'message', index: at })
      at++
      continue
    }

    // Collect the turn: each call, and the result that answered it.
    const indices: number[] = []
    let end = at
    while (isCall(messages[end])) {
      indices.push(end)
      end++
      if (isResult(messages[end])) end++
    }

    const trailing = end >= messages.length
    if (indices.length >= MIN_TURN && !(live && trailing)) {
      items.push({ kind: 'turn', indices })
    } else {
      // Left as they were: the results still follow their calls, and the
      // renderer folds each one onto the row above it as it always has.
      for (let index = at; index < end; index++) items.push({ kind: 'message', index })
    }
    at = end
  }

  return items
}

/**
 * What the turn did, counted by kind: "6 shell, 2 writes, 4 reads".
 *
 * Kinds rather than tool names: "3 Edits, 1 MultiEdit, 1 Write" is the same
 * fact told in the agent's vocabulary instead of the reader's. A tool that
 * fits no kind is counted as a call, which is what it is.
 */
export function summariseTurn(messages: TranscriptMessage[], indices: number[]): string {
  const counts = new Map<string, number>()
  for (const index of indices) {
    const tool = messages[index]?.tool ?? ''
    const kind = KINDS.find((one) => one.tools.has(tool))
    const label = kind ? kind.label : 'call'
    counts.set(label, (counts.get(label) ?? 0) + 1)
  }

  // In the order the kinds are declared, so two turns holding the same work
  // read the same way round.
  const order = [...KINDS.map((kind) => kind.label), 'call']
  return order
    .filter((label) => counts.has(label))
    .map((label) => {
      const count = counts.get(label) ?? 0
      const kind = KINDS.find((one) => one.label === label)
      const word = count === 1 ? label : (kind?.plural ?? 'calls')
      return `${count} ${word}`
    })
    .join(', ')
}

/** How a turn went: false if any call in it failed. */
export function runSucceeded(messages: TranscriptMessage[], indices: number[]): boolean {
  return indices.every((index) => messages[index + 1]?.success !== false)
}
