import 'dart:convert';

/// Why a file or a folder could not be read.
///
/// The reads used to turn every failure into `null`, so a 404, a permission
/// error and a dropped connection all said "Failed to load file" — and none of
/// them offered to try again. Each kind here has its own words, the same ones
/// the desktop uses (docs/specs/65-fast-files.md, B1).
enum FileErrorKind {
  notFound,
  permissionDenied,
  tooLarge,
  badPath,
  isDirectory,
  unreachable,
  notConnected,
  other,
}

class FileError implements Exception {
  final FileErrorKind kind;

  /// The path that was asked for, shown under the message so a chip that
  /// named a file which does not exist says which one.
  final String path;

  /// The daemon's own words, when it sent any. Shown only for [other], where
  /// there is no better sentence to put in their place.
  final String? detail;

  /// For [tooLarge]: the file's size and the daemon's limit, in bytes.
  final int? size;
  final int? limit;

  const FileError(this.kind, this.path, {this.detail, this.size, this.limit});

  /// Reads the daemon's error body.
  ///
  /// The `code` field is the contract (spec 65). A daemon from before it sends
  /// only `error`, so the status code is the fallback — which is also why an
  /// unknown code falls through to the status rather than to [other].
  factory FileError.fromResponse(int status, String body, String path) {
    String? code;
    String? message;
    int? size;
    int? limit;
    try {
      final data = jsonDecode(body);
      if (data is Map) {
        code = data['code'] as String?;
        message = (data['error'] ?? data['message']) as String?;
        size = (data['size'] as num?)?.toInt();
        limit = (data['limit'] as num?)?.toInt();
      }
    } catch (_) {
      // A proxy's HTML page, or a bare "404 page not found". The status is
      // still worth reading.
    }
    final kind = switch (code) {
      'not_found' => FileErrorKind.notFound,
      'permission_denied' => FileErrorKind.permissionDenied,
      'too_large' => FileErrorKind.tooLarge,
      'bad_path' => FileErrorKind.badPath,
      'is_directory' => FileErrorKind.isDirectory,
      _ => switch (status) {
        404 => FileErrorKind.notFound,
        403 => FileErrorKind.permissionDenied,
        413 => FileErrorKind.tooLarge,
        400 => FileErrorKind.badPath,
        _ => FileErrorKind.other,
      },
    };
    return FileError(kind, path, detail: message, size: size, limit: limit);
  }

  /// One short line that says what failed.
  String get message => switch (kind) {
    FileErrorKind.notFound => 'Not found',
    FileErrorKind.permissionDenied => 'Permission denied',
    FileErrorKind.tooLarge => _tooLargeMessage,
    FileErrorKind.badPath => 'Not a path the host can read',
    FileErrorKind.isDirectory => 'This is a folder',
    FileErrorKind.unreachable => 'Host unreachable',
    FileErrorKind.notConnected => 'Host not connected',
    FileErrorKind.other =>
      detail == null || detail!.isEmpty ? 'Could not read this' : detail!,
  };

  String get _tooLargeMessage {
    if (size == null) return 'Too large';
    final limitPart = limit == null ? '' : ', the limit is ${_mb(limit!)}';
    return 'Too large (${_mb(size!)}$limitPart)';
  }

  static String _mb(int bytes) {
    final mb = bytes / (1024 * 1024);
    return '${mb >= 10 ? mb.round() : mb.toStringAsFixed(1)} MB';
  }

  @override
  String toString() => 'FileError(${kind.name}, $path)';
}
