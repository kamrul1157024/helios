import 'dart:convert';

class HeliosNotification {
  final String hostId;
  final String id;
  final String source;
  final String sourceSession;
  final String cwd;
  final String type;
  final String status;
  final String? title;
  final String? detail;
  final Map<String, dynamic>? payload;
  final Map<String, dynamic>? response;
  final String? resolvedAt;
  final String? resolvedSource;
  final String createdAt;

  HeliosNotification({
    this.hostId = '',
    required this.id,
    required this.source,
    required this.sourceSession,
    required this.cwd,
    required this.type,
    required this.status,
    this.title,
    this.detail,
    this.payload,
    this.response,
    this.resolvedAt,
    this.resolvedSource,
    required this.createdAt,
  });

  /// The provider that raised this: "claude" from "claude.permission".
  String get provider {
    final i = type.indexOf('.');
    return i < 0 ? type : type.substring(0, i);
  }

  /// What kind of request this is, independent of who raised it:
  /// "permission" from "codex.permission", "elicitation.form" from
  /// "claude.elicitation.form".
  ///
  /// Every switch in the app keys on this rather than on the whole type. That
  /// one change is what makes a second provider cost nothing here: its
  /// permission request is the same request, and it gets the same card.
  String get kind {
    final i = type.indexOf('.');
    return i < 0 ? '' : type.substring(i + 1);
  }

  factory HeliosNotification.fromJson(Map<String, dynamic> json, {String hostId = ''}) {
    Map<String, dynamic>? parseJson(dynamic raw) {
      if (raw == null) return null;
      if (raw is Map<String, dynamic>) return raw;
      if (raw is String) {
        try {
          final decoded = jsonDecode(raw);
          if (decoded is Map<String, dynamic>) return decoded;
        } catch (_) {}
      }
      return null;
    }

    return HeliosNotification(
      hostId: hostId,
      id: json['id'] as String,
      source: json['source'] as String? ?? 'claude',
      sourceSession: json['source_session'] as String? ?? '',
      cwd: json['cwd'] as String? ?? '',
      type: json['type'] as String,
      status: json['status'] as String,
      title: json['title'] as String?,
      detail: json['detail'] as String?,
      payload: parseJson(json['payload']),
      response: parseJson(json['response']),
      resolvedAt: json['resolved_at'] as String?,
      resolvedSource: json['resolved_source'] as String?,
      createdAt: json['created_at'] as String,
    );
  }

  bool get isPending => status == 'pending';

  /// The heading a card shows.
  ///
  /// The server's title wins. The fallback describes the kind of request
  /// without naming an agent, so it reads correctly for any provider — and it
  /// beats showing the raw type, which is what a user used to see.
  String get displayTitle => title ?? kindLabel;

  /// A human name for the kind of request, for when the server sent no title.
  String get kindLabel {
    switch (kind) {
      case 'permission':
        return 'Permission request';
      case 'question':
        return 'Question';
      case 'elicitation.form':
        return 'Input requested';
      case 'elicitation.url':
        return 'Authentication required';
      case 'trust':
        return 'Workspace trust required';
      case 'done':
        return 'Session completed';
      case 'error':
        return 'Session error';
      default:
        return type;
    }
  }
  String get displayDetail => detail ?? 'No details';

  String get timeAgo {
    try {
      final ts = createdAt.contains('T')
          ? createdAt
          : '${createdAt.replaceAll(' ', 'T')}Z';
      final d = DateTime.parse(ts);
      final diff = DateTime.now().toUtc().difference(d);
      if (diff.inSeconds < 60) return 'just now';
      if (diff.inMinutes < 60) return '${diff.inMinutes}m ago';
      if (diff.inHours < 24) return '${diff.inHours}h ago';
      return '${d.month}/${d.day}';
    } catch (_) {
      return createdAt;
    }
  }
}
