/// What a file is, from its name.
///
/// The mirror of `desktop/src/renderer/filetype.ts`. Two small maps that agree
/// beat a field on the wire that has to be kept in step with both clients — and
/// the answer is needed before any bytes arrive, which a served mime type
/// cannot provide.
library;

/// Formats a webview can draw, and the mime each needs in a data URL.
const _imageMime = <String, String>{
  'apng': 'image/apng',
  'avif': 'image/avif',
  'bmp': 'image/bmp',
  'gif': 'image/gif',
  'ico': 'image/x-icon',
  'jpeg': 'image/jpeg',
  'jpg': 'image/jpeg',
  'png': 'image/png',
  'svg': 'image/svg+xml',
  'webp': 'image/webp',
};

const _htmlExtensions = <String>{'html', 'htm', 'xhtml'};

/// Lowercased, and empty for a dotfile or a name with no dot in it.
String extensionOf(String path) {
  final name = path.substring(path.lastIndexOf('/') + 1);
  final dot = name.lastIndexOf('.');
  // `> 0` rather than `>= 0`: `.gitignore` is a name, not an extension.
  return dot > 0 ? name.substring(dot + 1).toLowerCase() : '';
}

bool isImagePath(String path) => _imageMime.containsKey(extensionOf(path));

bool isHtmlPath(String path) => _htmlExtensions.contains(extensionOf(path));

/// The mime for a data URL. Only the ones a preview inlines are named; anything
/// else is served as bytes, which is what a browser assumes anyway.
String mimeForPath(String path) {
  final extension = extensionOf(path);
  final image = _imageMime[extension];
  if (image != null) return image;
  if (_htmlExtensions.contains(extension)) return 'text/html';
  if (extension == 'css') return 'text/css';
  return 'application/octet-stream';
}
