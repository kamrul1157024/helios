import 'dart:convert';
import 'dart:typed_data';

import '../services/api_client.dart';

/// When a pasted block is big enough to be worth offering as a file instead.
///
/// Not a correctness limit — the daemon delivers a prompt of any size — but a
/// wall of pasted log lines reads better to the agent as a path it can open
/// than as ten thousand characters of prompt.
const int largePasteChars = 2000;
const int largePasteLines = 50;

bool isLargePaste(String text) =>
    text.length >= largePasteChars || '\n'.allMatches(text).length + 1 >= largePasteLines;

/// The text inserted in one edit, or null when the change was not a single
/// insertion.
///
/// Flutter gives a text field no paste callback, so a paste is recognised by
/// its shape: one contiguous block appearing in one change. Typing produces the
/// same shape a character at a time, which the size threshold then rejects.
String? insertedText(String before, String after) {
  if (after.length <= before.length) return null;
  var head = 0;
  while (head < before.length && before[head] == after[head]) {
    head++;
  }
  var tail = 0;
  while (tail < before.length - head &&
      before[before.length - 1 - tail] == after[after.length - 1 - tail]) {
    tail++;
  }
  return after.substring(head, after.length - tail);
}

/// Takes the pasted block back out of the draft, once only.
///
/// The paste has already landed in the field by the time the offer can be
/// accepted — it is an offer, not an interception, so ignoring it changes
/// nothing — and removing every occurrence would also delete an identical
/// block the user pasted earlier on purpose.
String removeFirst(String draft, String pasted) {
  final at = draft.indexOf(pasted);
  if (at == -1) return draft;
  return (draft.substring(0, at) + draft.substring(at + pasted.length)).trim();
}

/// A pasted block as an attachment, so it takes the path every file takes.
UploadFile pastedTextFile(String text, {DateTime? at}) {
  final stamp = (at ?? DateTime.now())
      .toUtc()
      .toIso8601String()
      .replaceAll(RegExp(r'[-:]'), '')
      .replaceAll(RegExp(r'\..+'), '');
  return UploadFile(
    name: 'pasted-$stamp.txt',
    bytes: Uint8List.fromList(utf8.encode(text)),
  );
}
