# Group Chat: Sessions in a Channel

## The claim

Work is already split across sessions — one on the migration, one on the client, one reading the
logs — and the only thing joining them is a person copying a paragraph from one terminal into
another. The agents cannot see each other. Ask three of them to agree on an interface and you
are the wire.

A channel is a set of sessions and the people watching them, with one conversation running through
it. A message posted to the channel reaches every member, and any member — agent or human — can
post. The agents get a common place to say "I changed the response shape, it is `{items, next}`
now", and the person gets one place to say it to all three.

**What a member receives is a snippet, not the message.** A hundred-line design note pasted into
a channel must not land in four agents' context windows uninvited. Each participant is told, in one
line, that there is something new and who wrote it; the agent decides whether it is relevant and
pulls the thread with `helios chat read` if it is. That is the whole difference between a channel
that helps and a channel that costs four times as many tokens as it saves.

**Scope: the daemon and the desktop.** New: `internal/store/channels.go`, REST routes, SSE events,
`helios chat` with its section in `internal/skill/SKILL.md`, and a third sidebar mode beside
sessions and schedules. The TUI and mobile read the same API when somebody wants them to; nothing
here is desktop-only by design.

## Where we are

| | Today |
|---|---|
| Sending a prompt | `handleSessionSend` (`internal/server/api.go:552`) → `Shared.SendPrompt`, which queues behind a busy turn and answers 409 for a terminated session |
| Grouping sessions | `group_key` on a session: a tree in the sidebar, no shared state, no messages |
| Telling a human something happened | the notifications table (`internal/store/store.go:45`), with types like `claude.question` |
| An agent driving Helios | `helios schedule …`, documented in `internal/skill/SKILL.md`, which the agent reads during setup |
| Selecting several sessions | the bulk bar in the desktop sidebar — pin, move, terminate, delete |

So the delivery path exists and is the right one: a channel message becomes a prompt, and the
queueing, the 409 on a dead session and the transcript entry all come for free.

## The model

```
channel   id, name, created_at, created_by
member    channel_id, host-local session_id, joined_at, muted
message   id, channel_id, author ('session:<id>' or 'user'), body, created_at
receipt   channel_id, session_id, last_read_message_id
```

**A channel belongs to a host, the way a session does.** Sessions on two different machines are not
in one channel: the messages would have to cross a tunnel to be read, and "which daemon owns the
conversation" is a question with no good answer. So the store is the daemon's, the ids are
host-local, and a client paired with three daemons shows three sets of channels grouped by host —
the same grouping, and the same host headers, the session list already uses. `#general` is one
per host too: it is the notice board for the machine, and a machine is the thing the notices are
about.

Cross-host channels are a later spec if anybody wants one.

A channel is not a session group. Groups are how the list is arranged; a channel is a conversation.
Overlapping them would mean every rename of a folder renamed a chat.

## Delivery: the snippet, then the pull

A message from the person fans out to every session in the channel as an ordinary prompt, through
the path a typed prompt already takes. Posting is how the person says one thing to six agents,
which is the reason the channel exists.

When a message is posted, every member except the author is sent a prompt through the existing
path — the same call a typed prompt makes, with the same consequences: queued behind a busy
turn, and waking a cold session:

```
[helios] New message in "api-redesign" from "Split the orders migration":
"the response shape changed, items and next now"
— 4 unread. `helios chat read api-redesign --since-last` for the thread.
```

The body is cut to 200 characters. The agent has the gist, the author, and the command that
would tell it more. If the work has nothing to do with it, that is the end of it: one line of
context, not a hundred.

### The first thing an agent is told

An agent added to a channel does not know channels exist. The first prompt it receives is the only
chance to say what has happened and what it may do about it, and it is sent once — on joining,
whether the channel was made around it or it was invited later:

```
[helios] You are now in the group chat "api-redesign".

Members: "Split the orders migration", "Client for the new API", user.

  helios chat read api-redesign --since-last     the conversation since you last looked
  helios chat post api-redesign "…"              say something to everyone in it

Post when you have changed something another member would trip over — an interface, a
schema, a file you both touch. Do not narrate your work; your own transcript already
has it. You will be told when there is a new message, with the first line of it; read
the thread only when it looks like yours.

user: the response shape is {items, next} now — both of you please align on it.
```

The last line is the message the channel was created with, in full, if there was one. Creating a
channel with a message is the common case — the channel exists *because* somebody has something to say
to three sessions at once — and making them run `chat read` to find out what it was would be a
round trip for nothing.

### Full for a person, a snippet for an agent

That splits the rule stated above, and the split is deliberate:

| Posted by | Members receive |
|---|---|
| **user** | the message in full, as an ordinary prompt |
| **a session** | a snippet: author, first 200 characters, and the command to read the rest |

A person writing to a channel is addressing the agents and expects them to act on it — a snippet
there would be a prompt whose content is "there is a prompt, go and fetch it". An agent posting
is usually telling the others something in passing, and four copies of its paragraph in four
context windows is the cost the snippet exists to avoid.

A person's message is still cut at 4000 characters, with the tail behind `chat read`: a pasted
design document is not a prompt.

### Who a message is from

An author is named by its **session title**, cut to 48 characters on a word boundary — `sess-2`
means nothing to the agent reading it, and the title is the one line of the session that says
what it is for. A session with no title yet falls back to its project directory, and then to the
short id, which is what `sessionLabel` already does for every list in the app.

A person is **`user`**. Not a device name, not an email: there is one human in the loop as far as
a channel is concerned, and "user" is the word the agents already use for the other side of a
conversation.

Titles are not unique and can change. The id stays the identity in the store and on the wire —
`helios chat read` prints both, so an agent that wants to act on a member has something stable
to name.

Three rules keep a channel from becoming a machine for burning tokens:

1. **The author is never notified of their own message.** Obvious, and the first loop anybody
   writes by accident.
2. **A snippet is not a message.** An agent that replies to a snippet by posting to the channel
   produces one more snippet, not a thread quoted four times over.
3. **Rate and depth.** No more than one snippet per session per five seconds (the rest coalesce
   into "3 new messages"), and a chain of agent-to-agent posts deeper than eight without a human
   message in between stops and posts a warning to the channel. Two agents can otherwise talk to
   each other until the credits run out.

`muted` on a member is how a session stays in a channel it does not want interrupting it: it
receives nothing and reads the thread when it next looks.

## Making one, and growing one

A channel is made in three ways: a person selecting sessions, an agent naming sessions, or an agent
starting a session it needs and taking it in with it.

### The same sessions are the same conversation

An unnamed channel **is** its members. Ask for one with `[s2, s7]` when a channel of exactly `[s2, s7]`
exists and you get that channel — with the message you were creating it with posted into it, which
is what you meant. Ask with `[s2, s7, s9]` and that is a different set, so it is a different
channel, even though it differs by one.

The alternative is three channels holding the same two sessions, named "untitled", one of which has
the thing you are looking for.

A **named** channel is exempt. `helios chat new "api-redesign" --with s2,s7` is a deliberate act and
always makes a channel, because the name says this conversation is a different thing from the last
one those two had. Naming is how you say "not that one".

So:

| You ask for | You get |
|---|---|
| unnamed, members match an existing unnamed channel | that channel |
| unnamed, members differ by one | a new channel |
| named | a new channel, always |
| "add these to …" | the channel you picked, with the members added |

### Growing one instead

Adding to a conversation is a different intent from starting one, so it is a different action
rather than a clever inference. In the desktop's selection bar, **New group chat** sits beside
**Add to chat…**, which opens the list of channels on that host. On the CLI:

```
helios chat join api-redesign --session sess-9        # an existing session
helios chat leave api-redesign --session sess-9
```

A session added later gets the joining prompt like anyone else, ending with what it missed rather
than the missed messages themselves:

```
This conversation started before you joined — 14 messages.
  helios chat read api-redesign        all of it
```

Fourteen messages pasted into a new session's first prompt is the context-window bill this whole
design exists to avoid.

### An agent bringing a session with it

The case that makes channels worth building: an agent decides the work needs a second pair of hands.

```
helios chat invite api-redesign --new "port the client to {items, next}" \
    --cwd ~/work/client --provider claude
```

One call: start a session the way `helios new` does, add it to the channel, send it its joining
prompt, and post to the channel that it happened — *"Split the orders migration started 'Port the
client' and added it here"* — so the person watching knows a fourth agent exists and who asked
for it.

Without that last line an agent can quietly spawn work nobody sees. With it, the channel is the
audit trail: every session in it says who brought it in.

The same rationing as everything else that costs money: an agent may start at most two sessions
per channel, and none at all in `#general`.

## The global channel

Every daemon has one channel nobody creates and nobody is invited to: `#general`. Every session on
that daemon is in it from the moment it starts, and leaves it when it ends.

It is for the things that are nobody's conversation in particular. *I am about to force-push
main.* *The dev database is down, do not trust a failing integration test for the next hour.*
*The flake in the hooks test is mine, leave it.* Today those are said to one session, or to
nobody, and the other five find out by wasting an hour.

**It does not push.** This is the whole design of it, and the opposite of a channel's:

| | A channel | `#general` |
|---|---|---|
| Membership | invited | everyone, always |
| A post reaches you | as a snippet, immediately | not at all |
| You see it | when told | when you look |

A snippet per post per session is fine for a channel of three. In `#general` on a daemon running
thirty sessions it is thirty prompts, thirty interruptions and thirty context windows for one
sentence — the cost grows with the square of how useful the feature gets, which is the shape of
a thing that has to be redesigned a month later. So the channel is a notice board. Sessions read
it; it does not read them.

Two exceptions, and no more:

- **`--announce`** pushes a snippet to every session, rate-limited to one per minute per daemon
  and refused to a session that has used it twice in an hour. It is for "main is broken", and
  the limit is what stops it becoming the default way to post.
- **`@mention`** of a session pushes to that session alone, wherever it was written.

An agent is told the channel exists in its joining prompt, and is told when to read it: at the
start of a piece of work, and when it is about to do something wide — a force-push, a migration,
a dependency bump. That is a habit worth having and cheap to keep, because reading is one
command and produces nothing when there is nothing new.

### Urgent: the message that stops the channel

Some things cannot wait for the next `chat read`, and cannot wait for the end of a turn either.
*Stop, you are both editing the same file.* *The migration is wrong, do not run it.* *I have
force-pushed, your rebase is about to make a mess.* By the time a snippet is noticed, the thing
it was warning about has happened.

An urgent message interrupts:

```
helios chat post api-redesign --urgent "stop — the migration drops a column, do not run it"
```

and in the desktop, a switch beside the composer's send button rather than a different box.

What it does to each member, in order:

```
for each member that is not the author:
    +-- POST /api/sessions/:id/stop        the turn ends where it is
    |     (sh.stopSession → settleInterrupted, which is what the composer's ■ already does)
    +-- SendPrompt(the message, in full, marked urgent)
```

The stop is the point. A prompt to a busy session queues behind the turn, which for a
twenty-minute refactor is twenty minutes of doing the thing it was told not to do. Stopping first
costs the turn — whatever the agent was mid-way through is abandoned, and it comes back to a
prompt saying why.

Because it costs that, it is rationed harder than anything else here:

| | Who may | Limit |
|---|---|---|
| `user` | always | none — a person interrupting their own agents is the normal case |
| a session | only with `--urgent`, which it must justify in the message | one per session per ten minutes, and refused entirely in `#general` unless the daemon's operator has allowed it |

An interrupted session is not a failed one: the turn settles to idle the way a stopped turn
already does, and the transcript shows the interruption followed by the message that caused it.
The channel marks the message urgent, so reading it later says why everybody stopped.

### Pointing at a channel

A message may name another channel — `see #api-redesign` — and that is a pointer, not a copy. In
the desktop it is a link that opens the channel; in `chat read` it is printed as a name an agent can
pass straight back to `chat read`. A session that finds a conversation is happening in the wrong
place says so in one line instead of repeating it:

```
helios chat post general "the response shape is changing — #api-redesign has the detail"
```

This is what keeps `#general` short. The channel carries the fact that something is happening and
where; the channel carries the argument.

## Setup

`helios setup` gains two questions, because both answers are needed before the first channel is
useful and neither is worth a config file nobody finds.

**The skill.** The agent cannot use a CLI it has not read about. `skill.Install`
(`internal/skill/skill.go:48`) already writes `~/.claude/skills/helios/SKILL.md` — user-level, so
every Claude session on the machine has it, not only the ones started in a repo that happens to
carry a copy. Setup asks, rather than doing it silently, because writing into somebody's home
directory without asking is how a tool loses trust:

```
Install the Helios skill so agents can drive it themselves?
  Claude  ~/.claude/skills/helios/SKILL.md          [Y/n]
  Codex   ~/.codex/AGENTS.md  (appended, marked)    [y/N]
```

Codex has no skills directory: its global instructions are `~/.codex/AGENTS.md`. So the same
content goes in between markers — `<!-- helios:start -->` … `<!-- helios:end -->` — and a
reinstall replaces what is between them and leaves the rest of the file alone. A user who edits
inside the markers loses it on upgrade, which is why the markers say so.

**The house prompt.** A short text the operator writes once, prepended to every joining prompt on
that daemon:

```
Anything the agents in this daemon's channels should know? (optional)
> Post before force-pushing. Name the ticket. Ask in #general before touching infra/.
```

The etiquette in the joining prompt is general — post what others would trip over, do not
narrate. This is the part that is local: the conventions of this machine and the people using
it. Stored in the daemon's config, editable afterwards in desktop Settings, and empty by
default — an unanswered question costs nothing, and a paragraph of house rules is worth more
than a paragraph of ours.

## Flows

### Creating a channel with a message

```
user: picks three sessions, "New group chat", types a message
    |
    v
POST /api/channels { name, members: [s2, s7, s9], message }
    |
    v
Daemon:
    +-- insert channel, three member rows, one message (author 'user')
    +-- for each member:
    |     +-- compose the joining prompt (what a channel is, who is in it,
    |     |   the two commands, the etiquette)
    |     +-- append the message in full — a person's message is not a snippet
    |     +-- SendPrompt(session, text)      exactly as a typed prompt behaves
    |           +-- idle    → delivered now
    |           +-- busy    → queued behind the turn
    |           +-- cold    → woken, then delivered
    |           +-- ended   → 409, skipped, member marked inactive
    +-- SSE: channel_created, then channel_message
    |
    v
Desktop and mobile: the channel appears under its host, with the message in it
Each agent: reads the prompt, and knows the channel exists
```

### A session posting to a channel

```
sess-2: helios chat post api-redesign "the response shape is {items, next} now"
    |
    v
POST /api/channels/api-redesign/messages   (author 'session:sess-2')
    |
    v
Daemon:
    +-- insert message
    +-- members = [s2, s7, s9] minus the author  → [s7, s9]
    +-- minus muted                              → [s7]
    +-- rate gate: one snippet per session per 5s
    |     +-- inside the window → coalesce into "3 new messages"
    +-- depth gate: 8 agent posts with no 'user' message between
    |     +-- exceeded → stop, post a warning to the channel, notify nobody
    +-- for each remaining member: SendPrompt(snippet)
    |
    v
sess-7 receives one line:
    [helios] New message in "api-redesign" from "Split the orders migration":
    "the response shape is {items, next} now"
    — 1 unread. `helios chat read api-redesign --since-last` for the thread.
    |
    v
sess-7 decides it is its business
    |
    v
helios chat read api-redesign --since-last   → the thread, and the receipt moves
```

### The notice board, which pushes nothing

```
sess-4: helios chat post general "about to force-push main, hold your rebases"
    |
    v
Daemon: insert message. That is all — no member is notified.
    |
    v
sess-9, an hour later, before rebasing:
    helios chat read general --since-last
    → "sess-4: about to force-push main, hold your rebases"

                        ─── unless ───

sess-4: helios chat post general --announce "main is broken, do not pull"
    |
    v
Daemon:
    +-- rate gate: one announce per minute per host,
    |   and twice per hour per session
    +-- snippet to every live session on the host
```

## The CLI, which is how agents use any of this

```bash
helios chat list                        # channels and #general, with unread counts
helios chat sessions                    # who could be invited: id, title, cwd, status
helios chat new "api-redesign" --with sess-2,sess-7
helios chat post api-redesign "the response shape is {items, next} now"
helios chat read api-redesign --since-last   # and marks it read
helios chat join api-redesign --session sess-9
helios chat leave api-redesign --session sess-9
helios chat invite api-redesign --new "port the client" --cwd ~/work/client
helios chat mute api-redesign

helios chat read general --since-last         # the notice board, pulled not pushed
helios chat post general "about to force-push main, hold your rebases"
helios chat post general --announce "main is broken, do not pull"
```

`helios chat sessions` matters more than it looks: it is how an agent finds the others. A session
knows its own id from the environment; everything else it has to ask for.

`--since-last` reads from the receipt, so the common case — "something happened, show me what I
have missed" — is one command with no bookkeeping.

`SKILL.md` gains a section saying when to use it: post when you have changed something another
session would trip over, read when a snippet mentions your area, and do not narrate. An agent
that posts every thought turns the channel into its own transcript.

## Who may post

Anything that can reach the daemon. A session posts as itself and is shown by its title; the
desktop posts as `user`. A session that has been terminated stays in the channel as an author of
what it already said, and is not delivered to.

## The desktop

A third mode on the rail, under Sessions and Schedules:

```
┌────┬─────────────────────────┬────────────────────────────────┐
│ ▤  │ Channels            +      │  api-redesign                  │
│ ◷  │ ─────────────────────── │  Split the orders m… · Client  │
│ ⌸  │ api-redesign        3   │  for the new A… · user         │
│ ▣  │   Split the orders m…,  │ ───────────────────────────────│
│    │   Client for the new A… │  Split the orders m…    09:14  │
│    │ flaky-hooks-test        │  the response shape changed,   │
│    │   Chase the HITL flake  │  items and next now            │
│    │                         │                                │
│    │                         │  user                   09:15  │
│    │                         │  does the client cope with it? │
│    │                         │ ───────────────────────────────│
│    │                         │  [ message the channel…      ↑ ]  │
└────┴─────────────────────────┴────────────────────────────────┘
```

- **The rail** gains a channels icon. The sidebar lists channels with unread counts, under a host
  header each, exactly as the session list groups by host — and `#general` sits at the top of
  each host's group, because it is the one channel that is always there.
- **The panel** is the conversation.
- **A message names its author** by session title, truncated, as a chip — clicking it opens that
  session, which is the thing a reader wants two seconds after reading the message. The person's
  own messages are from `user`.
- **Making one** has two doors. In the channels list, `+` opens a picker of sessions. In the
  sessions list, the bulk bar that already does Pin and Move gains **New group chat** and **Add
  to chat…** — select three sessions and either start a conversation or drop them into one that
  exists. Starting one with a set that already has an unnamed channel opens that channel instead of
  making a second, and says so.
- **A channel's messages are not a transcript.** No tool calls, no diffs: a channel is people and
  agents talking, and what an agent *did* is in its own session, one click away.
- **Unread** is per channel, from the human's own receipt, and the rail carries the total the way
  approvals do.

Composer behaviour matches the transcript's: the draft is kept per channel, the box takes the
keyboard when the channel opens, and Enter posts.

## What this is not

- **Not a shared context window.** Agents do not see each other's transcripts. The channel is the
  only thing they share, and only what was deliberately posted to it.
- **Not automatic.** Nothing mirrors an agent's turn into the channel. If it is worth saying, the
  agent says it — the alternative is four transcripts pasted into one another.
- **Not a coordinator.** The channel does not stop two agents editing the same file. Worktrees are
  the answer to that, and they already exist.

## The mobile app

The same three pieces the desktop needs, in the shapes mobile already uses:

| | Where |
|---|---|
| A fourth tab, "Channels", between Schedules and Notifications | `lib/screens/home_screen.dart:565` — the `IndexedStack` and the `NavigationBar` beside it, with the per-tab FAB at `:575` creating a channel the way index 1 creates a schedule |
| The list and the conversation | `lib/screens/channels_screen.dart` and `channel_detail_screen.dart`, following `schedules_screen.dart` and `schedule_detail_screen.dart` |
| Channels over the wire | `lib/services/daemon_api_service.dart` — the REST calls beside `listSchedules` (`:498`), and `channel_message` in `_handleEvent` (`:329`), which already debounces staleness per event type |
| The types | `lib/models/channel.dart`, beside `schedule.dart` and `message.dart` |
| Unread on the tab | the `Badge` pattern the Sessions and Notifications destinations already use |

Creating a channel on mobile starts from the channels tab rather than from a multi-select in the
session list: the desktop has a selection bar to hang it off and mobile does not, and adding one
to carry a single action is a worse trade than a sheet with a list of sessions and tick boxes.

## Phases

1. **Channels and broadcast.** Store, REST, SSE, `helios chat` for list/new/post/read, the sidebar
   mode, and creation from the bulk bar. Snippet delivery with the author rule and the rate
   limit.
2. **Receipts and unread.** `--since-last`, per-channel unread counts, mute.
3. **Mentions.** `@` a member and it notifies that one alone. Cheap once the rest exists, and
   the thing that makes a channel of six usable.
4. **`#general` and pointers.** The implicit channel, pull-only, with `--announce` behind its rate
   limit and `#channel` references resolving in both clients.
5. **Urgent.** Stop-then-deliver, the rationing that goes with it, and the setup questions —
   the skill for Claude and Codex, and the house prompt.

## Open questions

- **Does a channel survive its sessions?** A session ends; the channel keeps the messages. Probably
  yes, and the member row keeps the id so the transcript is still reachable — but a channel of
  nothing but dead sessions should say so rather than looking live.
- **Snippet length.** 200 characters is a guess. It wants to be long enough to judge relevance
  and short enough that four of them cost nothing.
- ~~Should a message wake a cold session?~~ **Settled: yes.** Delivery is `SendPrompt` and
  nothing else — a cold member wakes exactly as it does for a typed prompt. A channel does not
  get its own rules for what a prompt is: one path, one behaviour, and somebody who did not want
  six agents awake did not want to write to six agents.
