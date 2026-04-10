import 'dart:convert';

class HeliosNotification {
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

  factory HeliosNotification.fromJson(Map<String, dynamic> json) {
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

  String get displayTitle => title ?? type;
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
