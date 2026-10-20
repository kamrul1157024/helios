import 'dart:io';

import 'package:flutter_test/flutter_test.dart';

/// Runs the desktop page's own tests (desktop_page_test.js) under node, so
/// the page's touch and view math is checked with the rest of the app.
///
/// Skipped where node is not installed: the page needs no build step, and the
/// tests need nothing but node itself.
void main() {
  final node = _which('node');

  test('desktop page gesture and view math', skip: node == null, () async {
    final result = await Process.run(node!, ['test/desktop_page_test.js']);
    expect(result.exitCode, 0, reason: '${result.stdout}${result.stderr}');
  });
}

String? _which(String name) {
  for (final dir in (Platform.environment['PATH'] ?? '').split(':')) {
    final file = File('$dir/$name');
    if (file.existsSync()) return file.path;
  }
  return null;
}
