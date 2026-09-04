import 'package:flutter_test/flutter_test.dart';
import 'package:helios/utils/file_types.dart';
import 'package:helios/utils/preview_assets.dart';

const root = '/Users/kim/work/repo';
const page = '/Users/kim/work/repo/docs/report.html';

void main() {
  group('file types', () {
    test('an extension is the part after the last dot, lowercased', () {
      expect(extensionOf('/a/b/c.PNG'), 'png');
      expect(extensionOf('/a/b/archive.tar.gz'), 'gz');
      expect(extensionOf('/a/b/Makefile'), '');
      // A dotfile is a name, not an extension.
      expect(extensionOf('/a/b/.gitignore'), '');
    });

    test('knows a picture and a page from anything else', () {
      expect(isImagePath('/a/logo.svg'), isTrue);
      expect(isImagePath('/a/shot.JPEG'), isTrue);
      expect(isImagePath('/a/main.go'), isFalse);
      expect(isHtmlPath('/a/index.html'), isTrue);
      expect(isHtmlPath('/a/index.htm'), isTrue);
      expect(isHtmlPath('/a/index.html.bak'), isFalse);
    });

    test('the mime is the one a data URL needs', () {
      expect(mimeForPath('/a/x.png'), 'image/png');
      expect(mimeForPath('/a/x.svg'), 'image/svg+xml');
      expect(mimeForPath('/a/x.css'), 'text/css');
      expect(mimeForPath('/a/x.bin'), 'application/octet-stream');
    });
  });

  group('resolveAsset', () {
    test('resolves a relative reference against the file that holds it', () {
      expect(resolveAsset(page, './chart.png', root), '$root/docs/chart.png');
      expect(resolveAsset(page, 'chart.png', root), '$root/docs/chart.png');
      expect(
        resolveAsset(page, 'img/chart.png', root),
        '$root/docs/img/chart.png',
      );
      expect(
        resolveAsset(page, '../assets/logo.svg', root),
        '$root/assets/logo.svg',
      );
      expect(resolveAsset(page, './x.png?v=2#frag', root), '$root/docs/x.png');
      expect(
        resolveAsset(page, 'my%20chart.png', root),
        '$root/docs/my chart.png',
      );
    });

    test('reads an absolute reference against the checkout', () {
      expect(
        resolveAsset(page, '/assets/logo.svg', root),
        '$root/assets/logo.svg',
      );
      // Which is also what stops a leading slash reaching outside it.
      expect(resolveAsset(page, '/etc/passwd', root), '$root/etc/passwd');
    });

    test('refuses anything that leaves the root', () {
      expect(resolveAsset(page, '../../../../.ssh/id_rsa', root), isNull);
      expect(resolveAsset(page, '../../../etc/passwd', root), isNull);
      expect(resolveAsset(page, './../../..', root), isNull);
    });

    test('refuses anything that is not a path on this machine', () {
      for (final href in [
        'http://example.com/x.png',
        'https://example.com/x.png',
        '//example.com/x.png',
        'data:image/png;base64,AAAA',
        'file:///etc/passwd',
        'javascript:alert(1)',
        '#anchor',
        '',
        '   ',
      ]) {
        expect(resolveAsset(page, href, root), isNull, reason: 'for "$href"');
      }
    });

    test('does not let a page inline itself', () {
      expect(resolveAsset(page, './report.html', root), isNull);
    });

    test('is not fooled by a sibling that shares a prefix', () {
      expect(withinRoot('/a/repo', '/a/repo/x'), isTrue);
      expect(withinRoot('/a/repo', '/a/repo'), isTrue);
      expect(withinRoot('/a/repo', '/a/repo-evil/x'), isFalse);
    });
  });

  group('planAssets', () {
    test('dedupes by path and keeps the order it found them', () {
      final planned = planAssets(
        const [
          AssetRef('img', './a.png'),
          AssetRef('img', 'a.png'),
          AssetRef('style', './s.css'),
          AssetRef('img', './b.png'),
        ],
        page,
        root,
      );

      expect(planned.map((a) => '${a.kind} ${a.path}'), [
        'img $root/docs/a.png',
        'style $root/docs/s.css',
        'img $root/docs/b.png',
      ]);
    });

    test('drops what it cannot resolve rather than failing', () {
      final planned = planAssets(
        const [
          AssetRef('img', 'https://example.com/x.png'),
          AssetRef('img', './good.png'),
          AssetRef('img', '../../../../etc/passwd'),
        ],
        page,
        root,
      );

      expect(planned.length, 1);
      expect(planned.first.path, '$root/docs/good.png');
    });

    test('stops at the cap', () {
      final refs = List.generate(
        maxAssets + 10,
        (i) => AssetRef('img', './img-$i.png'),
      );
      expect(planAssets(refs, page, root).length, maxAssets);
    });
  });
}
