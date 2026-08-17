import 'package:flutter_test/flutter_test.dart';

import 'package:helios/services/daemon_api_service.dart';

void main() {
  group('isSuccess', () {
    // POST /api/settings answers 204, and reading only 200 as success is what
    // made the sort toggle look broken: the daemon stored the mode, the app
    // decided the write had failed, and the switch never moved. Reordering
    // went with it — the list only offers a drag in manual mode.
    test('a write with no body to return still succeeded', () {
      expect(isSuccess(204), isTrue);
    });

    test('ordinary success', () {
      expect(isSuccess(200), isTrue);
    });

    test('failures stay failures', () {
      expect(isSuccess(400), isFalse);
      expect(isSuccess(401), isFalse);
      expect(isSuccess(500), isFalse);
    });
  });
}
