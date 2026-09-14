import 'package:flutter/material.dart';
import 'package:flutter_svg/flutter_svg.dart';

/// Which agent a session runs, as its maker draws it.
///
/// Anything that is not Codex is Claude: `source` is what the daemon's provider
/// registry stamped on the session, and every provider but one is Anthropic's.
class ProviderMark extends StatelessWidget {
  const ProviderMark({
    super.key,
    required this.source,
    this.size = 14,
    this.color,
  });

  final String source;
  final double size;
  final Color? color;

  @override
  Widget build(BuildContext context) {
    final codex = source == 'codex';
    final tint = color ?? Theme.of(context).colorScheme.onSurfaceVariant;
    return SvgPicture.asset(
      codex ? 'assets/logos/openai.svg' : 'assets/logos/anthropic.svg',
      width: size,
      height: size,
      colorFilter: ColorFilter.mode(tint, BlendMode.srcIn),
      semanticsLabel: codex ? 'Codex' : 'Claude Code',
    );
  }
}
