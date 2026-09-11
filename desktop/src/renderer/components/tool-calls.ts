import type { TranscriptMessage } from '../../shared/models.ts'

/**
 * Pairing a call with the result that followed it.
 *
 * A tool_result carries no output — the daemon records the tool's name and
 * whether it worked (internal/transcript/reader.go) — so drawing it as a
 * message of its own spends a line of the transcript on the word "done". Paired
 * up, it is a tick at the end of the row that earned it.
 */

/** A string field of a tool's input, or '' when it is anything else. */
function str(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

/**
 * A summary is one line by definition, but the thing it summarises often is
 * not — a heredoc, a multi-line command.
 */
export function oneLine(text: string | undefined): string {
  return (text ?? '').replace(/\s+/g, ' ').trim()
}

/**
 * What the row says the call was.
 *
 * The agent's own description first, where it wrote one: "Push and open the
 * second PR" is what the reader wants from a row, and the twelve-line heredoc
 * that did it is what they want from opening the row.
 *
 * Failing that, the command — not the daemon's summary, which cuts at 80
 * characters (internal/transcript/reader.go), the point where `git commit -m
 * "…"` loses its message and two pipelines become the same string.
 */
export function headline(tool: string, input: Record<string, unknown>, summary?: string): string {
  if (tool === 'Bash' || tool === 'BashOutput') {
    const described = oneLine(str(input.description))
    if (described) return described
    const command = str(input.command) || str(input.cmd)
    if (command) return command.trim()
  }
  return oneLine(summary)
}

/** The command a row is holding behind a description, if it is holding one. */
export function foldedCommand(tool: string, input: Record<string, unknown>): string {
  if (tool !== 'Bash' && tool !== 'BashOutput') return ''
  if (!oneLine(str(input.description))) return ''
  return str(input.command) || str(input.cmd)
}

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
