/// A saved prompt with something that decides when it runs.
///
/// One of four things decides: a cron expression (timer), a single moment
/// (once), a command checked on a cron (monitor), or another job finishing
/// (after). [kind] says which, and the fields for the other three are empty.
///
/// See docs/specs/55-scheduled-runs.md.
class Schedule {
  final String id;
  final String name;
  final String kind; // timer | once | monitor | after
  final bool enabled;

  /// Drives a timer and a monitor; for a monitor it is how often to look.
  final String cron;
  final String runAt;

  /// The job this one follows, and whether a failed parent still releases it.
  final String afterId;
  final String afterWhen; // success | any

  final String mode; // new | resume
  final String prompt;
  final String cwd;
  final String provider;
  final String model;
  final String permissionMode;
  final String targetSession;

  /// A monitor's check: a command through sh -c, or a script run directly.
  final String checkCmd;
  final String checkFile;
  final String checkMatch;
  final String lastCheckAt;
  final int? lastCheckExit;
  final String lastCheckOut;

  final String nextRunAt;
  final String lastFiredAt;
  final String lastSessionId;

  /// running until the session it started goes idle, then ok.
  final String lastStatus;
  final String lastError;
  final String doneAt;

  /// Consecutive failures, so a list can say "failing for six nights", and how
  /// often it has fired today, which is how a runaway monitor is noticed.
  final int failStreak;
  final String failingSince;
  final int firesToday;

  const Schedule({
    required this.id,
    required this.name,
    required this.kind,
    required this.enabled,
    this.cron = '',
    this.runAt = '',
    this.afterId = '',
    this.afterWhen = '',
    this.mode = 'new',
    this.prompt = '',
    this.cwd = '',
    this.provider = '',
    this.model = '',
    this.permissionMode = '',
    this.targetSession = '',
    this.checkCmd = '',
    this.checkFile = '',
    this.checkMatch = '',
    this.lastCheckAt = '',
    this.lastCheckExit,
    this.lastCheckOut = '',
    this.nextRunAt = '',
    this.lastFiredAt = '',
    this.lastSessionId = '',
    this.lastStatus = '',
    this.lastError = '',
    this.doneAt = '',
    this.failStreak = 0,
    this.failingSince = '',
    this.firesToday = 0,
  });

  factory Schedule.fromJson(Map<String, dynamic> json) {
    String str(String key) => (json[key] as String?) ?? '';
    return Schedule(
      id: str('id'),
      name: str('name'),
      kind: str('kind').isEmpty ? 'timer' : str('kind'),
      enabled: json['enabled'] as bool? ?? false,
      cron: str('cron'),
      runAt: str('run_at'),
      afterId: str('after_id'),
      afterWhen: str('after_when'),
      mode: str('mode').isEmpty ? 'new' : str('mode'),
      prompt: str('prompt'),
      cwd: str('cwd'),
      provider: str('provider'),
      model: str('model'),
      permissionMode: str('permission_mode'),
      targetSession: str('target_session'),
      checkCmd: str('check_cmd'),
      checkFile: str('check_file'),
      checkMatch: str('check_match'),
      lastCheckAt: str('last_check_at'),
      lastCheckExit: json['last_check_exit'] as int?,
      lastCheckOut: str('last_check_out'),
      nextRunAt: str('next_run_at'),
      lastFiredAt: str('last_fired_at'),
      lastSessionId: str('last_session_id'),
      lastStatus: str('last_status'),
      lastError: str('last_error'),
      doneAt: str('done_at'),
      failStreak: json['fail_streak'] as int? ?? 0,
      failingSince: str('failing_since'),
      firesToday: json['fires_today'] as int? ?? 0,
    );
  }

  bool get isMonitor => kind == 'monitor';

  /// What the check will be run as, whichever way it was written.
  String get check => checkCmd.isNotEmpty ? checkCmd : checkFile;

  /// The cron expression in words.
  ///
  /// Nobody reads `0 9 * * 1-5` at a glance, and a list that cannot be skimmed
  /// is one where a wrong schedule hides in plain sight. Only the shapes people
  /// actually write are spelled out; anything else keeps its expression.
  String get cronWords {
    final fields = cron.trim().split(RegExp(r'\s+'));
    if (fields.length != 5) return cron;
    final [min, hour, dom, month, dow] = fields;
    final everyDay = dom == '*' && month == '*' && dow == '*';

    final everyMinutes = RegExp(r'^\*/(\d+)$').firstMatch(min);
    if (everyMinutes != null && hour == '*' && everyDay) {
      final n = int.parse(everyMinutes.group(1)!);
      return n == 1 ? 'every minute' : 'every $n minutes';
    }
    if (min == '0' && hour == '*' && everyDay) return 'every hour';

    final everyHours = RegExp(r'^\*/(\d+)$').firstMatch(hour);
    if (everyHours != null && RegExp(r'^\d+$').hasMatch(min) && everyDay) {
      final n = int.parse(everyHours.group(1)!);
      return n == 1 ? 'every hour' : 'every $n hours';
    }
    if (RegExp(r'^\d+$').hasMatch(min) && RegExp(r'^\d+$').hasMatch(hour)) {
      final time = '${hour.padLeft(2, '0')}:${min.padLeft(2, '0')}';
      if (everyDay) return 'every day at $time';
      if (dom == '*' && month == '*' && dow == '1-5')
        return 'weekdays at $time';
      if (dom == '*' && month == '*' && RegExp(r'^\d$').hasMatch(dow)) {
        const days = [
          'Sunday',
          'Monday',
          'Tuesday',
          'Wednesday',
          'Thursday',
          'Friday',
          'Saturday',
        ];
        return '${days[int.parse(dow)]}s at $time';
      }
    }
    return cron;
  }

  /// The one line under the name: what it does, in words rather than in cron.
  String get subtitle {
    final where = mode == 'resume'
        ? 'into ${targetSession.length > 8 ? targetSession.substring(0, 8) : targetSession}'
        : (cwd.isEmpty ? 'home' : cwd);
    switch (kind) {
      case 'monitor':
        return '$cronWords · $check';
      case 'once':
        return 'once · $where';
      case 'after':
        return '${afterWhen == 'any' ? 'either way' : 'on success'} · $where';
      default:
        return '$cronWords · $where';
    }
  }

  /// Where this schedule stands right now, in one word.
  String get stateWord {
    if (lastStatus == 'running') return 'running';
    if (lastStatus == 'missed') return 'missed';
    if (lastStatus == 'blocked') return 'blocked';
    if (doneAt.isNotEmpty) return 'done';
    if (!enabled) return 'paused';
    if (kind == 'after') return 'waiting';
    if (lastStatus == 'failed') {
      return failStreak > 1 ? 'failed ×$failStreak' : 'failed';
    }
    return untilWords(nextRunAt);
  }
}

/// How far off something is, in words a person reads at a glance.
String untilWords(String stamp) {
  if (stamp.isEmpty) return '—';
  final at = DateTime.tryParse(stamp)?.toLocal();
  if (at == null) return '—';
  final until = at.difference(DateTime.now());
  if (until.isNegative) return 'due';
  if (until.inMinutes < 1) return 'in ${until.inSeconds}s';
  if (until.inMinutes < 60) return 'in ${until.inMinutes}m';
  if (until.inHours < 12)
    return 'in ${until.inHours}h ${until.inMinutes % 60}m';
  return '${_weekday(at)} ${at.hour.toString().padLeft(2, '0')}:'
      '${at.minute.toString().padLeft(2, '0')}';
}

/// How long ago something happened.
String agoWords(String stamp) {
  if (stamp.isEmpty) return 'never';
  final at = DateTime.tryParse(stamp)?.toLocal();
  if (at == null) return 'never';
  final since = DateTime.now().difference(at);
  if (since.inMinutes < 1) return 'just now';
  if (since.inMinutes < 60) return '${since.inMinutes}m ago';
  if (since.inHours < 24) return '${since.inHours}h ago';
  return '${since.inDays}d ago';
}

String _weekday(DateTime at) {
  const names = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'];
  return names[at.weekday - 1];
}

/// What a check saw, when it was run by hand from the editor.
class CheckResult {
  final int exit;
  final String output;
  final bool matched;
  final bool failed;
  final String error;

  const CheckResult({
    required this.exit,
    required this.output,
    required this.matched,
    required this.failed,
    this.error = '',
  });

  factory CheckResult.fromJson(Map<String, dynamic> json) => CheckResult(
    exit: json['exit'] as int? ?? 0,
    output: (json['output'] as String?) ?? '',
    matched: json['matched'] as bool? ?? false,
    failed: json['failed'] as bool? ?? false,
    error: (json['error'] as String?) ?? '',
  );
}
