import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart' as rp;
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

import 'package:helios/providers/daemon_providers.dart';
import 'package:helios/services/api_client.dart';
import 'package:helios/services/daemon_api_service.dart';
import 'package:helios/services/host_manager.dart';

const fakeHost = 'h1';

/// A daemon that holds a small tree in memory and speaks the spec 65 file
/// routes: listings, raw reads with ETags, git status, touched files and
/// prompts. Every request is recorded, so a test can say what went out.
class FakeFilesDaemon {
  /// Path to text. A path that ends in `/` is a folder; its entries are every
  /// path one level below it.
  final Map<String, String> files;

  /// Path to raw bytes, for pictures.
  final Map<String, Uint8List> binaries;

  /// Changes `git status` reports, relative to [gitRoot].
  final Map<String, String> gitChanges;
  final String gitRoot;
  final String branch;

  /// What `/api/sessions/{id}/touched` answers.
  final List<Map<String, dynamic>> touched;

  /// Paths marked ignored in their listing.
  final Set<String> ignored;

  /// The branch's base, and the commits on top of it, newest first. A base
  /// with commits makes the Changes view open on the review.
  final String base;
  final List<Map<String, dynamic>> commits;

  /// What the review against [base] changed, relative to [gitRoot].
  final Map<String, String> reviewChanges;

  /// When set, every file and listing read answers this way instead.
  http.Response Function(http.Request request)? failWith;

  /// When true, every request throws as a dropped connection would.
  bool unreachable = false;

  /// How `/api/sessions/{id}/send` answers: null for accepted, or the error
  /// and the status it comes with.
  String? sendError;
  int sendStatus = 409;

  /// Whether this daemon predates `/api/file/raw`.
  final bool oldDaemon;

  final List<http.Request> requests = [];

  FakeFilesDaemon({
    this.files = const {},
    this.binaries = const {},
    this.gitChanges = const {},
    this.gitRoot = '/repo',
    this.branch = 'main',
    this.touched = const [],
    this.ignored = const {},
    this.oldDaemon = false,
    this.base = '',
    this.commits = const [],
    this.reviewChanges = const {},
  });

  static const modTime = '2026-09-01T10:00:00Z';

  String etagOf(String path) {
    final size = files[path]?.length ?? binaries[path]?.length ?? 0;
    return 'W/"$size-1"';
  }

  Future<http.Response> handle(http.Request request) async {
    requests.add(request);
    if (unreachable) throw const SocketException('Network is unreachable');
    final path = request.url.path;
    final query = request.url.queryParameters['path'] ?? '';
    if (path.startsWith('/api/sessions/') && path.endsWith('/send')) {
      final error = sendError;
      return error == null
          ? http.Response('{}', 200)
          : http.Response(jsonEncode({'error': error}), sendStatus);
    }
    if (path.endsWith('/touched')) {
      return http.Response(jsonEncode({'files': touched}), 200);
    }
    if (path == '/api/git/status') return _gitStatus();
    if (path == '/api/git/log') return _gitLog();
    if (path == '/api/git/changes') return _gitChanges();
    if (path == '/api/git/diff') return _gitDiff(request);
    final fail = failWith;
    if (fail != null &&
        (path == '/api/files' || path.startsWith('/api/file'))) {
      return fail(request);
    }
    if (path == '/api/files') return _list(query);
    if (path == '/api/file/raw') {
      if (oldDaemon) return http.Response('404 page not found', 404);
      return _raw(request, query);
    }
    if (path == '/api/file') return _json(query);
    return http.Response('{}', 404);
  }

  http.Response _gitStatus() {
    final unstaged = [
      for (final e in gitChanges.entries)
        if (e.value != '?') {'path': e.key, 'status': e.value},
    ];
    final untracked = [
      for (final e in gitChanges.entries)
        if (e.value == '?') {'path': e.key, 'status': '?'},
    ];
    return http.Response(
      jsonEncode({
        'root': gitRoot,
        'branch': branch,
        'dirty': gitChanges.isNotEmpty,
        'ahead': 0,
        'behind': 0,
        'staged': [],
        'unstaged': unstaged,
        'untracked': untracked,
      }),
      200,
    );
  }

  http.Response _gitLog() => http.Response(
    jsonEncode({
      'root': gitRoot,
      'branch': branch,
      'base': base,
      'scope': base.isEmpty ? 'all' : 'branch',
      'commits': commits,
      'has_more': false,
    }),
    200,
  );

  http.Response _gitChanges() => http.Response(
    jsonEncode({
      'from': base,
      'to': 'HEAD',
      'single': false,
      'files': [
        for (final e in reviewChanges.entries)
          {'path': e.key, 'status': e.value, 'insertions': 3, 'deletions': 1},
      ],
      'insertions': 3 * reviewChanges.length,
      'deletions': reviewChanges.length,
    }),
    200,
  );

  http.Response _gitDiff(http.Request request) {
    final file = request.url.queryParameters['file'] ?? '';
    final text = files['$gitRoot/$file'] ?? '';
    final lines = text.split('\n');
    final diff = [
      'diff --git a/$file b/$file',
      '--- a/$file',
      '+++ b/$file',
      '@@ -1,${lines.length} +1,${lines.length + 1} @@',
      for (var i = 0; i < lines.length; i++)
        i == 1 ? '-${lines[i]}' : ' ${lines[i]}',
      if (lines.length > 1) '+${lines[1]} // changed by the agent',
    ].join('\n');
    return http.Response(
      jsonEncode({
        'file': file,
        'language': file.split('.').last,
        'diff': diff,
        'stat': '+1 -1',
      }),
      200,
    );
  }

  http.Response _list(String dir) {
    final prefix = dir.endsWith('/') ? dir : '$dir/';
    final names = <String, bool>{};
    for (final key in [...files.keys, ...binaries.keys]) {
      if (!key.startsWith(prefix) || key == prefix) continue;
      final rest = key.substring(prefix.length);
      final cut = rest.indexOf('/');
      if (cut < 0) {
        names.putIfAbsent(rest, () => false);
      } else {
        names[rest.substring(0, cut)] = true;
      }
    }
    if (names.isEmpty && !files.containsKey(prefix)) {
      return http.Response(
        jsonEncode({'error': 'no such directory', 'code': 'not_found'}),
        404,
      );
    }
    final sorted = names.keys.toList()
      ..sort((a, b) {
        final dirs = (names[b]! ? 1 : 0) - (names[a]! ? 1 : 0);
        return dirs != 0 ? dirs : a.compareTo(b);
      });
    return http.Response(
      jsonEncode({
        'path': dir,
        'entries': [
          for (final name in sorted)
            {
              'name': name,
              'path': '$prefix$name',
              'is_dir': names[name],
              'size':
                  files['$prefix$name']?.length ??
                  binaries['$prefix$name']?.length ??
                  0,
              'mod_time': modTime,
              if (ignored.contains('$prefix$name')) 'ignored': true,
            },
        ],
      }),
      200,
    );
  }

  http.Response _raw(http.Request request, String path) {
    final text = files[path];
    final bytes = binaries[path];
    if (text == null && bytes == null) {
      return http.Response(
        jsonEncode({'error': 'not found', 'code': 'not_found'}),
        404,
      );
    }
    final etag = etagOf(path);
    if (request.headers['If-None-Match'] == etag) {
      return http.Response('', 304, headers: {'etag': etag});
    }
    final body = bytes ?? Uint8List.fromList(utf8.encode(text!));
    return http.Response.bytes(
      body,
      200,
      headers: {
        'etag': etag,
        'x-helios-mod-time': modTime,
        'x-helios-size': '${body.length}',
        'x-helios-encoding': bytes == null ? 'utf8' : 'binary',
      },
    );
  }

  http.Response _json(String path) {
    final text = files[path];
    final bytes = binaries[path];
    if (text == null && bytes == null) {
      return http.Response(jsonEncode({'error': 'not found'}), 404);
    }
    return http.Response(
      jsonEncode({
        'path': path,
        'size': text?.length ?? bytes!.length,
        'content': text ?? base64Encode(bytes!),
        if (bytes != null) 'encoding': 'base64',
        'mod_time': modTime,
      }),
      200,
    );
  }

  /// The paths of the raw and JSON reads, in order.
  List<String> get reads => [
    for (final r in requests)
      if (r.url.path.startsWith('/api/file'))
        '${r.url.path} ${r.url.queryParameters['path']}',
  ];

  DaemonAPIService service() => DaemonAPIService(
    hostId: fakeHost,
    serverUrl: 'http://localhost:1',
    api: ApiClient(
      serverUrl: 'http://localhost:1',
      deviceId: 'd1',
      privateKeySeed: Uint8List(32),
      client: MockClient(handle),
    ),
  );
}

/// Wraps [child] the way the app does, with [daemon] as the only host.
Widget filesApp(
  FakeFilesDaemon daemon, {
  Widget? home,
  GlobalKey<NavigatorState>? navigatorKey,
  ThemeData? theme,
}) {
  return rp.ProviderScope(
    overrides: [
      hostManagerProvider.overrideWithValue(HostManager()),
      serviceProvider(fakeHost).overrideWithValue(daemon.service()),
    ],
    child: MaterialApp(
      navigatorKey: navigatorKey,
      theme: theme,
      debugShowCheckedModeBanner: false,
      home: home ?? const SizedBox.shrink(),
    ),
  );
}
