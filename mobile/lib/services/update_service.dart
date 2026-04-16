import 'dart:convert';
import 'dart:io';
import 'package:http/http.dart' as http;
import 'package:open_file/open_file.dart';
import 'package:package_info_plus/package_info_plus.dart';
import 'package:path_provider/path_provider.dart';
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

  String? _currentVersion;

  Future<String> get currentVersion async {
    _currentVersion ??= (await PackageInfo.fromPlatform()).version;
    return _currentVersion!;
  }

  // Returns UpdateInfo if an update is available, null otherwise.
  Future<UpdateInfo?> checkForUpdate() async {
    try {
      final res = await http
          .get(Uri.parse(_apiUrl), headers: {'Accept': 'application/vnd.github+json'})
          .timeout(const Duration(seconds: 10));
      if (res.statusCode != 200) return null;

      final data = jsonDecode(res.body) as Map<String, dynamic>;
      final tag = (data['tag_name'] as String?)?.replaceFirst('v', '') ?? '';
      if (tag.isEmpty) return null;

      final current = await currentVersion;
      if (!_isNewer(tag, current)) return null;

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
    } catch (_) {
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

    await OpenFile.open(file.path);
  }

  bool _isNewer(String latest, String current) {
    try {
      final l = latest.split('.').map(int.parse).toList();
      final c = current.split('.').map(int.parse).toList();
      for (int i = 0; i < l.length && i < c.length; i++) {
        if (l[i] > c[i]) return true;
        if (l[i] < c[i]) return false;
      }
      return l.length > c.length;
    } catch (_) {
      return false;
    }
  }
}
