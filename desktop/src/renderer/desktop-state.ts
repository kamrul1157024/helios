/**
 * What the Desktop view shows, and what it does when the tunnel ends (spec 66,
 * D7).
 *
 * Both are decisions about data — a state from the daemon, a close code from
 * the socket — so they are made here, as plain functions, and the view only
 * draws the answer.
 */

import { statusOf } from './errors.ts'
import type { DesktopState, DesktopStep } from '../shared/models.ts'

export type DesktopScreen =
  | { kind: 'loading' }
  /** The daemon has no desktop routes: too old, or the feature is off. */
  | { kind: 'absent' }
  | { kind: 'unreachable'; text: string }
  /** `command` is what to show instead if the daemon turns out to need root after all. */
  | { kind: 'install'; missing: string[]; command: string }
  | { kind: 'command'; command: string }
  | { kind: 'progress'; title: string; steps: DesktopStep[] }
  | { kind: 'ready' }
  | { kind: 'failed'; error: string; steps: DesktopStep[] }
  | { kind: 'unsupported' }

/** What the CLI is called when the daemon does not say. */
const FALLBACK_COMMAND = 'helios setup desktop'

export function screenFor(state: DesktopState | undefined, error: unknown): DesktopScreen {
  if (!state) {
    if (!error) return { kind: 'loading' }
    if (statusOf(error) === 404) return { kind: 'absent' }
    return { kind: 'unreachable', text: error instanceof Error ? error.message : String(error) }
  }
  switch (state.state) {
    case 'missing':
      return state.can_install
        ? { kind: 'install', missing: state.missing ?? [], command: state.install_command || FALLBACK_COMMAND }
        : { kind: 'command', command: state.install_command || FALLBACK_COMMAND }
    case 'installing':
      return { kind: 'progress', title: 'Installing the desktop', steps: state.steps ?? [] }
    case 'starting':
      return { kind: 'progress', title: 'Starting the desktop', steps: state.steps ?? [] }
    case 'ready':
      return { kind: 'ready' }
    case 'failed':
      return {
        kind: 'failed',
        error: state.error || 'The desktop could not start.',
        steps: (state.steps ?? []).filter((step) => step.state !== 'pending'),
      }
    case 'unsupported':
      return { kind: 'unsupported' }
    default:
      // A state from a newer daemon. Saying so beats drawing nothing.
      return { kind: 'unreachable', text: `The daemon reports a desktop state this app does not know: ${String(state.state)}` }
  }
}

/** The close codes the tunnel uses (spec 66, D2). */
export const CLOSE = {
  normal: 1000,
  abnormal: 1006,
  guacd: 1011,
  ticket: 4401,
  tooMany: 4429,
  notReady: 4503,
} as const

export type CloseAction =
  /** The ticket was refused: get a fresh one and dial once more. */
  | { kind: 'reticket' }
  /** The desktop stopped being ready: read the state again and show it. */
  | { kind: 'refresh' }
  /** The line dropped: dial again after a pause. */
  | { kind: 'reconnect' }
  /** Something a second dial will not fix by itself. */
  | { kind: 'fail'; text: string }

/**
 * What a close code means for the view.
 *
 * A refused ticket is retried once, automatically: a ticket lives thirty
 * seconds and a slow first frame can outlast one. A second refusal is not a
 * clock, so it stops and says so.
 */
export function closeAction(code: number, reticketed: boolean): CloseAction {
  switch (code) {
    case CLOSE.ticket:
      return reticketed
        ? { kind: 'fail', text: 'The daemon refused the desktop ticket twice. Check this device is still paired.' }
        : { kind: 'reticket' }
    case CLOSE.notReady:
      return { kind: 'refresh' }
    case CLOSE.tooMany:
      return { kind: 'fail', text: 'Too many viewers. The desktop takes four at a time — close one and try again.' }
    case CLOSE.guacd:
      return { kind: 'fail', text: 'The desktop service failed.' }
    case CLOSE.normal:
      return { kind: 'fail', text: 'The desktop session ended.' }
    default:
      return { kind: 'reconnect' }
  }
}

/** Back-off between dials after a drop, as the terminals do it (spec 31). */
const RECONNECT_DELAYS = [500, 1_000, 2_000, 4_000, 8_000]

export function reconnectDelay(attempt: number): number {
  return RECONNECT_DELAYS[Math.min(attempt, RECONNECT_DELAYS.length - 1)] as number
}
