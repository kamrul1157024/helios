import type { NotificationPrefs } from './models.ts'

/**
 * Types that hold an agent until they are answered.
 *
 * These go to the HUD, which can answer them; the rest are news, and a banner
 * is the right size for news.
 */
export function isBlocking(type: string): boolean {
  return (
    type === 'claude.permission' ||
    type === 'claude.question' ||
    type === 'claude.trust' ||
    type.startsWith('claude.elicitation')
  )
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
