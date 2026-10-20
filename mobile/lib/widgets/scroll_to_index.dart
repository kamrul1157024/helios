import 'dart:math' as math;

import 'package:flutter/widgets.dart';

/// Where a lazily built list of rows of unknown height should jump to.
///
/// A list that builds only what is on screen cannot say where row 4000 is
/// until it has been built, and it is not built until it is near the screen.
/// So the jump is made in passes: estimate, jump, see which rows got built,
/// correct, and stop as soon as the target row exists — then let
/// `ensureVisible` place it exactly.
///
/// The list's item builder wraps row [ScrollTarget.index] in a
/// `KeyedSubtree` with [ScrollTarget.key], and reports every index it builds
/// to [ScrollTarget.built].
class ScrollTarget {
  final int index;
  final GlobalKey key = GlobalKey();

  /// The rows built since the last pass, which is roughly what is on screen.
  final Set<int> built = <int>{};

  ScrollTarget(this.index);
}

Future<void> scrollToTarget({
  required ScrollController controller,
  required ScrollTarget target,
  required int count,
  double alignment = 0.1,
}) async {
  for (var pass = 0; pass < 8; pass++) {
    if (!controller.hasClients) return;
    final context = target.key.currentContext;
    if (context != null) {
      if (!context.mounted) return;
      await Scrollable.ensureVisible(context, alignment: alignment);
      return;
    }
    final position = controller.position;
    double next;
    if (pass == 0 || target.built.isEmpty) {
      next = position.maxScrollExtent * target.index / math.max(1, count - 1);
    } else {
      final first = target.built.reduce(math.min);
      final last = target.built.reduce(math.max);
      // What was built spans the viewport and the cache either side of it.
      final span = position.viewportDimension + 2 * 250.0;
      final perRow = span / math.max(1, last - first + 1);
      final middle = (first + last) / 2;
      next = position.pixels + (target.index - middle) * perRow;
    }
    target.built.clear();
    controller.jumpTo(
      next.clamp(position.minScrollExtent, position.maxScrollExtent),
    );
    await WidgetsBinding.instance.endOfFrame;
  }
}
