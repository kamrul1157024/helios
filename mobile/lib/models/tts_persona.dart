import 'dart:math';

class Segment {
  final String slot;
  final List<String> options;
  final double weight;

  const Segment({required this.slot, required this.options, this.weight = 1.0});
}

class SentenceGraph {
  final List<Segment> segments;

  const SentenceGraph(this.segments);

  String build(String target, Random random) {
    final parts = <String>[];
    for (final seg in segments) {
      if (seg.slot == 'target') {
        if (target.isNotEmpty) parts.add(target);
        continue;
      }
      if (seg.options.isEmpty) continue;
      if (random.nextDouble() > seg.weight) continue;
      parts.add(seg.options[random.nextInt(seg.options.length)]);
    }
    return parts.join(' ').trim();
  }
}

/// Extract just the filename (no path, no extension) for spoken output.
String shortenFilePath(String path) {
  if (path.isEmpty) return path;
  var name = path.split('/').last;
  final dotIdx = name.lastIndexOf('.');
  if (dotIdx > 0) {
    name = name.substring(0, dotIdx);
  }
  name = name.replaceAll('_', ' ');
  return name;
}

/// Shorten a shell command for spoken output.
String shortenCommand(String command) {
  if (command.isEmpty) return command;

  var line = command.split('\n').first.trim();
  final words = line.split(RegExp(r'\s+'));
  var i = 0;
  while (i < words.length) {
    final w = words[i];
    if (w.contains('=') && !w.startsWith('-')) {
      i++;
      continue;
    }
    if (w == 'sudo' || w == 'env' || w == 'nohup' || w == 'time') {
      i++;
      continue;
    }
    break;
  }

  if (i >= words.length) return words.last;

  var cmd = words[i];
  if (cmd.contains('/')) {
    cmd = cmd.split('/').last;
  }

  final remaining = words.skip(i + 1).where((w) => !w.startsWith('-')).toList();
  if (remaining.isNotEmpty && remaining.first.length <= 30) {
    return '$cmd ${remaining.first}';
  }

  return cmd;
}

// ==================== Sentence Graphs ====================
// Casual first-person tone: "Now I'm reading...", "Let me check...", etc.

const graphs = <String, SentenceGraph>{
  'tool.Read': SentenceGraph([
    Segment(slot: 'verb', options: [
      "Now I'm reading",
      "Let me check",
      "Looking at",
      "Let me read",
      "Pulling up",
      "Checking out",
      "Opening up",
      "Taking a look at",
    ]),
    Segment(slot: 'target', options: []),
  ]),
  'tool.Edit': SentenceGraph([
    Segment(slot: 'verb', options: [
      "Now I'm editing",
      "Making some changes to",
      "Let me update",
      "Tweaking",
      "Fixing up",
      "Adjusting",
      "Working on",
    ]),
    Segment(slot: 'target', options: []),
  ]),
  'tool.Write': SentenceGraph([
    Segment(slot: 'verb', options: [
      "Creating",
      "Writing out",
      "Let me write",
      "Putting together",
      "Drafting",
    ]),
    Segment(slot: 'target', options: []),
  ]),
  'tool.Bash': SentenceGraph([
    Segment(slot: 'verb', options: [
      "Running",
      "Let me run",
      "Firing off",
      "Executing",
      "Kicking off",
    ]),
    Segment(slot: 'target', options: []),
  ]),
  'tool.Grep': SentenceGraph([
    Segment(slot: 'verb', options: [
      "Searching for",
      "Looking for",
      "Let me find",
      "Scanning for",
      "Hunting down",
    ]),
    Segment(slot: 'target', options: []),
  ]),
  'tool.Glob': SentenceGraph([
    Segment(slot: 'verb', options: [
      "Finding files matching",
      "Looking for files like",
      "Scanning for",
      "Let me locate",
    ]),
    Segment(slot: 'target', options: []),
  ]),
  'tool.Agent': SentenceGraph([
    Segment(slot: 'verb', options: [
      "Handing this off to a sub-agent,",
      "Delegating this one,",
      "Spinning up a helper for",
      "Let me pass this along,",
    ]),
    Segment(slot: 'target', options: []),
  ]),
  'tool.default': SentenceGraph([
    Segment(slot: 'verb', options: ["Using", "Working with"]),
    Segment(slot: 'target', options: []),
  ]),
  'tool.failed': SentenceGraph([
    Segment(slot: 'phrase', options: [
      "That didn't work.",
      "Hmm, that failed.",
      "Oops, something went wrong.",
      "That one failed.",
      "Hit an error there.",
    ]),
    Segment(slot: 'target', options: [], weight: 0.7),
  ]),
  'permission': SentenceGraph([
    Segment(slot: 'prefix', options: [
      "Hey, I need your permission.",
      "I'd need your go-ahead here.",
      "Quick one, need your approval.",
      "Need a thumbs up from you.",
    ]),
    Segment(slot: 'filler', options: ["I want to", "I'd like to", "Can I"]),
    Segment(slot: 'target', options: []),
  ]),
  'done': SentenceGraph([
    Segment(slot: 'phrase', options: [
      "All done!",
      "That's it, finished.",
      "Done and dusted.",
      "Wrapped up.",
      "All finished.",
      "That's a wrap.",
    ]),
    Segment(slot: 'target', options: [], weight: 0.5),
  ]),
  'error': SentenceGraph([
    Segment(slot: 'phrase', options: [
      "Something went wrong.",
      "Ran into an error.",
      "Hit a snag here.",
      "Got an error.",
      "Oops, there's an error.",
    ]),
    Segment(slot: 'target', options: []),
  ]),
  'question': SentenceGraph([
    Segment(slot: 'phrase', options: [
      "Hey, I have a question.",
      "Quick question for you.",
      "I need to ask you something.",
      "Got a question.",
    ]),
    Segment(slot: 'target', options: []),
  ]),
  'trust': SentenceGraph([
    Segment(slot: 'phrase', options: [
      "Hey, this workspace needs trust.",
      "I need trust for this workspace first.",
      "Trust required for this workspace.",
    ]),
    Segment(slot: 'target', options: []),
  ]),
  'elicitation': SentenceGraph([
    Segment(slot: 'phrase', options: [
      "I need some input from you.",
      "Your input is needed here.",
    ]),
    Segment(slot: 'target', options: []),
  ]),
  'elicitation.url': SentenceGraph([
    Segment(slot: 'phrase', options: [
      "Hey, authentication needed. Check your device.",
      "Need you to authenticate. Check your phone.",
    ]),
  ]),
};
