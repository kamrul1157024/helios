import 'dart:ui';

class HostConnection {
  final String id;
  String label;
  final String serverUrl;
  final String deviceId;
  int colorIndex;
  String? hostname;
  final DateTime addedAt;

  HostConnection({
    required this.id,
    required this.label,
    required this.serverUrl,
    required this.deviceId,
    required this.colorIndex,
    this.hostname,
    required this.addedAt,
  });

  Color get color => hostColors[colorIndex % hostColors.length];

  Map<String, dynamic> toJson() => {
        'id': id,
        'label': label,
        'serverUrl': serverUrl,
        'deviceId': deviceId,
        'colorIndex': colorIndex,
        'hostname': hostname,
        'addedAt': addedAt.toIso8601String(),
      };

  factory HostConnection.fromJson(Map<String, dynamic> json) {
    return HostConnection(
      id: json['id'] as String,
      label: json['label'] as String,
      serverUrl: json['serverUrl'] as String,
      deviceId: json['deviceId'] as String,
      colorIndex: json['colorIndex'] as int? ?? 0,
      hostname: json['hostname'] as String?,
      addedAt: DateTime.parse(json['addedAt'] as String),
    );
  }

  static const hostColors = [
    Color(0xFF4285F4), // Blue
    Color(0xFF34A853), // Green
    Color(0xFFFBBC04), // Amber
    Color(0xFFEA4335), // Red
    Color(0xFF9C27B0), // Purple
    Color(0xFF00ACC1), // Cyan
    Color(0xFFFF7043), // Deep Orange
    Color(0xFF5C6BC0), // Indigo
  ];
}
