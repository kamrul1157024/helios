/// A colour per author, so a channel can be followed without reading names.
///
/// Several sessions talking in one channel rendered every name in the same
/// accent, which made a busy channel a wall of identical headers: telling who
/// said what meant reading each one.
///
/// The hue comes from the session id rather than the title, because the title
/// is generated and can change under a conversation that is already coloured.
/// The same derivation as the desktop's `author-colour.ts`, so a channel looks
/// the same on both.
library;

import 'package:flutter/material.dart';

/// How many hues to choose between. Enough to separate a channel of six, and
/// coarse enough that two adjacent ones are still distinguishable.
const _hues = 12;

/// The person has no colour of their own. `user` is the one author that is not
/// a session, and leaving it in the ordinary text colour keeps it distinct
/// from every agent more sharply than a thirteenth hue would.
Color? authorColour(String author, Brightness brightness) {
  if (author.isEmpty || author == 'user') return null;
  final hue = (_hash(author) % _hues) * (360 / _hues);
  // Fixed saturation and lightness so the hue is the only thing that varies
  // and no session is dealt a name too dim to read. Lifted on a dark surface
  // and dropped on a light one, since the same colour cannot serve both.
  return HSLColor.fromAHSL(
    1,
    hue,
    0.62,
    brightness == Brightness.dark ? 0.66 : 0.38,
  ).toColor();
}

/// The one or two letters that stand for an author on its avatar.
///
/// Taken from the resolved title rather than the id, because the avatar sits
/// beside the name and initials that do not match read as somebody else's. A
/// title like "[INFRA] Debug SSH auth" leads with a bracket, so anything that
/// is not a letter or a digit is skipped.
String authorInitials(String from) {
  final words = from
      .split(RegExp(r'[^\p{L}\p{N}]+', unicode: true))
      .where((w) => w.isNotEmpty)
      .toList();
  if (words.isEmpty) return '?';
  if (words.length == 1) {
    final one = words.first;
    return (one.length >= 2 ? one.substring(0, 2) : one).toUpperCase();
  }
  return (words[0][0] + words[1][0]).toUpperCase();
}

/// FNV-1a, for a hue that is stable across restarts and machines.
///
/// Not for uniqueness: two sessions sharing a hue is a cosmetic collision, and
/// the name beside the colour still says which is which.
int _hash(String text) {
  var value = 0x811c9dc5;
  for (final unit in text.codeUnits) {
    value ^= unit;
    value = (value * 0x01000193) & 0xffffffff;
  }
  return value;
}
