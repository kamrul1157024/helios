import type { NotificationPrefs } from './models.ts'

/**
 * The provider that raised a notification: "claude" from "claude.permission".
 */
export function providerOf(type: string): string {
  const i = type.indexOf('.')
  return i < 0 ? type : type.slice(0, i)
}

/**
 * What kind of request this is, independent of who raised it: "permission"
 * from "codex.permission", "elicitation.form" from "claude.elicitation.form".
 *
 * Every switch keys on this rather than the whole type. That one change is
 * what makes a second provider cost nothing here: its permission request is
 * the same request, and it gets the same card.
 */
export function kindOf(type: string): string {
  const i = type.indexOf('.')
  return i < 0 ? '' : type.slice(i + 1)
}

/**
 * Kinds that hold an agent until they are answered.
 *
 * These go to the HUD, which can answer them; the rest are news, and a banner
 * is the right size for news.
 *
 * The daemon also serves this, on /api/notification-types. Until the renderer
 * reads that, this list is the fallback — and it is keyed on kind, so an
 * unrecognised provider's permission request still reaches the HUD instead of
 * being quietly filed as news.
 */
const BLOCKING_KINDS = ['permission', 'question', 'trust']

export function isBlocking(type: string): boolean {
  const kind = kindOf(type)
  return BLOCKING_KINDS.includes(kind) || kind.startsWith('elicitation')
}

/**
 * Types a user can silence, in the order the settings screen lists them.
 * Mirrors the phone's Alert Settings, which is where a user will have formed an
 * expectation of what these toggles do.
 */
export const ALERT_TYPES = [
  'claude.permission',
  'claude.question',
  'claude.elicitation.form',
  'claude.elicitation.url',
  'claude.trust',
  'claude.done',
  'claude.error',
] as const

export const DEFAULT_PREFS: NotificationPrefs = {
  sound: true,
  alerts: Object.fromEntries(ALERT_TYPES.map((type) => [type, true])),
}

/**
 * Whether a notification of this type should make a sound.
 *
 * Silencing a type is not the same as hiding it: a request that blocks an agent
 * still gets its HUD card, its tray entry and its badge. The toggle buys quiet,
 * not invisibility — the same promise the phone's settings screen makes.
 */
export function shouldSound(prefs: NotificationPrefs, type: string): boolean {
  if (!prefs.sound) return false
  return prefs.alerts[type] ?? true
}
