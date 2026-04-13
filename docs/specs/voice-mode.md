# Voice Mode for Helios Mobile

## Context

Users want hands-free interaction with Claude Code sessions from the mobile app — speak prompts instead of typing, hear responses read aloud. This adds full voice mode: STT input, TTS output, with a session-level toggle and persona-styled speech with randomized sentence generation. Android + iOS only (skip macOS).

## Architecture Overview

```
Message/Notification
       ↓
TTSTransformer.transform(item, persona)
       ↓
SentenceGraph.build(target)  →  randomized spoken text
       ↓
VoiceService.speak(text)
       ↓
flutter_tts engine
```

## UI Mockups

### Session Detail Screen — Voice mode OFF (current)

```
┌──────────────────────────────────┐
│ ← Session Title          ■ ⏸ ▶  │  ← app bar actions (stop/pause/resume)
├──────────────────────────────────┤
│                                  │
│  ┌─────────────────────────┐     │
│  │ Assistant message...    │     │
│  │ Here's the fix for the  │     │
│  │ auth bug in handler.go  │     │
│  └─────────────────────────┘     │
│                                  │
│     ┌─────────────────────────┐  │
│     │ User message...         │  │
│     └─────────────────────────┘  │
│                                  │
├──────────────────────────────────┤
│  ┌──────────────────────┐  [➤]  │  ← prompt bar
│  │ Send a message...    │       │
│  └──────────────────────┘       │
└──────────────────────────────────┘
```

### Session Detail Screen — Voice mode ON

```
┌──────────────────────────────────┐
│ ← Session Title    🎧  ■ ⏸ ▶   │  ← 🎧 toggle added (highlighted)
├──────────────────────────────────┤
│                                  │
│  ┌─────────────────────────┐     │
│  │ Assistant message...    │     │
│  │ Here's the fix for the  │     │
│  │ auth bug in handler.go  │     │
│  │                     🔊  │     │  ← speaker button (always visible)
│  └─────────────────────────┘     │
│                                  │
│     ┌─────────────────────────┐  │
│     │ User message...         │  │
│     └─────────────────────────┘  │
│                                  │
│  ┌────────────────────────────┐  │
│  │  ⚡ Reading auth/handler   │  │  ← tool_use card
│  └────────────────────────────┘  │
│                                  │
├──────────────────────────────────┤
│       ✨ contemplating... ✨      │  ← verb animation (when active)
│  ┌──────────────────────┐ 🎤 [➤]│  ← mic button appears
│  │ Send a message...    │       │
│  └──────────────────────┘       │
└──────────────────────────────────┘
```

### Session Detail Screen — Recording active

```
┌──────────────────────────────────┐
│ ← Session Title    🎧  ■ ⏸ ▶   │
├──────────────────────────────────┤
│                                  │
│          (messages...)           │
│                                  │
├──────────────────────────────────┤
│  ┌──────────────────────┐ 🔴 [➤]│  ← mic red (recording)
│  │ fix the auth bug in  │       │  ← text fills as user speaks
│  └──────────────────────┘       │
└──────────────────────────────────┘
```

### Assistant Message Card — with speaker button

```
┌─────────────────────────────────┐
│ Here's the fix for the auth bug │
│ in handler.go. The issue was    │
│ that the token validation was   │
│ missing the expiry check.       │
│                                 │
│ I've updated the `validateToken`│
│ function to check `exp` claim.  │
│                             🔊  │  ← tap: read aloud / tap again: stop
└─────────────────────────────────┘
```

### Settings Screen — Voice section

```
┌──────────────────────────────────┐
│ Settings                         │
├──────────────────────────────────┤
│                                  │
│ NOTIFICATIONS                    │
│ ┌──────────────────────────────┐ │
│ │ Sound               [━━●━━] │ │
│ │ Vibration            [━━●━━] │ │
│ └──────────────────────────────┘ │
│                                  │
│ VOICE                            │
│ ┌──────────────────────────────┐ │
│ │ Voice input          [━━●━━] │ │  ← show/hide mic button
│ │ Auto-read responses  [━━●━━] │ │  ← auto-speak when voice mode on
│ │                              │ │
│ │ Speech rate                  │ │
│ │ ○─────────●──────────○       │ │  ← 0.1 to 1.0 slider
│ │ Slow              Fast       │ │
│ │                              │ │
│ │ Persona            Sarcastic>│ │  ← tap to open picker
│ └──────────────────────────────┘ │
│                                  │
│ APPEARANCE                       │
│ ┌──────────────────────────────┐ │
│ │ Theme     [Light|Dark|Auto]  │ │
│ └──────────────────────────────┘ │
└──────────────────────────────────┘
```

### Persona Picker Dialog

```
┌──────────────────────────────────┐
│         Choose Persona           │
├──────────────────────────────────┤
│                                  │
│  ○ Default                       │
│    Neutral, professional         │
│                                  │
│  ○ Butler                        │
│    Formal, polished              │
│                                  │
│  ○ Casual                        │
│    Friendly, relaxed             │
│                                  │
│  ○ GenZ                          │
│    Slang, fun                    │
│                                  │
│  ● Sarcastic                     │  ← selected (filled radio)
│    Dry humor, deadpan            │
│                                  │
├──────────────────────────────────┤
│              [ OK ]              │
└──────────────────────────────────┘
```

### Inline Notification with Voice — Permission request

```
┌──────────────────────────────────┐
│  ⚠️ Permission Request           │
│                                  │
│  Claude wants to:                │
│  Edit file auth/handler.go       │
│                                  │
│  [  Deny  ]    [ Approve ]       │
│                                  │
│  🔊 "Excuse me your majesty,    │  ← TTS auto-reads this
│      may I edit auth handler     │
│      dot go, no pressure"        │
└──────────────────────────────────┘
```

## New Files

### 1. `lib/models/tts_persona.dart` — Sentence graph model + built-in personas

#### Core types

```dart
class Segment {
  final String slot;           // "greeting", "filler", "verb", "target", "suffix"
  final List<String> options;  // possible values (empty for "target" — filled at runtime)
  final double weight;         // probability of inclusion (0.0 = never, 1.0 = always)

  const Segment({required this.slot, required this.options, this.weight = 1.0});
}

class SentenceGraph {
  final List<Segment> segments;

  const SentenceGraph(this.segments);

  /// Build a randomized sentence by picking one option per included segment.
  String build(String target, Random random) {
    final parts = <String>[];
    for (final seg in segments) {
      if (seg.slot == 'target') {
        parts.add(target);
        continue;
      }
      if (seg.options.isEmpty) continue;
      if (random.nextDouble() > seg.weight) continue; // skip segment
      parts.add(seg.options[random.nextInt(seg.options.length)]);
    }
    return parts.join(' ').trim();
  }
}

class TTSPersona {
  final String id;
  final String name;
  final String description;
  final Map<String, SentenceGraph> graphs;
  // Keys: "tool.Read", "tool.Edit", "tool.Write", "tool.Bash",
  //        "tool.Grep", "tool.Glob", "tool.Agent", "tool.default",
  //        "tool.failed",
  //        "permission", "done", "error", "question", "trust",
  //        "elicitation", "elicitation.url"

  const TTSPersona({
    required this.id,
    required this.name,
    required this.description,
    required this.graphs,
  });
}
```

#### Built-in personas

##### Default — Neutral, professional
```dart
"tool.Read": SentenceGraph([
  Segment(slot: "verb", options: ["Reading", "Opening", "Checking"]),
  Segment(slot: "target", options: []),
])

"tool.Edit": SentenceGraph([
  Segment(slot: "verb", options: ["Editing", "Modifying", "Updating"]),
  Segment(slot: "target", options: []),
])

"tool.Write": SentenceGraph([
  Segment(slot: "verb", options: ["Writing", "Creating"]),
  Segment(slot: "target", options: []),
])

"tool.Bash": SentenceGraph([
  Segment(slot: "verb", options: ["Running", "Executing"]),
  Segment(slot: "target", options: []),
])

"tool.Grep": SentenceGraph([
  Segment(slot: "verb", options: ["Searching for", "Looking for"]),
  Segment(slot: "target", options: []),
])

"tool.Glob": SentenceGraph([
  Segment(slot: "verb", options: ["Finding files matching", "Locating"]),
  Segment(slot: "target", options: []),
])

"tool.Agent": SentenceGraph([
  Segment(slot: "verb", options: ["Delegating task", "Dispatching"]),
  Segment(slot: "target", options: []),
])

"tool.failed": SentenceGraph([
  Segment(slot: "verb", options: ["Failed", "Error running"]),
  Segment(slot: "target", options: []),
])

"permission": SentenceGraph([
  Segment(slot: "prefix", options: ["Permission needed.", "Approval required."]),
  Segment(slot: "filler", options: ["Claude wants to"]),
  Segment(slot: "target", options: []),
])

"done": SentenceGraph([
  Segment(slot: "phrase", options: ["Session completed.", "Done."]),
  Segment(slot: "target", options: [], weight: 0.8),
])

"error": SentenceGraph([
  Segment(slot: "phrase", options: ["Session error.", "Error occurred."]),
  Segment(slot: "target", options: []),
])

"question": SentenceGraph([
  Segment(slot: "phrase", options: ["Claude is asking."]),
  Segment(slot: "target", options: []),
])

"trust": SentenceGraph([
  Segment(slot: "phrase", options: ["Workspace trust required."]),
  Segment(slot: "target", options: []),
])
```

##### Butler — Formal, polished
```dart
"tool.Read": SentenceGraph([
  Segment(slot: "greeting", weight: 0.8, options: ["Sir,", "If I may,", "Right then,"]),
  Segment(slot: "filler", weight: 0.3, options: ["I shall be", "allow me to be"]),
  Segment(slot: "verb", options: ["reviewing", "examining", "perusing", "inspecting"]),
  Segment(slot: "target", options: []),
  Segment(slot: "suffix", weight: 0.2, options: ["for you", "at once", "presently"]),
])

"tool.Edit": SentenceGraph([
  Segment(slot: "greeting", weight: 0.8, options: ["Sir,", "Very well,", "Right away,"]),
  Segment(slot: "filler", weight: 0.3, options: ["I am now", "proceeding to be"]),
  Segment(slot: "verb", options: ["making changes to", "modifying", "adjusting", "amending"]),
  Segment(slot: "target", options: []),
])

"tool.Bash": SentenceGraph([
  Segment(slot: "greeting", weight: 0.8, options: ["Sir,", "Very well,", "At once,"]),
  Segment(slot: "verb", options: ["executing", "running", "carrying out"]),
  Segment(slot: "target", options: []),
])

"permission": SentenceGraph([
  Segment(slot: "greeting", options: ["Sir,", "If I may, sir,"]),
  Segment(slot: "phrase", options: ["your approval is needed.", "I require your permission.", "might I request authorization."]),
  Segment(slot: "filler", options: ["I'd like to", "I wish to"]),
  Segment(slot: "target", options: []),
])

"done": SentenceGraph([
  Segment(slot: "phrase", options: ["All done, sir.", "The task is complete, sir.", "Mission accomplished, sir."]),
  Segment(slot: "target", options: [], weight: 0.8),
])

"error": SentenceGraph([
  Segment(slot: "phrase", options: [
    "My apologies sir, something went wrong.",
    "I regret to inform you, sir, an error occurred.",
    "Most unfortunate, sir. An error.",
  ]),
  Segment(slot: "target", options: []),
])

"question": SentenceGraph([
  Segment(slot: "greeting", weight: 0.7, options: ["Sir,", "If I may, sir,"]),
  Segment(slot: "phrase", options: ["Claude inquires.", "A question, if you please.", "Claude wishes to ask."]),
  Segment(slot: "target", options: []),
])
```

##### Casual — Friendly, relaxed
```dart
"tool.Read": SentenceGraph([
  Segment(slot: "greeting", weight: 0.6, options: ["Hey,", "Alright,", "Cool,"]),
  Segment(slot: "verb", options: ["checking out", "looking at", "pulling up", "opening up"]),
  Segment(slot: "target", options: []),
])

"tool.Edit": SentenceGraph([
  Segment(slot: "greeting", weight: 0.6, options: ["Hey,", "Alright,", "Cool,"]),
  Segment(slot: "verb", options: ["tweaking", "updating", "fixing up", "changing"]),
  Segment(slot: "target", options: []),
])

"tool.Bash": SentenceGraph([
  Segment(slot: "greeting", weight: 0.5, options: ["Alright,", "Ok,", "Cool,"]),
  Segment(slot: "verb", options: ["running", "firing off", "kicking off"]),
  Segment(slot: "target", options: []),
])

"permission": SentenceGraph([
  Segment(slot: "greeting", options: ["Hey,", "Quick one,"]),
  Segment(slot: "phrase", options: ["need a thumbs up to", "can I get a go-ahead to", "mind if I"]),
  Segment(slot: "target", options: []),
])

"done": SentenceGraph([
  Segment(slot: "phrase", options: ["All done!", "That's a wrap!", "Finished up!", "Done and dusted!"]),
  Segment(slot: "target", options: [], weight: 0.7),
])

"error": SentenceGraph([
  Segment(slot: "phrase", options: ["Oops, hit a snag.", "Ah, something broke.", "Hmm, ran into an issue."]),
  Segment(slot: "target", options: []),
])
```

##### GenZ — Slang, fun
```dart
"tool.Read": SentenceGraph([
  Segment(slot: "greeting", weight: 0.7, options: ["Yo,", "Ayo,", "Fam,", "No cap,"]),
  Segment(slot: "filler", weight: 0.3, options: ["just", "boutta be", "finna be"]),
  Segment(slot: "verb", options: ["peeking at", "vibing with", "scoping out", "pulling up"]),
  Segment(slot: "target", options: []),
  Segment(slot: "suffix", weight: 0.3, options: ["real quick", "rn", "no cap"]),
])

"tool.Edit": SentenceGraph([
  Segment(slot: "greeting", weight: 0.7, options: ["Yo,", "Bet,", "Fam,", "Ayo,"]),
  Segment(slot: "filler", weight: 0.3, options: ["boutta be", "finna be", "straight up"]),
  Segment(slot: "verb", options: ["fixing up", "glow-up for", "cooking", "hitting different on"]),
  Segment(slot: "target", options: []),
  Segment(slot: "suffix", weight: 0.3, options: ["fr fr", "no cap", "sheesh"]),
])

"tool.Bash": SentenceGraph([
  Segment(slot: "greeting", weight: 0.7, options: ["Bet,", "Yo,", "Ayo,", "Aight,"]),
  Segment(slot: "verb", options: ["hitting", "running", "firing up", "sending"]),
  Segment(slot: "target", options: []),
  Segment(slot: "suffix", weight: 0.3, options: ["let's gooo", "sheesh", "fr"]),
])

"permission": SentenceGraph([
  Segment(slot: "greeting", options: ["Bruh,", "Yo,", "Fam,"]),
  Segment(slot: "phrase", options: ["need your go to", "can I get a vibe check to", "lemme", "need the green light to"]),
  Segment(slot: "target", options: []),
  Segment(slot: "suffix", weight: 0.4, options: ["real quick", "fr fr", "please bestie"]),
])

"done": SentenceGraph([
  Segment(slot: "phrase", options: [
    "We're bussin! All done.", "Slay! Done.", "That's a W.",
    "Let's gooo, finished.", "Nailed it, fr.",
  ]),
  Segment(slot: "target", options: [], weight: 0.5),
])

"error": SentenceGraph([
  Segment(slot: "phrase", options: [
    "Bruh, it's cooked.", "That ain't it chief.", "Big L.",
    "Down bad, got an error.", "It's giving error.",
  ]),
  Segment(slot: "target", options: [], weight: 0.7),
])

"question": SentenceGraph([
  Segment(slot: "greeting", options: ["Yo,", "Fam,", "Bestie,"]),
  Segment(slot: "phrase", options: ["Claude wants to know.", "got a question real quick.", "what's the move on this."]),
  Segment(slot: "target", options: []),
])
```

##### Sarcastic — Dry humor, deadpan
```dart
"tool.Read": SentenceGraph([
  Segment(slot: "greeting", weight: 0.7, options: ["Oh,", "Great,", "Wonderful,", "How delightful,"]),
  Segment(slot: "filler", weight: 0.4, options: [
    "apparently we're", "guess what,", "brace yourself,",
    "believe it or not,", "hold your breath,"
  ]),
  Segment(slot: "verb", options: ["reading", "looking at", "opening", "checking", "examining"]),
  Segment(slot: "target", options: []),
  Segment(slot: "suffix", weight: 0.5, options: [
    "how exciting", "riveting stuff", "what a thrill",
    "groundbreaking", "can barely contain myself",
  ]),
])

"tool.Edit": SentenceGraph([
  Segment(slot: "greeting", weight: 0.7, options: ["Oh,", "Great,", "Wonderful,", "Fantastic,"]),
  Segment(slot: "filler", weight: 0.4, options: [
    "apparently we're", "surprise surprise,", "brace yourself,",
    "who would have thought,",
  ]),
  Segment(slot: "verb", options: ["editing", "changing", "modifying", "touching"]),
  Segment(slot: "target", options: []),
  Segment(slot: "suffix", weight: 0.5, options: [
    "yet again", "what could go wrong", "this will end well",
    "I'm sure this is a great idea",
  ]),
])

"tool.Write": SentenceGraph([
  Segment(slot: "greeting", weight: 0.6, options: ["Oh,", "Wow,", "Amazing,"]),
  Segment(slot: "filler", weight: 0.3, options: ["look at us,", "we're actually"]),
  Segment(slot: "verb", options: ["writing", "creating", "conjuring up"]),
  Segment(slot: "target", options: []),
  Segment(slot: "suffix", weight: 0.4, options: ["groundbreaking", "truly historic", "the world needed this"]),
])

"tool.Bash": SentenceGraph([
  Segment(slot: "greeting", weight: 0.6, options: ["Sure,", "Oh,", "Right,"]),
  Segment(slot: "filler", weight: 0.4, options: [
    "let me just", "apparently I need to", "because why not,",
    "here we go again,",
  ]),
  Segment(slot: "verb", options: ["run", "execute", "fire off"]),
  Segment(slot: "target", options: []),
  Segment(slot: "suffix", weight: 0.5, options: [
    "no big deal", "what's the worst that could happen",
    "I live for this", "absolutely thrilling",
  ]),
])

"tool.Grep": SentenceGraph([
  Segment(slot: "greeting", weight: 0.6, options: ["Oh great,", "Wonderful,", "Marvelous,"]),
  Segment(slot: "verb", options: ["searching for", "hunting down", "looking for"]),
  Segment(slot: "target", options: []),
  Segment(slot: "suffix", weight: 0.5, options: [
    "like a needle in a haystack", "this should be fun",
    "wish me luck",
  ]),
])

"tool.Agent": SentenceGraph([
  Segment(slot: "greeting", weight: 0.5, options: ["Oh,", "Great,"]),
  Segment(slot: "filler", weight: 0.4, options: [
    "delegating because apparently I can't do everything,",
    "sending someone else to do it,",
    "outsourcing,"
  ]),
  Segment(slot: "target", options: []),
])

"tool.failed": SentenceGraph([
  Segment(slot: "phrase", options: [
    "Who could have seen this coming.", "Totally unexpected, an error.",
    "Oh no. Anyway.", "Shocker, something broke.",
    "I am absolutely stunned. An error.", "Well that went well.",
  ]),
  Segment(slot: "target", options: [], weight: 0.6),
])

"permission": SentenceGraph([
  Segment(slot: "greeting", options: [
    "Excuse me your majesty,", "Oh mighty one,",
    "If it pleases the court,", "Your highness,",
  ]),
  Segment(slot: "phrase", options: ["may I", "would you grace me with permission to", "dare I"]),
  Segment(slot: "target", options: []),
  Segment(slot: "suffix", weight: 0.4, options: ["no pressure", "whenever you're ready", "take your time"]),
])

"done": SentenceGraph([
  Segment(slot: "phrase", options: [
    "Shocking, it actually worked.",
    "Against all odds, it's done.",
    "Well well well, it finished.",
    "I'm as surprised as you are, it worked.",
    "Alert the press, task complete.",
  ]),
  Segment(slot: "target", options: [], weight: 0.5),
])

"error": SentenceGraph([
  Segment(slot: "phrase", options: [
    "Who could have seen this coming.",
    "Color me shocked. An error.",
    "In a completely foreseeable turn of events,",
    "Plot twist, something broke.",
    "And the award for least surprising error goes to,",
  ]),
  Segment(slot: "target", options: []),
])

"question": SentenceGraph([
  Segment(slot: "phrase", options: [
    "Claude has a question, try not to faint.",
    "Brace yourself, Claude is asking something.",
    "Sit down for this one. A question.",
    "Oh, Claude needs input. How novel.",
  ]),
  Segment(slot: "target", options: []),
])

"trust": SentenceGraph([
  Segment(slot: "phrase", options: [
    "Oh, this workspace needs trust. Imagine that.",
    "Apparently we need trust here. Shocking.",
    "Trust required. I know, groundbreaking concept.",
  ]),
  Segment(slot: "target", options: []),
])
```

### 2. `lib/utils/markdown_stripper.dart` — Pure function

`String stripMarkdown(String input)`:
- Remove fenced code blocks entirely (code sounds terrible spoken)
- Remove inline backticks but keep text
- Remove `[text](url)` → keep "text"
- Remove heading markers, bold/italic markers, bullet markers
- Collapse multiple newlines

### 3. `lib/services/tts_transformer.dart` — Message/notification → spoken text

```dart
class TTSTransformer {
  static final _random = Random();

  /// Transform a transcript message into spoken text. Returns null to skip.
  static String? transformMessage(Message message, TTSPersona persona) {
    switch (message.role) {
      case 'assistant':
        if (message.content == null || message.content!.isEmpty) return null;
        return stripMarkdown(message.content!);

      case 'tool_use':
        final key = 'tool.${message.tool ?? "default"}';
        final graph = persona.graphs[key] ?? persona.graphs['tool.default'];
        if (graph == null) return null;
        return graph.build(message.summary ?? '', _random);

      case 'tool_result':
        if (message.success == true) return null; // skip successes
        final graph = persona.graphs['tool.failed'];
        if (graph == null) return null;
        return graph.build(message.tool ?? 'command', _random);

      case 'user':
        return null;

      default:
        return null;
    }
  }

  /// Transform a notification into spoken text. Returns null to skip.
  static String? transformNotification(HeliosNotification n, TTSPersona persona) {
    if (!n.isPending) return null;

    String graphKey;
    String target;

    switch (n.type) {
      case 'claude.permission':
        graphKey = 'permission';
        final verb = _permissionVerb(n.toolName);
        target = '$verb ${n.detail ?? ""}';
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

    final graph = persona.graphs[graphKey];
    if (graph == null) return null;
    return graph.build(target, _random);
  }

  static String _permissionVerb(String? tool) {
    switch (tool) {
      case 'Edit':  return 'edit';
      case 'Write': return 'write';
      case 'Bash':  return 'run';
      case 'Read':  return 'read';
      default:      return 'use ${tool ?? "a tool on"}';
    }
  }
}
```

### 4. `lib/services/voice_service.dart` — Singleton service

Follows `NotificationService` pattern: private constructor, static `instance`, `init()` called from `main()`, `SharedPreferences` for persistence.

Owns:
- `SpeechToText _stt` — STT engine
- `FlutterTts _tts` — TTS engine
- Persisted settings: `voiceInputEnabled`, `autoReadEnabled`, `speechRate` (0.1–1.0), `language`, `personaId`
- Runtime state: `isListening`, `isSpeaking`, `sttAvailable`
- `onStateChanged` callback for UI updates

Key methods:
- `init()` — load prefs, init TTS defaults
- `startListening({onResult, onDone, onError})` → `Future<bool>` — requests mic permission, starts STT, returns false if denied
- `stopListening()` — stops STT
- `speak(String text)` — speaks via TTS (text already transformed by caller)
- `stopSpeaking()` — stops TTS
- `TTSPersona get persona` — returns current persona by `personaId`
- Settings setters that persist to SharedPreferences

SharedPreferences keys:
- `voice_input_enabled`
- `voice_auto_read_enabled`
- `voice_speech_rate`
- `voice_language`
- `voice_persona_id`

## Modified Files

### 5. `pubspec.yaml` — Add dependencies

```yaml
speech_to_text: ^7.0.0
flutter_tts: ^4.2.0
```

### 6. `android/app/src/main/AndroidManifest.xml` — Permission

Add `RECORD_AUDIO` permission and speech recognition query intent.

### 7. `ios/Runner/Info.plist` — Permission descriptions

Add `NSMicrophoneUsageDescription` and `NSSpeechRecognitionUsageDescription`.

### 8. `lib/main.dart` — Init voice service

After `await NotificationService.instance.init();` add:
```dart
await VoiceService.instance.init();
```

### 9. `lib/screens/session_detail_screen.dart` — Core UI changes

**New state fields:**
- `bool _voiceModeActive = false` — per-session toggle
- `bool _isRecording = false` — currently recording
- `int _lastReadTotal = 0` — track last known transcript total for auto-read

**9a. Voice mode toggle in `_buildActions()`:**
Add headset toggle IconButton at the start of the actions list. When toggled off, stop any active TTS/STT.

**9b. Mic button in `_buildPromptBar()`:**
Add mic IconButton between TextField and send button, visible only when `_voiceModeActive && VoiceService.instance.voiceInputEnabled`. Tap toggles recording — recognized text fills `_promptController` for review before send.

**9c. `_toggleRecording()` method:**
- If recording: stop STT
- If not: stop TTS first (prevent feedback), call `startListening()`, update `_promptController.text` on results
- Show snackbar on permission denial or error

**9d. Auto-read in `_loadTranscript()`:**
After setting `_messages`, if `_voiceModeActive && autoReadEnabled`:
- Compare `result.total` against `_lastReadTotal`
- New messages are those beyond `_lastReadTotal`
- For each new message, call `TTSTransformer.transformMessage(msg, persona)` — if non-null, speak it
- Update `_lastReadTotal = result.total`

**9e. Auto-read notifications:**
In the SSE listener, when a `notification` event arrives for this session and voice mode is active, call `TTSTransformer.transformNotification(n, persona)` and speak if non-null.

**9f. Cleanup in `dispose()`:**
Call `stopSpeaking()` and `stopListening()`.

### 10. `lib/widgets/message_card.dart` — Speaker button

Convert `_AssistantMessageCard` from StatelessWidget to StatefulWidget.

Add a small speaker IconButton (16px) at the bottom-right of the assistant message bubble. Tap calls `TTSTransformer.transformMessage()` then `VoiceService.instance.speak()`. Always visible so any message can be read on demand.

### 11. `lib/screens/settings_screen.dart` — Voice settings section

Add after Notifications section:
- `_SectionHeader('Voice')`
- `SwitchListTile` — Voice input enabled (show/hide mic button)
- `SwitchListTile` — Auto-read responses (speak new messages in voice mode)
- `ListTile` with `Slider` — Speech rate (0.1–1.0)
- `ListTile` with persona picker — dialog showing all personas with name + description

## Implementation Order

1. `pubspec.yaml` + `flutter pub get`
2. Platform permissions (AndroidManifest.xml, Info.plist)
3. `tts_persona.dart` (pure data model + 5 built-in personas)
4. `markdown_stripper.dart` (pure utility)
5. `tts_transformer.dart` (depends on #3, #4)
6. `voice_service.dart` (depends on #3)
7. `main.dart` (one-line init)
8. `session_detail_screen.dart` (voice toggle, mic button, auto-read)
9. `message_card.dart` (speaker button)
10. `settings_screen.dart` (voice settings + persona picker)

## Key Design Decisions

- **Sentence graph** — each TTS output is built from randomized segments with weighted inclusion, producing thousands of unique combinations per persona from a handful of words
- **5 built-in personas** — Default, Butler, Casual, GenZ, Sarcastic
- **Adding a persona = data only** — one `const TTSPersona(...)` declaration with graph definitions, zero code changes
- **Tap-to-toggle** for mic (not hold-to-record) — prompts can be long
- **Text fills TextField** — user reviews/edits before sending, no auto-send
- **Stop TTS before starting STT** — prevents audio feedback loop
- **Assistant content read as-is** (markdown stripped) — personas only style status messages, not Claude's words
- **Track by total count** — use `TranscriptResult.total` to detect new messages
- **No new Provider** — VoiceService is a singleton like NotificationService
- **Voice mode is per-session** — toggle in the app bar, not a global setting

## Verification

1. Build and install: `cd mobile && flutter build apk --debug && adb install ...`
2. Open a session → tap headset icon → mic button appears in prompt bar
3. Tap mic → speak → text appears in input field → tap send
4. Wait for assistant response → auto-reads aloud with current persona
5. Tap speaker icon on any assistant message → reads that message
6. Change persona to Sarcastic → hear varied outputs like "Oh, brace yourself, reading auth handler dot go, riveting stuff" and "Great, apparently we're editing main dot go, this will end well"
7. Same action repeated → different sentence each time due to randomization
8. Settings → Voice → toggle auto-read off → new messages no longer auto-read
9. Settings → Voice → adjust speech rate → TTS speed changes
10. Deny mic permission → snackbar shown, mic button stays but doesn't work
11. Notification arrives → hear persona-styled "Excuse me your majesty, may I edit auth handler dot go, no pressure"
