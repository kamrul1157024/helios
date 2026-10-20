import 'package:flutter/widgets.dart';

/// The names the screens under a session push themselves with.
///
/// "Back to chat" and "Ask AI" pop to the session by name. They used to pop
/// *past* the routes they knew about — `name != '/file-browser'` — which
/// passed at once when the top route was the viewer, so a sent question left
/// the reader in the viewer, wondering whether it had gone (spec 65, F6).
const sessionRoute = '/session';
const fileBrowserRoute = '/file-browser';
const fileViewerRoute = '/file-viewer';

/// Pops everything above the session the current screen was opened from.
///
/// Stops at the first route too, so a screen opened some other way — a
/// notification straight into a file — lands somewhere rather than popping the
/// app shut.
void popToSession(NavigatorState navigator) {
  navigator.popUntil(
    (route) => route.settings.name == sessionRoute || route.isFirst,
  );
}
