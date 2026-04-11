class ProviderInfo {
  final String id;
  final String name;
  final String icon;

  ProviderInfo({required this.id, required this.name, required this.icon});

  factory ProviderInfo.fromJson(Map<String, dynamic> json) {
    return ProviderInfo(
      id: json['id'] as String,
      name: json['name'] as String? ?? '',
      icon: json['icon'] as String? ?? '',
    );
  }
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
