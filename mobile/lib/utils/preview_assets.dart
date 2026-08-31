/// Turning the links inside a preview into files on disk.
///
/// The mirror of `desktop/src/renderer/preview.ts`, and it has to stay one: the
/// interesting cases here are the ones that must be *refused*, and a rule that
/// holds on one client and not the other is worse than no rule.
///
/// Pure, so it can be tested without a widget or a daemon.
library;

/// Where a reference was found and what it asked for.
class AssetRef {
  /// 'img' becomes a data URL; 'style' is inlined as a &lt;style&gt; element.
  final String kind;

  /// The href or src exactly as written in the document.
  final String href;

  const AssetRef(this.kind, this.href);
}

/// A reference that resolved to a file worth reading.
class PlannedAsset {
  final String kind;
  final String href;

  /// Absolute, inside the root, and safe to ask the daemon for.
  final String path;

  const PlannedAsset(this.kind, this.href, this.path);
}

/// How much a preview may pull in behind the file that was opened. Generous
/// for a report, and meaningless for a hand-written page.
const int maxAssets = 24;
const int maxTotalBytes = 8 * 1024 * 1024;

/// Absolute, and free of `.` and `..`.
String _normalise(String path) {
  final out = <String>[];
  for (final part in path.split('/')) {
    if (part.isEmpty || part == '.') continue;
    if (part == '..') {
      if (out.isNotEmpty) out.removeLast();
      continue;
    }
    out.add(part);
  }
  return '/${out.join('/')}';
}

/// Whether [path] is [root] or sits underneath it.
bool withinRoot(String root, String path) {
  final base = _normalise(root);
  if (path == base) return true;
  return path.startsWith(base == '/' ? '/' : '$base/');
}

final _scheme = RegExp(r'^[a-z][a-z0-9+.\-]*:', caseSensitive: false);

/// The file a reference points at, or null if it must not be read.
///
/// Refused: anything with a scheme, protocol-relative `//host`, bare fragments,
/// and — the one that matters — anything that climbs out of the root with `..`.
/// A preview may read the checkout it belongs to and nothing else.
String? resolveAsset(String basePath, String href, String root) {
  final raw = href.trim();
  if (raw.isEmpty) return null;
  if (raw.startsWith('#')) return null;
  if (raw.startsWith('//')) return null;
  if (_scheme.hasMatch(raw)) return null;

  // The query and the fragment belong to a server, and there is not one.
  final clean = raw.split(RegExp(r'[?#]')).first;
  if (clean.isEmpty) return null;

  var decoded = clean;
  try {
    decoded = Uri.decodeComponent(clean);
  } catch (_) {
    // A stray % is not worth refusing the file over; use it as written.
  }

  // A leading slash in a page means the site root, and there is no site. The
  // checkout is the nearest honest reading, and the safe one.
  final base = basePath.substring(0, basePath.lastIndexOf('/'));
  final joined = decoded.startsWith('/') ? '$root/$decoded' : '$base/$decoded';
  final path = _normalise(joined);

  if (!withinRoot(root, path)) return null;
  if (path == _normalise(basePath)) return null;
  return path;
}

/// Which references to read, in order, within the caps. Deduped by path: a page
/// that uses one icon forty times is one read.
List<PlannedAsset> planAssets(List<AssetRef> refs, String basePath, String root) {
  final seen = <String>{};
  final planned = <PlannedAsset>[];

  for (final ref in refs) {
    if (planned.length >= maxAssets) break;
    final path = resolveAsset(basePath, ref.href, root);
    if (path == null) continue;
    final key = '${ref.kind}:$path';
    if (seen.contains(key)) continue;
    seen.add(key);
    planned.add(PlannedAsset(ref.kind, ref.href, path));
  }

  return planned;
}
