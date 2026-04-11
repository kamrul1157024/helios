import 'package:flutter/material.dart';

/// A shimmer-animated skeleton placeholder.
class Skeleton extends StatefulWidget {
  final double width;
  final double height;
  final BorderRadius borderRadius;

  const Skeleton({
    super.key,
    this.width = double.infinity,
    required this.height,
    this.borderRadius = const BorderRadius.all(Radius.circular(8)),
  });

  @override
  State<Skeleton> createState() => _SkeletonState();
}

class _SkeletonState extends State<Skeleton> with SingleTickerProviderStateMixin {
  late AnimationController _controller;
  late Animation<double> _animation;

  @override
  void initState() {
    super.initState();
    _controller = AnimationController(
      vsync: this,
      duration: const Duration(milliseconds: 1200),
    )..repeat();
    _animation = Tween<double>(begin: -1.0, end: 2.0).animate(
      CurvedAnimation(parent: _controller, curve: Curves.easeInOut),
    );
  }

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final baseColor = theme.colorScheme.surfaceContainerHighest;
    final highlightColor = theme.colorScheme.surfaceContainerHigh;

    return AnimatedBuilder(
      animation: _animation,
      builder: (context, _) {
        return Container(
          width: widget.width,
          height: widget.height,
          decoration: BoxDecoration(
            borderRadius: widget.borderRadius,
            gradient: LinearGradient(
              begin: Alignment.centerLeft,
              end: Alignment.centerRight,
              stops: [
                (_animation.value - 0.3).clamp(0.0, 1.0),
                _animation.value.clamp(0.0, 1.0),
                (_animation.value + 0.3).clamp(0.0, 1.0),
              ],
              colors: [baseColor, highlightColor, baseColor],
            ),
          ),
        );
      },
    );
  }
}

/// Skeleton that mimics a session card.
class SessionCardSkeleton extends StatelessWidget {
  const SessionCardSkeleton({super.key});

  @override
  Widget build(BuildContext context) {
    return Card(
      margin: const EdgeInsets.only(bottom: 8),
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
      child: Padding(
        padding: const EdgeInsets.all(12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                const Skeleton(width: 14, height: 14, borderRadius: BorderRadius.all(Radius.circular(7))),
                const SizedBox(width: 6),
                const Skeleton(width: 70, height: 18),
                const SizedBox(width: 8),
                const Skeleton(width: 90, height: 14),
                const Spacer(),
                const Skeleton(width: 40, height: 14),
              ],
            ),
            const SizedBox(height: 8),
            const Skeleton(width: 180, height: 16),
            const SizedBox(height: 4),
            const Skeleton(width: 140, height: 14),
            const SizedBox(height: 4),
            const Skeleton(width: 60, height: 12),
          ],
        ),
      ),
    );
  }
}

/// Skeleton that mimics a notification card.
class NotificationCardSkeleton extends StatelessWidget {
  const NotificationCardSkeleton({super.key});

  @override
  Widget build(BuildContext context) {
    return Card(
      margin: const EdgeInsets.only(bottom: 8),
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                const Skeleton(width: 70, height: 20),
                const SizedBox(width: 8),
                const Skeleton(width: 120, height: 16),
              ],
            ),
            const SizedBox(height: 8),
            const Skeleton(height: 50),
            const SizedBox(height: 8),
            Row(
              children: [
                const Expanded(child: Skeleton(height: 14)),
                const SizedBox(width: 8),
                const Skeleton(width: 50, height: 14),
              ],
            ),
            const SizedBox(height: 12),
            Row(
              children: [
                const Expanded(child: Skeleton(height: 36)),
                const SizedBox(width: 8),
                const Expanded(child: Skeleton(height: 36)),
              ],
            ),
          ],
        ),
      ),
    );
  }
}

/// Skeleton that mimics a chat message list (mix of user + assistant + tool bubbles).
class MessageListSkeleton extends StatelessWidget {
  const MessageListSkeleton({super.key});

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
      child: Column(
        children: const [
          // User message (right-aligned, short)
          _UserBubbleSkeleton(width: 180),
          SizedBox(height: 8),
          // Tool use (full width, compact)
          _ToolUseSkeleton(),
          SizedBox(height: 4),
          // Tool result
          _ToolResultSkeleton(),
          SizedBox(height: 8),
          // Assistant message (left-aligned, tall)
          _AssistantBubbleSkeleton(height: 80),
          SizedBox(height: 8),
          // Another user message
          _UserBubbleSkeleton(width: 140),
          SizedBox(height: 8),
          // Tool use
          _ToolUseSkeleton(),
          SizedBox(height: 4),
          _ToolResultSkeleton(),
          SizedBox(height: 8),
          // Longer assistant reply
          _AssistantBubbleSkeleton(height: 120),
          SizedBox(height: 8),
          // User
          _UserBubbleSkeleton(width: 200),
          SizedBox(height: 8),
          // Assistant
          _AssistantBubbleSkeleton(height: 60),
        ],
      ),
    );
  }
}

class _UserBubbleSkeleton extends StatelessWidget {
  final double width;
  const _UserBubbleSkeleton({required this.width});

  @override
  Widget build(BuildContext context) {
    return Align(
      alignment: Alignment.centerRight,
      child: Skeleton(
        width: width,
        height: 36,
        borderRadius: const BorderRadius.only(
          topLeft: Radius.circular(16),
          topRight: Radius.circular(16),
          bottomLeft: Radius.circular(16),
          bottomRight: Radius.circular(4),
        ),
      ),
    );
  }
}

class _AssistantBubbleSkeleton extends StatelessWidget {
  final double height;
  const _AssistantBubbleSkeleton({required this.height});

  @override
  Widget build(BuildContext context) {
    return Align(
      alignment: Alignment.centerLeft,
      child: FractionallySizedBox(
        widthFactor: 0.85,
        child: Skeleton(
          height: height,
          borderRadius: const BorderRadius.only(
            topLeft: Radius.circular(16),
            topRight: Radius.circular(16),
            bottomLeft: Radius.circular(4),
            bottomRight: Radius.circular(16),
          ),
        ),
      ),
    );
  }
}

class _ToolUseSkeleton extends StatelessWidget {
  const _ToolUseSkeleton();

  @override
  Widget build(BuildContext context) {
    return const Skeleton(
      height: 28,
      borderRadius: BorderRadius.all(Radius.circular(8)),
    );
  }
}

class _ToolResultSkeleton extends StatelessWidget {
  const _ToolResultSkeleton();

  @override
  Widget build(BuildContext context) {
    return const Align(
      alignment: Alignment.centerLeft,
      child: Skeleton(
        width: 80,
        height: 14,
        borderRadius: BorderRadius.all(Radius.circular(4)),
      ),
    );
  }
}
