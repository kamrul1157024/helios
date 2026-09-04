import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:helios/utils/large_paste.dart';

void main() {
  group('isLargePaste', () {
    test('leaves a short paste alone', () {
      expect(isLargePaste('a stack trace line'), isFalse);
    });

    test('is large by length or by line count', () {
      expect(isLargePaste('x' * largePasteChars), isTrue);
      expect(isLargePaste('short\n' * largePasteLines), isTrue);
    });
  });

  group('insertedText', () {
    // A phone gives no paste callback, so a paste is recognised by its shape.
    test('reports a block dropped into the middle', () {
      expect(insertedText('look at  here', 'look at LOG here'), 'LOG');
    });

    test('reports an append', () {
      expect(insertedText('look at ', 'look at LOG'), 'LOG');
    });

    // Typing has the same shape, and only the size threshold separates them.
    test('reports a single typed character', () {
      expect(insertedText('ab', 'abc'), 'c');
    });

    test('a deletion is not an insertion', () {
      expect(insertedText('abc', 'ab'), isNull);
    });

    test('no change is not an insertion', () {
      expect(insertedText('abc', 'abc'), isNull);
    });
  });

  group('removeFirst', () {
    test('takes only the pasted block out', () {
      expect(
        removeFirst('look at this: LOG here', 'LOG'),
        'look at this:  here',
      );
    });

    // An identical block pasted earlier on purpose is not the one being filed.
    test('leaves an earlier identical block', () {
      expect(removeFirst('LOG and LOG', 'LOG'), 'and LOG');
    });

    test('leaves a draft the paste is no longer in', () {
      expect(removeFirst('user deleted it', 'LOG'), 'user deleted it');
    });
  });

  group('pastedTextFile', () {
    test('names by UTC stamp and carries the bytes', () {
      final file = pastedTextFile(
        'hello',
        at: DateTime.utc(2026, 8, 15, 4, 5, 6),
      );
      expect(file.name, 'pasted-20260815T040506.txt');
      expect(file.size, 5);
      expect(utf8.decode(file.bytes), 'hello');
      expect(
        file.storedPath,
        isNull,
        reason: 'not stored until the send uploads it',
      );
    });
  });
}
