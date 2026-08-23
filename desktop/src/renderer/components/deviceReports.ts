import type { IFunctionIdentifier, IParser } from '@xterm/xterm'

/**
 * The device queries an xterm answers on its own — cursor-position reports
 * (DSR) and device attributes (DA). A child that wants to know about its
 * terminal sends one of these and reads the reply.
 *
 * In Helios the terminal is the host, not this viewer: the host's emulator
 * already answers these. If a viewer answered them too, its reply would arrive
 * a network round-trip late and land on whatever is reading by then — usually
 * the shell prompt, showing up as a stray `^[[…R`. Several viewers meant
 * several replies, which is the doubled bytes users reported.
 *
 * Only the report-generating queries are listed. Finals that change the display
 * (e.g. `q` for DECSCUSR cursor style) are deliberately absent so they still
 * take effect, and the CPR reply's own final `R` is absent so a keyboard
 * sequence like Shift+F3 (`^[[1;2R`) is never mistaken for a report.
 */
export const DEVICE_REPORT_QUERIES: readonly IFunctionIdentifier[] = [
  { final: 'n' }, // DSR — includes the cursor-position query ^[[6n
  { prefix: '?', final: 'n' }, // DECDSR / DECXCPR
  { final: 'c' }, // Primary DA
  { prefix: '>', final: 'c' }, // Secondary DA
  { prefix: '=', final: 'c' }, // Tertiary DA
]

/**
 * Stop a terminal from replying to device queries, so only the host answers.
 * A handler returning `true` marks the sequence handled, which suppresses
 * xterm's built-in reply. Keystrokes are produced by the key handler, not this
 * parser, so they are unaffected.
 */
export function silenceDeviceReports(parser: IParser): void {
  for (const id of DEVICE_REPORT_QUERIES) parser.registerCsiHandler(id, () => true)
}
