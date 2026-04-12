import 'dart:math';
import '../models/message.dart';
import '../models/notification.dart';
import '../models/tts_persona.dart';
import '../providers/claude/notification_ext.dart';
import '../services/voice_service.dart';
import '../utils/markdown_stripper.dart';

class TTSTransformer {
  static final _random = Random();

  /// Transform a transcript message into spoken text. Returns null to skip.
  static String? transformMessage(Message message) {
    switch (message.role) {
      case 'assistant':
        if (message.content == null || message.content!.isEmpty) return null;
        return stripMarkdown(message.content!);

      case 'tool_use':
        if (!VoiceService.instance.toolCallTtsEnabled) return null;
        final raw = message.summary ?? '';
        String target;
        switch (message.tool) {
          case 'Bash':
            target = shortenCommand(raw);
          case 'Read':
          case 'Write':
          case 'Edit':
            target = shortenFilePath(raw);
          default:
            target = raw;
        }
        final key = 'tool.${message.tool ?? "default"}';
        final graph = graphs[key] ?? graphs['tool.default'];
        if (graph == null) return null;
        return graph.build(target, _random);

      case 'tool_result':
        if (!VoiceService.instance.toolCallTtsEnabled) return null;
        if (message.success == true) return null;
        final graph = graphs['tool.failed'];
        if (graph == null) return null;
        return graph.build(message.tool ?? 'command', _random);

      case 'user':
        return null;

      default:
        return null;
    }
  }

  /// Transform a notification into spoken text. Returns null to skip.
  static String? transformNotification(HeliosNotification n) {
    if (!n.isPending) return null;

    String graphKey;
    String target;

    switch (n.type) {
      case 'claude.permission':
        graphKey = 'permission';
        final verb = _permissionVerb(n.toolName);
        target = '$verb ${n.detail ?? ''}'.trim();
      case 'claude.question':
        graphKey = 'question';
        target = n.detail ?? '';
      case 'claude.trust':
        graphKey = 'trust';
        target = n.detail ?? '';
      case 'claude.done':
        graphKey = 'done';
        target = n.detail ?? '';
      case 'claude.error':
        graphKey = 'error';
        target = n.detail ?? '';
      case 'claude.elicitation.url':
        graphKey = 'elicitation.url';
        target = '';
      default:
        if (n.type.startsWith('claude.elicitation.')) {
          graphKey = 'elicitation';
          target = n.detail ?? '';
        } else {
          return null;
        }
    }

    final graph = graphs[graphKey];
    if (graph == null) return null;
    return graph.build(target, _random);
  }

  /// Build a global spoken announcement for a notification, with session context.
  /// [sessionTitle] is the session's displayTitle (lastUserMessage or title).
  static String? transformGlobalNotification(HeliosNotification n, String? sessionTitle) {
    if (!n.isPending && n.type != 'claude.done' && n.type != 'claude.error') return null;

    final context = sessionTitle != null && sessionTitle.isNotEmpty
        ? _shortenPrompt(sessionTitle)
        : null;

    switch (n.type) {
      case 'claude.done':
        if (context != null) {
          return [
            'Your task, $context, is done. Let me know if you need anything else.',
            'Finished with $context. Ready for the next one.',
            'All done with $context.',
          ][_random.nextInt(3)];
        }
        return 'A session just finished. Let me know if you need anything.';

      case 'claude.error':
        if (context != null) {
          return [
            'Ran into a problem with $context. You might want to check it out.',
            'Got an error on $context.',
            'Something went wrong with $context.',
          ][_random.nextInt(3)];
        }
        return 'A session hit an error.';

      case 'claude.permission':
        final verb = _permissionVerb(n.toolName);
        if (context != null) {
          return [
            'Hey, I need permission to $verb on $context.',
            'Need your go-ahead to $verb for $context.',
            'Quick one, can I $verb? This is for $context.',
          ][_random.nextInt(3)];
        }
        return 'Permission needed to $verb.';

      case 'claude.question':
        if (context != null) {
          return 'Got a question about $context. ${n.detail ?? ''}'.trim();
        }
        return 'Claude has a question. ${n.detail ?? ''}'.trim();

      case 'claude.trust':
        return 'A workspace needs trust. ${n.detail ?? ''}'.trim();

      default:
        if (n.type.startsWith('claude.elicitation.')) {
          if (context != null) {
            return 'Need some input for $context.';
          }
          return 'Input requested.';
        }
        return null;
    }
  }

  /// Speak a session status change (idle = done, error = error).
  /// [sessionTitle] provides context about what the session was doing.
  /// [global] uses longer phrasing with session context for global announcements.
  static String? transformSessionStatus(String status, String? sessionTitle, {bool global = false}) {
    final context = sessionTitle != null && sessionTitle.isNotEmpty
        ? _shortenPrompt(sessionTitle)
        : null;

    if (status == 'idle') {
      if (global) {
        if (context != null) {
          return [
            'Your task, $context, is done. Let me know if you need anything else.',
            'Finished with $context. Ready for the next one.',
            'All done with $context.',
          ][_random.nextInt(3)];
        }
        return 'A session just finished. Let me know if you need anything.';
      }
      // Session-level: shorter
      if (context != null) {
        return [
          'All done with $context.',
          'Finished. $context is done.',
          'Done with $context.',
        ][_random.nextInt(3)];
      }
      return [
        'All done!',
        "That's it, finished.",
        'Done and dusted.',
        'Wrapped up.',
      ][_random.nextInt(4)];
    }

    if (status == 'error') {
      if (global) {
        if (context != null) {
          return [
            'Ran into a problem with $context. You might want to check it out.',
            'Got an error on $context.',
            'Something went wrong with $context.',
          ][_random.nextInt(3)];
        }
        return 'A session hit an error.';
      }
      if (context != null) {
        return [
          'Hit an error on $context.',
          'Something went wrong with $context.',
        ][_random.nextInt(2)];
      }
      return [
        'Something went wrong.',
        'Ran into an error.',
        'Hit a snag here.',
      ][_random.nextInt(3)];
    }

    return null;
  }

  /// Shorten a user prompt for spoken context (first ~60 chars).
  static String _shortenPrompt(String prompt) {
    final clean = prompt.replaceAll('\n', ' ').trim();
    if (clean.length <= 60) return clean;
    // Cut at word boundary
    final cut = clean.substring(0, 60);
    final lastSpace = cut.lastIndexOf(' ');
    if (lastSpace > 30) return cut.substring(0, lastSpace);
    return cut;
  }

  static String _permissionVerb(String? tool) {
    switch (tool) {
      case 'Edit':
        return 'edit';
      case 'Write':
        return 'write';
      case 'Bash':
        return 'run';
      case 'Read':
        return 'read';
      default:
        return 'use ${tool ?? "a tool on"}';
    }
  }
}
