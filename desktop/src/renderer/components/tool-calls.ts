import type { TranscriptMessage } from '../../shared/models.ts'

/**
 * Pairing a call with the result that followed it.
 *
 * A tool_result carries no output — the daemon records the tool's name and
 * whether it worked (internal/transcript/reader.go) — so drawing it as a
 * message of its own spends a line of the transcript on the word "done". Paired
 * up, it is a tick at the end of the row that earned it.
 */

/** Whether the message at this index is a result belonging to the call above it. */
export function followsItsCall(messages: TranscriptMessage[], index: number): boolean {
  return messages[index]?.role === 'tool_result' && messages[index - 1]?.role === 'tool_use'
}

/**
 * How the call at this index went, or undefined when nothing says.
 *
 * Undefined is not the same as false: a call whose result has not arrived yet —
 * the agent is still running it — must not be drawn as one that failed.
 */
export function resultOf(messages: TranscriptMessage[], index: number): boolean | undefined {
  if (messages[index]?.role !== 'tool_use') return undefined
  const next = messages[index + 1]
  if (next?.role !== 'tool_result') return undefined
  return next.success !== false
}
