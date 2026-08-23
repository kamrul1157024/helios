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
 * OSC colour queries: OSC 10/11/12 (fg/bg/cursor) and OSC 4 (palette). Sent
 * with a `?` argument, the terminal answers with the current colour — e.g.
 * powerlevel10k probes the background with `^[]11;?^G` on every prompt. That
 * reply leaks exactly like a DSR one (`^[]11;rgb:1f1f/1f1f/1f1f^[\`), which is
 * the other half of the stray-line bug.
 *
 * Unlike the CSI reports, these OSC numbers also *set* colours, so only the
 * `?` query is suppressed; a set (`^[]11;#1e1e1e^G`) still recolours the view.
 */
export const OSC_COLOUR_QUERIES: readonly number[] = [10, 11, 12, 4]

/**
 * Stop a terminal from replying to device queries, so only the host answers.
 * A handler returning `true` marks the sequence handled, which suppresses
 * xterm's built-in reply. Keystrokes are produced by the key handler, not this
 * parser, so they are unaffected.
 */
export function silenceDeviceReports(parser: IParser): void {
  for (const id of DEVICE_REPORT_QUERIES) parser.registerCsiHandler(id, () => true)
  // Swallow the colour report but let a colour *set* fall through to the
  // built-in handler by returning false when there is no `?` to answer.
  for (const id of OSC_COLOUR_QUERIES) parser.registerOscHandler(id, (data) => isColourQuery(data))
}

/** An OSC colour payload is a query when any of its `;`-separated parts is `?`. */
export function isColourQuery(data: string): boolean {
  return data.split(';').some((part) => part === '?')
}
