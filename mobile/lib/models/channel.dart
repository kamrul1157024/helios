/// A channel: several sessions and the person, with one conversation running
/// through it. See docs/specs/60-group-chat.md and internal/store/channels.go.
///
/// Everything here is host-local. A channel belongs to the daemon that holds
/// it, because a conversation spanning two daemons has no owner and its
/// messages would have to cross a tunnel to be read.
class Channel {
  final String id;

  /// Empty for a channel that is only its members, which is shown by them
  /// instead — `ch_8f21a0` on a row says nothing about the conversation.
  final String name;

  /// Session ids, in the order they joined.
  final List<String> members;

  /// Session id → the title it is shown by, resolved by the daemon.
  final Map<String, String> titles;

  /// Session id → the handle it answers to when addressed with `@`.
  ///
  /// A slug off the title where that names exactly one member, and the head of
  /// the id where two titles collide. Derived by the daemon so there is one
  /// source of truth for what an `@` means.
  final Map<String, String> slugs;

  /// What this reader has not seen.
  final int unread;

  /// Of the unread, how many named this reader. Kept apart because "somebody
  /// addressed me" and "there is traffic" are different questions.
  final int mentions;

  /// Closed: still readable, but it takes no more messages and delivers
  /// nothing.
  final bool archived;

  final String createdAt;

  const Channel({
    required this.id,
    this.name = '',
    this.members = const [],
    this.titles = const {},
    this.slugs = const {},
    this.unread = 0,
    this.mentions = 0,
    this.archived = false,
    this.createdAt = '',
  });

  /// The channel every session on the daemon is in, and the one that never
  /// pushes: sessions read it, it does not read them.
  static const generalId = 'general';

  bool get isGeneral => id == generalId;

  /// Whether the label is a name somebody chose, rather than a list of members
  /// standing in for one. Only a name is worth marking as a handle.
  bool get isNamed => name.isNotEmpty;

  /// What a person reads on a row. An unnamed channel is its members, so it is
  /// shown by them rather than by the id nobody chose.
  ///
  /// A name carries a `#`, the mark every chat tool uses for a channel. The
  /// member list does not: `#Alpha, Beta +2` reads as a name somebody chose.
  String get label {
    if (name.isNotEmpty) return '#$name';
    final named = members.map((id) => titles[id] ?? id).toList();
    if (named.isEmpty) return id;
    if (named.length <= 2) return named.join(', ');
    return '${named.take(2).join(', ')} +${named.length - 2}';
  }

  /// The handle a member answers to, falling back to the head of its id for a
  /// daemon too old to send the map.
  String handleFor(String sessionId) {
    final slug = slugs[sessionId];
    if (slug != null && slug.isNotEmpty) return slug;
    return sessionId.length > 8 ? sessionId.substring(0, 8) : sessionId;
  }

  factory Channel.fromJson(Map<String, dynamic> json) {
    return Channel(
      id: json['id'] as String? ?? '',
      name: json['name'] as String? ?? '',
      members:
          (json['members'] as List?)?.map((m) => m as String).toList() ??
          const [],
      titles: _stringMap(json['titles']),
      slugs: _stringMap(json['slugs']),
      unread: (json['unread'] as num?)?.toInt() ?? 0,
      mentions: (json['mentions'] as num?)?.toInt() ?? 0,
      archived: json['archived'] as bool? ?? false,
      createdAt: json['created_at'] as String? ?? '',
    );
  }
}

/// One message in a channel.
class ChannelMessage {
  final String id;

  /// 'user', or `session:<id>`.
  final String author;

  /// What to show it as: 'user', or the session's title.
  final String from;

  final String body;
  final bool urgent;
  final String createdAt;

  /// Empty on the channel's spine, else the message this one hangs off.
  /// Always a spine message: threads are one layer deep.
  final String threadRoot;

  /// The readers this named, in author form, resolved by the daemon when it
  /// was posted. Nothing here re-derives them.
  final List<String> mentions;

  /// How many replies hang off this one, and who wrote them. Spine only.
  final int replyCount;
  final List<String> replyAuthors;

  const ChannelMessage({
    required this.id,
    required this.author,
    required this.from,
    required this.body,
    this.urgent = false,
    this.createdAt = '',
    this.threadRoot = '',
    this.mentions = const [],
    this.replyCount = 0,
    this.replyAuthors = const [],
  });

  static const userAuthor = 'user';

  bool get fromPerson => author == userAuthor;

  /// The session behind the author, or '' for the person.
  String get sessionId =>
      author.startsWith('session:') ? author.substring('session:'.length) : '';

  /// Whether this message named the person reading it.
  bool get addressesUser => mentions.contains(userAuthor);

  factory ChannelMessage.fromJson(Map<String, dynamic> json) {
    return ChannelMessage(
      id: json['id'] as String? ?? '',
      author: json['author'] as String? ?? '',
      from: json['from'] as String? ?? '',
      body: json['body'] as String? ?? '',
      urgent: json['urgent'] as bool? ?? false,
      createdAt: json['created_at'] as String? ?? '',
      threadRoot: json['thread_root'] as String? ?? '',
      mentions:
          (json['mentions'] as List?)?.map((m) => m as String).toList() ??
          const [],
      replyCount: (json['reply_count'] as num?)?.toInt() ?? 0,
      replyAuthors:
          (json['reply_authors'] as List?)?.map((a) => a as String).toList() ??
          const [],
    );
  }
}

Map<String, String> _stringMap(dynamic raw) {
  if (raw is! Map) return const {};
  return raw.map((key, value) => MapEntry(key as String, value as String));
}
