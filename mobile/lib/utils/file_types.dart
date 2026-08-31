/// What a file is, from its name.
///
/// The mirror of `desktop/src/renderer/filetype.ts`. Two small maps that agree
/// beat a field on the wire that has to be kept in step with both clients — and
/// the answer is needed before any bytes arrive, which a served mime type
/// cannot provide.
library;

const _imageExtensions = <String>{
  'apng',
  'avif',
  'bmp',
  'gif',
  'ico',
  'jpeg',
  'jpg',
  'png',
  'svg',
  'webp',
};

/// Lowercased, and empty for a dotfile or a name with no dot in it.
String extensionOf(String path) {
  final name = path.substring(path.lastIndexOf('/') + 1);
  final dot = name.lastIndexOf('.');
  // `> 0` rather than `>= 0`: `.gitignore` is a name, not an extension.
  return dot > 0 ? name.substring(dot + 1).toLowerCase() : '';
}

bool isImagePath(String path) => _imageExtensions.contains(extensionOf(path));
