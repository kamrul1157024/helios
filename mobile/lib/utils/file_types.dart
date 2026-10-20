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

/// Which picture a row in the browser draws for a file.
///
/// A family rather than an icon, so this file stays free of Flutter: the
/// browser and the Changes view both map a family to a glyph, and before this
/// the browser kept a second extension map of its own that disagreed with the
/// one above.
enum FileFamily {
  code,
  markdown,
  text,
  data,
  image,
  html,
  style,
  shell,
  pdf,
  archive,
  other,
}

const _families = <String, FileFamily>{
  'dart': FileFamily.code,
  'go': FileFamily.code,
  'py': FileFamily.code,
  'js': FileFamily.code,
  'mjs': FileFamily.code,
  'cjs': FileFamily.code,
  'ts': FileFamily.code,
  'tsx': FileFamily.code,
  'jsx': FileFamily.code,
  'java': FileFamily.code,
  'kt': FileFamily.code,
  'swift': FileFamily.code,
  'rs': FileFamily.code,
  'c': FileFamily.code,
  'cc': FileFamily.code,
  'cpp': FileFamily.code,
  'h': FileFamily.code,
  'hpp': FileFamily.code,
  'cs': FileFamily.code,
  'rb': FileFamily.code,
  'php': FileFamily.code,
  'lua': FileFamily.code,
  'sql': FileFamily.code,
  'proto': FileFamily.code,
  'md': FileFamily.markdown,
  'markdown': FileFamily.markdown,
  'txt': FileFamily.text,
  'rst': FileFamily.text,
  'log': FileFamily.text,
  'json': FileFamily.data,
  'yaml': FileFamily.data,
  'yml': FileFamily.data,
  'toml': FileFamily.data,
  'xml': FileFamily.data,
  'csv': FileFamily.data,
  'ini': FileFamily.data,
  'lock': FileFamily.data,
  'mod': FileFamily.data,
  'sum': FileFamily.data,
  'css': FileFamily.style,
  'scss': FileFamily.style,
  'sass': FileFamily.style,
  'less': FileFamily.style,
  'sh': FileFamily.shell,
  'bash': FileFamily.shell,
  'zsh': FileFamily.shell,
  'fish': FileFamily.shell,
  'pdf': FileFamily.pdf,
  'zip': FileFamily.archive,
  'gz': FileFamily.archive,
  'tgz': FileFamily.archive,
  'tar': FileFamily.archive,
  'xz': FileFamily.archive,
  'apk': FileFamily.archive,
  'dmg': FileFamily.archive,
};

FileFamily familyOf(String path) {
  final extension = extensionOf(path);
  if (_imageMime.containsKey(extension)) return FileFamily.image;
  if (_htmlExtensions.contains(extension)) return FileFamily.html;
  final known = _families[extension];
  if (known != null) return known;
  // Names that are a language without saying so.
  final name = path.substring(path.lastIndexOf('/') + 1);
  if (name == 'Makefile' || name == 'Dockerfile') return FileFamily.code;
  return FileFamily.other;
}

bool isMarkdownPath(String path) => familyOf(path) == FileFamily.markdown;
