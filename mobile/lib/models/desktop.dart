/// The host's remote desktop, as `GET /api/desktop` and the `desktop_state`
/// event describe it (spec 66, D3 and D4).
///
/// One desktop for each host, not for each session: an agent's windows and the
/// person watching them share the same screen.
library;

import 'dart:math' as math;

/// Where the desktop is on its way to being usable.
enum DesktopPhase {
  missing,
  installing,
  starting,
  ready,
  failed,
  unsupported;

  static DesktopPhase parse(String raw) => DesktopPhase.values.firstWhere(
    (p) => p.name == raw,
    orElse: () => DesktopPhase.failed,
  );
}

/// One install step and how far it got.
class DesktopStep {
  final String id;
  final String label;

  /// pending, running, done, skipped or failed.
  final String state;

  /// The last line the step printed, for a step that is running or failed.
  final String line;

  const DesktopStep({
    required this.id,
    required this.label,
    required this.state,
    this.line = '',
  });

  factory DesktopStep.fromJson(Map<String, dynamic> json) => DesktopStep(
    id: json['id'] as String? ?? '',
    label: json['label'] as String? ?? '',
    state: json['state'] as String? ?? 'pending',
    line: json['line'] as String? ?? '',
  );

  bool get finished => state == 'done' || state == 'skipped';
}

class DesktopStatus {
  final DesktopPhase phase;

  /// What the host still lacks: some of xrdp, xfce and guacd.
  final List<String> missing;

  /// Whether the daemon can escalate on its own (`sudo -n true` works).
  final bool canInstall;

  /// What to run in a terminal when it cannot.
  final String installCommand;
  final String display;

  /// The desktop session's Xft/DPI, or 0 when nobody knows yet.
  final int dpi;
  final int viewers;

  /// Why it failed, in words.
  final String error;
  final List<DesktopStep> steps;

  const DesktopStatus({
    required this.phase,
    this.missing = const [],
    this.canInstall = false,
    this.installCommand = 'helios setup desktop',
    this.display = '',
    this.dpi = 0,
    this.viewers = 0,
    this.error = '',
    this.steps = const [],
  });

  factory DesktopStatus.fromJson(Map<String, dynamic> json) {
    final command = json['install_command'] as String? ?? '';
    return DesktopStatus(
      phase: DesktopPhase.parse(json['state'] as String? ?? ''),
      missing: [
        for (final m in (json['missing'] as List?) ?? const [])
          if (m is String) m,
      ],
      canInstall: json['can_install'] as bool? ?? false,
      installCommand: command.isEmpty ? 'helios setup desktop' : command,
      display: json['display'] as String? ?? '',
      dpi: (json['dpi'] as num?)?.toInt() ?? 0,
      viewers: (json['viewers'] as num?)?.toInt() ?? 0,
      error: json['error'] as String? ?? '',
      steps: [
        for (final s in (json['steps'] as List?) ?? const [])
          if (s is Map<String, dynamic>) DesktopStep.fromJson(s),
      ],
    );
  }
}

/// A single-use pass for the tunnel. It is what the page holds instead of the
/// bearer token, which never leaves the app.
class DesktopTicket {
  final String ticket;
  final DateTime? expiresAt;

  const DesktopTicket(this.ticket, {this.expiresAt});
}

/// A desktop write the daemon refused, with its code and, for `needs_root`,
/// the command to run instead.
class DesktopActionException implements Exception {
  final int statusCode;
  final String code;
  final String message;

  const DesktopActionException(this.statusCode, this.code, this.message);

  @override
  String toString() => message.isEmpty ? 'HTTP $statusCode' : message;
}

// ─── Screen size and DPI (D9) ───────────────────────────────────────────────

/// How the remote screen's pixel count is chosen.
enum DesktopSizeMode {
  /// A fixed aspect ratio at a chosen height.
  fixed,

  /// The phone's own landscape shape at a chosen height, so the screen fills
  /// the phone when it is turned sideways.
  fit,

  /// An exact width and height.
  custom,
}

enum DesktopAspect {
  wide16x9(16, 9),
  wide16x10(16, 10),
  classic4x3(4, 3),
  photo3x2(3, 2),
  ultra21x9(21, 9);

  final int w;
  final int h;

  const DesktopAspect(this.w, this.h);

  String get label => '$w:$h';
}

/// The limits the daemon clamps every request to (D9).
const desktopMinWidth = 640;
const desktopMaxWidth = 7680;
const desktopMinHeight = 480;
const desktopMaxHeight = 4320;
const desktopMinDpi = 72;
const desktopMaxDpi = 288;

/// The heights a phone offers. 800 is there because 1280×800 is where the phone
/// starts: big enough for XFCE, small enough to read on a phone when zoomed.
const desktopHeights = [720, 800, 900, 1080, 1440];

/// The DPI presets, and what they mean in a scale a person recognises.
const desktopDpiPresets = {96: '100%', 120: '125%', 144: '150%', 192: '200%'};

/// The phone's choice for one host. Kept per host because two machines can
/// have very different screens behind them.
class DesktopDisplayPrefs {
  final DesktopSizeMode mode;
  final DesktopAspect aspect;
  final int height;
  final int customWidth;
  final int customHeight;
  final int dpi;

  /// Set once the phone's default DPI has been applied to this host, so it is
  /// applied once and never fights a choice made later — here or elsewhere.
  final bool dpiDefaulted;

  static const phoneDpi = 120;

  const DesktopDisplayPrefs({
    this.mode = DesktopSizeMode.fixed,
    this.aspect = DesktopAspect.wide16x10,
    this.height = 800,
    this.customWidth = 1280,
    this.customHeight = 800,
    this.dpi = phoneDpi,
    this.dpiDefaulted = false,
  });

  DesktopDisplayPrefs copyWith({
    DesktopSizeMode? mode,
    DesktopAspect? aspect,
    int? height,
    int? customWidth,
    int? customHeight,
    int? dpi,
    bool? dpiDefaulted,
  }) => DesktopDisplayPrefs(
    mode: mode ?? this.mode,
    aspect: aspect ?? this.aspect,
    height: height ?? this.height,
    customWidth: customWidth ?? this.customWidth,
    customHeight: customHeight ?? this.customHeight,
    dpi: dpi ?? this.dpi,
    dpiDefaulted: dpiDefaulted ?? this.dpiDefaulted,
  );

  Map<String, dynamic> toJson() => {
    'mode': mode.name,
    'aspect': aspect.name,
    'height': height,
    'custom_width': customWidth,
    'custom_height': customHeight,
    'dpi': dpi,
    'dpi_defaulted': dpiDefaulted,
  };

  factory DesktopDisplayPrefs.fromJson(Map<String, dynamic> json) {
    const fallback = DesktopDisplayPrefs();
    T pick<T extends Enum>(List<T> values, Object? raw, T or) =>
        values.firstWhere((v) => v.name == raw, orElse: () => or);
    return DesktopDisplayPrefs(
      mode: pick(DesktopSizeMode.values, json['mode'], fallback.mode),
      aspect: pick(DesktopAspect.values, json['aspect'], fallback.aspect),
      height: (json['height'] as num?)?.toInt() ?? fallback.height,
      customWidth:
          (json['custom_width'] as num?)?.toInt() ?? fallback.customWidth,
      customHeight:
          (json['custom_height'] as num?)?.toInt() ?? fallback.customHeight,
      dpi: (json['dpi'] as num?)?.toInt() ?? fallback.dpi,
      dpiDefaulted: json['dpi_defaulted'] as bool? ?? false,
    );
  }

  /// The remote screen's pixels for this choice.
  ///
  /// [screen] is the phone's screen in any orientation; only its shape is used,
  /// and always the landscape way round, so turning the phone does not resize
  /// the remote screen under the person.
  (int, int) resolve(math.Point<double> screen) {
    switch (mode) {
      case DesktopSizeMode.fixed:
        return clampDesktopSize(height * aspect.w / aspect.h, height);
      case DesktopSizeMode.fit:
        final long = math.max(screen.x, screen.y);
        final short = math.min(screen.x, screen.y);
        final ratio = short <= 0 ? 16 / 10 : long / short;
        return clampDesktopSize(height * ratio, height);
      case DesktopSizeMode.custom:
        return clampDesktopSize(customWidth, customHeight);
    }
  }
}

/// Rounds to even pixels and holds the size inside the daemon's limits.
/// Even, because RDP display updates refuse odd widths.
(int, int) clampDesktopSize(num width, num height) {
  int even(num v) => (v / 2).round() * 2;
  return (
    even(width).clamp(desktopMinWidth, desktopMaxWidth),
    even(height).clamp(desktopMinHeight, desktopMaxHeight),
  );
}
