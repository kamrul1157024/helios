import 'dart:convert';
import 'dart:io';
import 'package:flutter/foundation.dart';
import 'package:http/http.dart' as http;
import 'package:open_file/open_file.dart';
import 'package:package_info_plus/package_info_plus.dart';
import 'package:path_provider/path_provider.dart';
import 'package:permission_handler/permission_handler.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:url_launcher/url_launcher.dart';

const _repo = 'kamrul1157024/helios';
const _apiUrl = 'https://api.github.com/repos/$_repo/releases/latest';
const _releasesUrl = 'https://github.com/$_repo/releases/latest';

class UpdateInfo {
  final String latestVersion;
  final String? apkDownloadUrl; // null on non-Android
  final String releasesPageUrl;

  const UpdateInfo({
    required this.latestVersion,
    required this.apkDownloadUrl,
    required this.releasesPageUrl,
  });

  bool get canDirectInstall => apkDownloadUrl != null;
}

class UpdateService {
  UpdateService._();
  static final instance = UpdateService._();

  static const _dismissedKey = 'update.dismissed_version';

  String? _currentVersion;

  Future<String> get currentVersion async {
    _currentVersion ??= (await PackageInfo.fromPlatform()).version;
    return _currentVersion!;
  }

  /// Whether this version has already been waved away. Kept per version, so
  /// the next release is mentioned once and this one never again.
  Future<bool> isDismissed(String version) async {
    final prefs = await SharedPreferences.getInstance();
    return prefs.getString(_dismissedKey) == version;
  }

  Future<void> dismiss(String version) async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(_dismissedKey, version);
  }

  // Returns UpdateInfo if an update is available, null otherwise.
  Future<UpdateInfo?> checkForUpdate() async {
    try {
      final res = await http
          .get(Uri.parse(_apiUrl), headers: {'Accept': 'application/vnd.github+json'})
          .timeout(const Duration(seconds: 10));
      if (res.statusCode != 200) {
        debugPrint('[UpdateService] github said ${res.statusCode}');
        return null;
      }

      final data = jsonDecode(res.body) as Map<String, dynamic>;
      final tag = (data['tag_name'] as String?)?.replaceFirst('v', '') ?? '';
      if (tag.isEmpty) return null;

      final current = await currentVersion;
      debugPrint('[UpdateService] latest=$tag running=$current');
      if (!isNewer(tag, current)) return null;

      String? apkUrl;
      if (Platform.isAndroid) {
        final assets = (data['assets'] as List?) ?? [];
        final apkAsset = assets
            .cast<Map<String, dynamic>>()
            .where((a) => (a['name'] as String).endsWith('.apk'))
            .firstOrNull;
        apkUrl = apkAsset?['browser_download_url'] as String?;
      }

      return UpdateInfo(
        latestVersion: tag,
        apkDownloadUrl: apkUrl,
        releasesPageUrl: _releasesUrl,
      );
    } catch (e) {
      // Worth a line: a check that never succeeds is indistinguishable from
      // being up to date, and that is how a stale build goes unnoticed.
      debugPrint('[UpdateService] check failed: $e');
      return null;
    }
  }

  // Android: downloads APK to cache and opens system installer.
  // Desktop: opens releases page in browser.
  Future<void> install(UpdateInfo info, {void Function(double)? onProgress}) async {
    if (info.canDirectInstall) {
      await _downloadAndInstallApk(info.apkDownloadUrl!, onProgress: onProgress);
    } else {
      await launchUrl(Uri.parse(info.releasesPageUrl), mode: LaunchMode.externalApplication);
    }
  }

  Future<void> _downloadAndInstallApk(String url, {void Function(double)? onProgress}) async {
    final dir = await getTemporaryDirectory();
    final file = File('${dir.path}/helios-update.apk');

    final req = http.Request('GET', Uri.parse(url));
    final res = await req.send();
    final total = res.contentLength ?? 0;
    int received = 0;

    final sink = file.openWrite();
    await res.stream.listen((chunk) {
      sink.add(chunk);
      received += chunk.length;
      if (total > 0 && onProgress != null) {
        onProgress(received / total);
      }
    }).asFuture();
    await sink.close();

    // On Android 8+, request permission to install unknown apps.
    if (Platform.isAndroid) {
      final status = await Permission.requestInstallPackages.status;
      if (!status.isGranted) {
        await Permission.requestInstallPackages.request();
      }
    }

    await OpenFile.open(file.path);
  }

  /// Compares dotted versions, tolerating what a build actually reports: a
  /// debug build calls itself "0.2.0-dev", and parsing that strictly threw,
  /// which the catch below turned into "no update" for every dev build ever
  /// run.
  @visibleForTesting
  bool isNewer(String latest, String current) {
    final l = _parts(latest);
    final c = _parts(current);
    for (var i = 0; i < l.length || i < c.length; i++) {
      final a = i < l.length ? l[i] : 0;
      final b = i < c.length ? c[i] : 0;
      if (a != b) return a > b;
    }
    return false;
  }

  /// Leading digits of each dotted component; anything else counts as zero.
  List<int> _parts(String version) => version
      .split('.')
      .map((part) => int.tryParse(RegExp(r'^\d+').firstMatch(part)?.group(0) ?? '') ?? 0)
      .toList();
}
