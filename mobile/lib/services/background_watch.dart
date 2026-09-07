import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:flutter_foreground_task/flutter_foreground_task.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'background_watcher.dart';

/// The app-facing switch for the background watcher.
///
/// Split from [BackgroundWatcher] so the app can ask whether to hand over
/// without pulling the service isolate's entry point into every caller.

const _keyEnabled = 'background_watch_enabled';

/// Whether the watcher should run while the app is closed.
///
/// On by default. An approval that never reaches the phone is the failure the
/// whole feature exists to prevent, so the setting is there to turn it off
/// rather than to turn it on.
Future<bool> backgroundWatchEnabled() async {
  if (!Platform.isAndroid) return false;
  try {
    final prefs = await SharedPreferences.getInstance();
    return prefs.getBool(_keyEnabled) ?? true;
  } catch (_) {
    return true;
  }
}

Future<void> setBackgroundWatchEnabled(bool value) async {
  final prefs = await SharedPreferences.getInstance();
  await prefs.setBool(_keyEnabled, value);
  if (!value) await stopBackgroundWatch();
}

Future<bool> isBackgroundWatchRunning() async {
  if (!Platform.isAndroid) return false;
  return FlutterForegroundTask.isRunningService;
}

/// Start the service, unless it is already up.
Future<void> startBackgroundWatch() async {
  if (!Platform.isAndroid) return;
  if (await FlutterForegroundTask.isRunningService) return;

  initBackgroundWatcher();
  await FlutterForegroundTask.startService(
    serviceTypes: [ForegroundServiceTypes.specialUse],
    notificationTitle: 'Helios',
    notificationText: 'Watching for approvals',
    callback: startBackgroundWatcher,
  );
}

Future<void> stopBackgroundWatch() async {
  if (!Platform.isAndroid) return;
  if (!await FlutterForegroundTask.isRunningService) return;
  await FlutterForegroundTask.stopService();
}

/// Whether Android will let the service keep its socket open in Doze.
Future<bool> isBatteryOptimised() async {
  if (!Platform.isAndroid) return false;
  try {
    return !await FlutterForegroundTask.isIgnoringBatteryOptimizations;
  } catch (e) {
    debugPrint('Battery optimisation check failed: $e');
    return false;
  }
}

/// Ask to be exempted from battery optimisation.
///
/// Deliberately not called on launch: a prompt like this before the user has
/// seen a single notification reads as an app overreaching. It belongs next to
/// the toggle that turns the watcher on.
Future<void> requestBatteryExemption() async {
  if (!Platform.isAndroid) return;
  try {
    await FlutterForegroundTask.requestIgnoreBatteryOptimization();
  } catch (e) {
    debugPrint('Battery exemption request failed: $e');
  }
}
