class ProviderInfo {
  final String id;
  final String name;
  final String icon;

  /// Permission modes this provider's agent can run under, most restrictive
  /// first, or empty if it has no such concept. Served by the daemon rather
  /// than hardcoded here: the vocabulary is the agent CLI's own, and it has
  /// already gained a mode between releases.
  final List<String> permissionModes;

  ProviderInfo({
    required this.id,
    required this.name,
    required this.icon,
    this.permissionModes = const [],
  });

  factory ProviderInfo.fromJson(Map<String, dynamic> json) {
    return ProviderInfo(
      id: json['id'] as String,
      name: json['name'] as String? ?? '',
      icon: json['icon'] as String? ?? '',
      permissionModes:
          (json['permission_modes'] as List?)
              ?.map((m) => m as String)
              .toList() ??
          const [],
    );
  }
}

/// PermissionMode describes one mode for display. The daemon sends bare
/// identifiers, so the labels and the warning live on the client.
class PermissionMode {
  final String id;
  final String label;
  final String description;

  /// Modes that hand the agent a blank cheque. Worth a confirmation before the
  /// user picks one from a phone.
  bool get isRisky => id == 'bypassPermissions' || id == 'dontAsk';

  const PermissionMode(this.id, this.label, this.description);

  static const _known = <String, PermissionMode>{
    'plan': PermissionMode(
      'plan',
      'Plan',
      'Research and plan only — makes no changes',
    ),
    'manual': PermissionMode('manual', 'Manual', 'Asks before every action'),
    'acceptEdits': PermissionMode(
      'acceptEdits',
      'Accept edits',
      'Edits files without asking; still asks for commands',
    ),
    'auto': PermissionMode(
      'auto',
      'Auto',
      'Decides routine actions itself, asks about risky ones',
    ),
    'dontAsk': PermissionMode(
      'dontAsk',
      "Don't ask",
      'Never prompts; denies anything it cannot do safely',
    ),
    'bypassPermissions': PermissionMode(
      'bypassPermissions',
      'Bypass permissions',
      'Skips every permission check. Only for sandboxes.',
    ),
  };

  /// Describes an identifier, falling back to the raw value so a mode added by
  /// a newer CLI still renders instead of disappearing.
  factory PermissionMode.of(String id) =>
      _known[id] ?? PermissionMode(id, id, '');
}

class ModelInfo {
  final String id;
  final String name;
  final String description;
  final String? contextWindow;

  ModelInfo({
    required this.id,
    required this.name,
    required this.description,
    this.contextWindow,
  });

  factory ModelInfo.fromJson(Map<String, dynamic> json) {
    return ModelInfo(
      id: json['id'] as String,
      name: json['name'] as String? ?? '',
      description: json['description'] as String? ?? '',
      contextWindow: json['context_window'] as String?,
    );
  }
}
