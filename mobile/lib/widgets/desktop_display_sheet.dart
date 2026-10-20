import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../models/desktop.dart';

/// Screen size and DPI for the remote desktop (spec 66, D9).
///
/// Two separate things, so two separate sections. The size is how many pixels
/// the remote screen has and goes over the open tunnel; the DPI is how big text
/// is inside it and goes to the host, which keeps it for every viewer. Both
/// apply live, without a reconnect.
class DesktopDisplaySheet extends StatefulWidget {
  final DesktopDisplayPrefs prefs;

  /// The host's current DPI, or 0 when it does not know.
  final int hostDpi;

  /// The phone's screen, for the size the Fit mode gives.
  final math.Point<double> screen;

  /// A new size choice. The caller saves it and resizes the remote screen.
  final ValueChanged<DesktopDisplayPrefs> onPrefs;

  /// A new DPI. Answers whether the host took it.
  final Future<bool> Function(int dpi) onDpi;

  const DesktopDisplaySheet({
    super.key,
    required this.prefs,
    required this.hostDpi,
    required this.screen,
    required this.onPrefs,
    required this.onDpi,
  });

  @override
  State<DesktopDisplaySheet> createState() => _DesktopDisplaySheetState();
}

class _DesktopDisplaySheetState extends State<DesktopDisplaySheet> {
  late DesktopDisplayPrefs _prefs = widget.prefs;
  late int _dpi = widget.hostDpi > 0 ? widget.hostDpi : widget.prefs.dpi;
  late final _width = TextEditingController(text: '${_prefs.customWidth}');
  late final _height = TextEditingController(text: '${_prefs.customHeight}');
  late final _customDpi = TextEditingController(text: '$_dpi');
  late bool _dpiCustom = !desktopDpiPresets.containsKey(_dpi);
  bool _dpiBusy = false;

  @override
  void dispose() {
    _width.dispose();
    _height.dispose();
    _customDpi.dispose();
    super.dispose();
  }

  void _apply(DesktopDisplayPrefs next) {
    setState(() => _prefs = next);
    widget.onPrefs(next);
  }

  void _applyCustom() {
    final w = int.tryParse(_width.text);
    final h = int.tryParse(_height.text);
    if (w == null || h == null) return;
    final (cw, ch) = clampDesktopSize(w, h);
    _width.text = '$cw';
    _height.text = '$ch';
    _apply(_prefs.copyWith(customWidth: cw, customHeight: ch));
  }

  Future<void> _setDpi(int dpi) async {
    final value = dpi.clamp(desktopMinDpi, desktopMaxDpi);
    final previous = _dpi;
    setState(() {
      _dpi = value;
      _dpiBusy = true;
    });
    final ok = await widget.onDpi(value);
    if (!mounted) return;
    setState(() {
      _dpiBusy = false;
      if (!ok) _dpi = previous;
    });
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final (w, h) = _prefs.resolve(widget.screen);
    return SafeArea(
      child: SingleChildScrollView(
        padding: EdgeInsets.fromLTRB(
          20,
          8,
          20,
          20 + MediaQuery.viewInsetsOf(context).bottom,
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Text('Screen size', style: theme.textTheme.titleMedium),
                const Spacer(),
                Text(
                  '$w × $h',
                  key: const Key('desktop-size-label'),
                  style: theme.textTheme.titleSmall?.copyWith(
                    fontFeatures: const [FontFeature.tabularFigures()],
                  ),
                ),
              ],
            ),
            const SizedBox(height: 12),
            SegmentedButton<DesktopSizeMode>(
              showSelectedIcon: false,
              segments: const [
                ButtonSegment(
                  value: DesktopSizeMode.fixed,
                  label: Text('Fixed aspect'),
                ),
                ButtonSegment(
                  value: DesktopSizeMode.fit,
                  label: Text('Fit phone'),
                ),
                ButtonSegment(
                  value: DesktopSizeMode.custom,
                  label: Text('Custom'),
                ),
              ],
              selected: {_prefs.mode},
              onSelectionChanged: (s) => _apply(_prefs.copyWith(mode: s.first)),
            ),
            const SizedBox(height: 12),
            ..._modeControls(theme),
            const SizedBox(height: 24),
            Row(
              children: [
                Text('Text size (DPI)', style: theme.textTheme.titleMedium),
                const Spacer(),
                if (_dpiBusy)
                  const SizedBox(
                    width: 16,
                    height: 16,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  ),
              ],
            ),
            const SizedBox(height: 4),
            Text(
              'Set on the host, so everyone watching sees the same size.',
              style: theme.textTheme.bodySmall?.copyWith(
                color: theme.colorScheme.onSurfaceVariant,
              ),
            ),
            const SizedBox(height: 12),
            Wrap(
              spacing: 8,
              runSpacing: 8,
              children: [
                for (final e in desktopDpiPresets.entries)
                  ChoiceChip(
                    label: Text('${e.key} · ${e.value}'),
                    selected: !_dpiCustom && _dpi == e.key,
                    onSelected: _dpiBusy
                        ? null
                        : (_) {
                            setState(() => _dpiCustom = false);
                            _setDpi(e.key);
                          },
                  ),
                ChoiceChip(
                  label: const Text('Custom'),
                  selected: _dpiCustom,
                  onSelected: (_) => setState(() => _dpiCustom = true),
                ),
              ],
            ),
            if (_dpiCustom) ...[
              const SizedBox(height: 12),
              Row(
                children: [
                  Expanded(
                    child: _NumberField(
                      key: const Key('desktop-dpi-field'),
                      controller: _customDpi,
                      label: 'DPI ($desktopMinDpi–$desktopMaxDpi)',
                    ),
                  ),
                  const SizedBox(width: 12),
                  FilledButton.tonal(
                    onPressed: _dpiBusy
                        ? null
                        : () {
                            final dpi = int.tryParse(_customDpi.text);
                            if (dpi != null) _setDpi(dpi);
                          },
                    child: const Text('Set'),
                  ),
                ],
              ),
            ],
          ],
        ),
      ),
    );
  }

  List<Widget> _modeControls(ThemeData theme) {
    switch (_prefs.mode) {
      case DesktopSizeMode.fixed:
        return [
          _label(theme, 'Aspect ratio'),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              for (final a in DesktopAspect.values)
                ChoiceChip(
                  label: Text(a.label),
                  selected: _prefs.aspect == a,
                  onSelected: (_) => _apply(_prefs.copyWith(aspect: a)),
                ),
            ],
          ),
          const SizedBox(height: 12),
          ..._heights(theme),
        ];
      case DesktopSizeMode.fit:
        return [
          Text(
            'The shape of this phone held sideways, so the desktop fills it '
            'in landscape.',
            style: theme.textTheme.bodySmall?.copyWith(
              color: theme.colorScheme.onSurfaceVariant,
            ),
          ),
          const SizedBox(height: 12),
          ..._heights(theme),
        ];
      case DesktopSizeMode.custom:
        return [
          Row(
            children: [
              Expanded(
                child: _NumberField(
                  key: const Key('desktop-width-field'),
                  controller: _width,
                  label: 'Width',
                ),
              ),
              const Padding(
                padding: EdgeInsets.symmetric(horizontal: 8),
                child: Text('×'),
              ),
              Expanded(
                child: _NumberField(
                  key: const Key('desktop-height-field'),
                  controller: _height,
                  label: 'Height',
                ),
              ),
              const SizedBox(width: 12),
              FilledButton.tonal(
                onPressed: _applyCustom,
                child: const Text('Apply'),
              ),
            ],
          ),
          const SizedBox(height: 4),
          Text(
            'Width $desktopMinWidth–$desktopMaxWidth, '
            'height $desktopMinHeight–$desktopMaxHeight.',
            style: theme.textTheme.bodySmall?.copyWith(
              color: theme.colorScheme.onSurfaceVariant,
            ),
          ),
        ];
    }
  }

  List<Widget> _heights(ThemeData theme) => [
    _label(theme, 'Height'),
    Wrap(
      spacing: 8,
      runSpacing: 8,
      children: [
        for (final height in desktopHeights)
          ChoiceChip(
            label: Text('$height'),
            selected: _prefs.height == height,
            onSelected: (_) => _apply(_prefs.copyWith(height: height)),
          ),
      ],
    ),
  ];

  Widget _label(ThemeData theme, String text) => Padding(
    padding: const EdgeInsets.only(bottom: 8),
    child: Text(text, style: theme.textTheme.labelLarge),
  );
}

class _NumberField extends StatelessWidget {
  final TextEditingController controller;
  final String label;

  const _NumberField({
    super.key,
    required this.controller,
    required this.label,
  });

  @override
  Widget build(BuildContext context) => TextField(
    controller: controller,
    keyboardType: TextInputType.number,
    inputFormatters: [FilteringTextInputFormatter.digitsOnly],
    decoration: InputDecoration(
      labelText: label,
      isDense: true,
      border: const OutlineInputBorder(),
    ),
  );
}
