/// X11 keysyms, which is what the Guacamole protocol sends for every key.
library;

const keyEscape = 0xFF1B;
const keyTab = 0xFF09;
const keyBackspace = 0xFF08;
const keyReturn = 0xFF0D;
const keyLeft = 0xFF51;
const keyUp = 0xFF52;
const keyRight = 0xFF53;
const keyDown = 0xFF54;
const keyControl = 0xFFE3;
const keyAlt = 0xFFE9;

/// The keysym for one typed character.
///
/// Latin-1 keysyms are the code point itself; everything else is the code
/// point with the Unicode bit set, which xorgxrdp maps to whatever key types
/// it. A newline is Return, because that is the key a phone's Enter means.
int keysymForRune(int rune) {
  if (rune == 0x0A || rune == 0x0D) return keyReturn;
  if (rune == 0x09) return keyTab;
  if ((rune >= 0x20 && rune <= 0x7E) || (rune >= 0xA0 && rune <= 0xFF)) {
    return rune;
  }
  return 0x01000000 | rune;
}

/// The chord for [keysym] with the modifiers held, outermost first.
List<int> chordFor(int keysym, {bool ctrl = false, bool alt = false}) => [
  if (ctrl) keyControl,
  if (alt) keyAlt,
  keysym,
];

/// One chord per character of [text].
///
/// A held Ctrl is pressed with a letter in its lower case: Ctrl+Shift+C is a
/// different shortcut from Ctrl+C on most desktops, and the phone's keyboard
/// capitalises the first letter it types.
List<List<int>> chordsForText(
  String text, {
  bool ctrl = false,
  bool alt = false,
}) => [
  for (final rune in text.runes)
    chordFor(
      keysymForRune(ctrl && rune >= 0x41 && rune <= 0x5A ? rune + 0x20 : rune),
      ctrl: ctrl,
      alt: alt,
    ),
];
