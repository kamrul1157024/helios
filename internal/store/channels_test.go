package store

import (
	"testing"
)

func channelStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestCreateChannelHoldsItsMembers(t *testing.T) {
	s := channelStore(t)

	ch, reused, err := s.CreateChannel("", AuthorUser, []string{"s2", "s7"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if reused {
		t.Error("a first channel cannot be a reused one")
	}
	if len(ch.Members) != 2 {
		t.Errorf("members = %v, want two", ch.Members)
	}
}

// The rule the spec is built on: an unnamed channel is its members.
func TestTheSameMembersAreTheSameChannel(t *testing.T) {
	s := channelStore(t)

	first, _, err := s.CreateChannel("", AuthorUser, []string{"s2", "s7"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Asked for again, in the other order, with a duplicate thrown in.
	again, reused, err := s.CreateChannel("", AuthorUser, []string{"s7", "s2", "s7"})
	if err != nil {
		t.Fatalf("create again: %v", err)
	}
	if !reused {
		t.Error("the second ask should have been told it is an existing channel")
	}
	if again.ID != first.ID {
		t.Errorf("got %s, want the channel that already held those two (%s)", again.ID, first.ID)
	}
}

func TestOneMoreMemberIsAnotherChannel(t *testing.T) {
	s := channelStore(t)

	first, _, _ := s.CreateChannel("", AuthorUser, []string{"s2", "s7"})
	second, reused, err := s.CreateChannel("", AuthorUser, []string{"s2", "s7", "s9"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if reused || second.ID == first.ID {
		t.Error("a different set of members is a different channel")
	}
}

// Naming one is how somebody says "not that one".
func TestANamedChannelIsAlwaysNew(t *testing.T) {
	s := channelStore(t)

	first, _, _ := s.CreateChannel("api-redesign", AuthorUser, []string{"s2", "s7"})
	second, reused, err := s.CreateChannel("api-redesign", AuthorUser, []string{"s2", "s7"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if reused || second.ID == first.ID {
		t.Error("a named channel must not be deduplicated against another")
	}
}

func TestAnUnnamedChannelIsNotMatchedAgainstANamedOne(t *testing.T) {
	s := channelStore(t)

	named, _, _ := s.CreateChannel("api-redesign", AuthorUser, []string{"s2", "s7"})
	plain, reused, _ := s.CreateChannel("", AuthorUser, []string{"s2", "s7"})
	if reused || plain.ID == named.ID {
		t.Error("the unnamed ask should have made its own channel")
	}
}

func TestAChannelNeedsSomebodyInIt(t *testing.T) {
	s := channelStore(t)
	if _, _, err := s.CreateChannel("", AuthorUser, nil); err == nil {
		t.Error("want an error for a channel with no members")
	}
}

func TestMembersComeAndGo(t *testing.T) {
	s := channelStore(t)
	ch, _, _ := s.CreateChannel("", AuthorUser, []string{"s2"})

	added, err := s.AddMember(ch.ID, "s7")
	if err != nil || !added {
		t.Fatalf("add: added=%v err=%v", added, err)
	}
	// Two agents adding the same session is a race, not a mistake — and the
	// second one must not send it a second joining prompt.
	again, err := s.AddMember(ch.ID, "s7")
	if err != nil || again {
		t.Errorf("adding twice: added=%v err=%v, want false and no error", again, err)
	}

	if err := s.RemoveMember(ch.ID, "s7"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	members, _ := s.ChannelMembers(ch.ID)
	if len(members) != 1 || members[0] != "s2" {
		t.Errorf("members = %v, want just s2", members)
	}
}

func TestDeliveryLeavesOutTheAuthorAndTheMuted(t *testing.T) {
	s := channelStore(t)
	ch, _, _ := s.CreateChannel("", AuthorUser, []string{"s2", "s7", "s9"})
	if err := s.SetMuted(ch.ID, "s9", true); err != nil {
		t.Fatalf("mute: %v", err)
	}

	to, err := s.Unmuted(ch.ID, "s2")
	if err != nil {
		t.Fatalf("unmuted: %v", err)
	}
	if len(to) != 1 || to[0] != "s7" {
		t.Errorf("delivering to %v, want only s7", to)
	}

	// A message from the person leaves nobody out but the muted.
	all, _ := s.Unmuted(ch.ID, "")
	if len(all) != 2 {
		t.Errorf("delivering to %v, want both unmuted members", all)
	}
}

func TestMessagesComeBackInOrder(t *testing.T) {
	s := channelStore(t)
	ch, _, _ := s.CreateChannel("", AuthorUser, []string{"s2"})

	for _, body := range []string{"first", "second", "third"} {
		if _, err := s.PostMessage(ch.ID, AuthorUser, body, false, "", nil); err != nil {
			t.Fatalf("post %q: %v", body, err)
		}
	}

	messages, err := s.Messages(ch.ID, "", 0)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(messages) != 3 || messages[0].Body != "first" || messages[2].Body != "third" {
		t.Errorf("read %d messages, oldest %q", len(messages), messages[0].Body)
	}
}

func TestAnEmptyMessageIsNotAMessage(t *testing.T) {
	s := channelStore(t)
	ch, _, _ := s.CreateChannel("", AuthorUser, []string{"s2"})
	if _, err := s.PostMessage(ch.ID, AuthorUser, "   ", false, "", nil); err == nil {
		t.Error("want an error for a message with nothing in it")
	}
}

func TestUnreadCountsWhatSomebodyElseSaid(t *testing.T) {
	s := channelStore(t)
	ch, _, _ := s.CreateChannel("", AuthorUser, []string{"s2"})

	_, _ = s.PostMessage(ch.ID, AuthorUser, "one", false, "", nil)
	_, _ = s.PostMessage(ch.ID, SessionAuthor("s2"), "two", false, "", nil)

	// s2 wrote the second one, so it has one thing to read, not two.
	unread, err := s.Unread(ch.ID, SessionAuthor("s2"))
	if err != nil {
		t.Fatalf("unread: %v", err)
	}
	if unread != 1 {
		t.Errorf("unread = %d, want 1", unread)
	}

	if err := s.MarkRead(ch.ID, SessionAuthor("s2")); err != nil {
		t.Fatalf("mark read: %v", err)
	}
	if unread, _ := s.Unread(ch.ID, SessionAuthor("s2")); unread != 0 {
		t.Errorf("unread after reading = %d, want 0", unread)
	}
}

func TestReadingFromAReceiptSkipsWhatWasSeen(t *testing.T) {
	s := channelStore(t)
	ch, _, _ := s.CreateChannel("", AuthorUser, []string{"s2"})
	_, _ = s.PostMessage(ch.ID, AuthorUser, "before", false, "", nil)
	_ = s.MarkRead(ch.ID, SessionAuthor("s2"))
	_, _ = s.PostMessage(ch.ID, AuthorUser, "after", false, "", nil)

	at, err := s.LastRead(ch.ID, SessionAuthor("s2"))
	if err != nil {
		t.Fatalf("last read: %v", err)
	}
	messages, _ := s.Messages(ch.ID, at, 0)
	if len(messages) != 1 || messages[0].Body != "after" {
		t.Errorf("since the receipt: %v, want only the later one", messages)
	}
}

func TestGeneralIsMadeOnceAndPinnedToTheTop(t *testing.T) {
	s := channelStore(t)
	if _, _, err := s.CreateChannel("later", AuthorUser, []string{"s2"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	for range 2 {
		if err := s.EnsureGeneral(); err != nil {
			t.Fatalf("ensure general: %v", err)
		}
	}

	channels, err := s.Channels(false)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(channels) != 2 {
		t.Fatalf("got %d channels, want the named one and general", len(channels))
	}
	if channels[0].ID != GeneralChannel {
		t.Errorf("first channel is %s, want general at the top", channels[0].ID)
	}
}

func TestGeneralCannotBeDeleted(t *testing.T) {
	s := channelStore(t)
	_ = s.EnsureGeneral()
	if err := s.DeleteChannel(GeneralChannel); err == nil {
		t.Error("want an error: the notice board is not somebody's to delete")
	}
}

func TestDeletingAChannelTakesItsMessagesWithIt(t *testing.T) {
	s := channelStore(t)
	ch, _, _ := s.CreateChannel("", AuthorUser, []string{"s2"})
	_, _ = s.PostMessage(ch.ID, AuthorUser, "something", false, "", nil)

	if err := s.DeleteChannel(ch.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	messages, _ := s.Messages(ch.ID, "", 0)
	members, _ := s.ChannelMembers(ch.ID)
	if len(messages) != 0 || len(members) != 0 {
		t.Errorf("left behind %d messages and %d members", len(messages), len(members))
	}
}

// Archiving is what shortens the list. Destroying the conversation is what it
// exists to avoid, so the channel is still there to be asked for.
func TestArchivingTakesAChannelOutOfTheList(t *testing.T) {
	s := channelStore(t)
	ch, _, _ := s.CreateChannel("api-redesign", AuthorUser, []string{"s2"})

	if err := s.SetArchived(ch.ID, true); err != nil {
		t.Fatalf("archive: %v", err)
	}

	open, _ := s.Channels(false)
	for _, one := range open {
		if one.ID == ch.ID {
			t.Error("a closed channel is still in the open list")
		}
	}
	all, _ := s.Channels(true)
	found := false
	for _, one := range all {
		if one.ID == ch.ID {
			found = true
			if !one.Archived {
				t.Error("it is in the full list but does not say it is closed")
			}
		}
	}
	if !found {
		t.Error("archiving lost the channel; it is meant to keep it")
	}

	if err := s.SetArchived(ch.ID, false); err != nil {
		t.Fatalf("unarchive: %v", err)
	}
	if again, _ := s.Channels(false); len(again) != 1 {
		t.Errorf("reopened, the list has %d channels, want it back", len(again))
	}
}

// The promise archiving makes: the conversation is finished. A channel that
// still took messages would be a filter, not a close.
func TestAClosedChannelTakesNothing(t *testing.T) {
	s := channelStore(t)
	ch, _, _ := s.CreateChannel("api-redesign", AuthorUser, []string{"s2"})
	_ = s.SetArchived(ch.ID, true)

	if _, err := s.PostMessage(ch.ID, AuthorUser, "anyone there?", false, "", nil); err == nil {
		t.Error("want an error: a closed channel takes no messages")
	}
	if _, err := s.AddMember(ch.ID, "s7"); err == nil {
		t.Error("want an error: joining a finished conversation is the thing this prevents")
	}
}

// Reopening a finished conversation because somebody asked for the same two
// sessions again would undo the close behind their back.
func TestTheSameMembersDoNotReopenAClosedChannel(t *testing.T) {
	s := channelStore(t)
	first, _, _ := s.CreateChannel("", AuthorUser, []string{"s2", "s7"})
	_ = s.SetArchived(first.ID, true)

	again, reused, err := s.CreateChannel("", AuthorUser, []string{"s2", "s7"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if reused || again.ID == first.ID {
		t.Error("the closed channel was handed back; want a new one")
	}
}

func TestGeneralCannotBeClosed(t *testing.T) {
	s := channelStore(t)
	_ = s.EnsureGeneral()
	if err := s.SetArchived(GeneralChannel, true); err == nil {
		t.Error("want an error: the notice board is not somebody's to close")
	}
}

func TestArchivingWhatIsNotThereSaysSo(t *testing.T) {
	s := channelStore(t)
	if err := s.SetArchived("ch_nope", true); err == nil {
		t.Error("want an error naming the channel that does not exist")
	}
}

func TestRenamingAChannelKeepsWhatWasSaidInIt(t *testing.T) {
	s := channelStore(t)
	ch, _, _ := s.CreateChannel("api-redesing", AuthorUser, []string{"s2"})
	_, _ = s.PostMessage(ch.ID, AuthorUser, "the typo is in the name", false, "", nil)

	if err := s.RenameChannel(ch.ID, "  api-redesign  "); err != nil {
		t.Fatalf("rename: %v", err)
	}

	again, _, _ := s.Channel(ch.ID)
	if again.Name != "api-redesign" {
		t.Errorf("name = %q, want it trimmed and changed", again.Name)
	}
	messages, _ := s.Messages(ch.ID, "", 0)
	if len(messages) != 1 {
		t.Error("renaming lost the conversation; deleting is the thing it exists to avoid")
	}
}

// Naming an unnamed channel takes it out of the pool matched by member set.
// That is the existing rule — naming is how somebody says "not that one" —
// but it is a change of identity, not only of label.
func TestNamingAnUnnamedChannelTakesItOutOfTheMemberMatch(t *testing.T) {
	s := channelStore(t)
	first, _, _ := s.CreateChannel("", AuthorUser, []string{"s2", "s7"})
	if err := s.RenameChannel(first.ID, "api-redesign"); err != nil {
		t.Fatalf("rename: %v", err)
	}

	again, reused, err := s.CreateChannel("", AuthorUser, []string{"s2", "s7"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if reused || again.ID == first.ID {
		t.Error("the named channel was matched by its members; naming should have taken it out")
	}
}

// Clearing the name would put it back in the member-set pool, where it could
// collide with the channel already sitting there.
func TestAChannelCannotBeRenamedToNothing(t *testing.T) {
	s := channelStore(t)
	ch, _, _ := s.CreateChannel("api-redesign", AuthorUser, []string{"s2"})
	if err := s.RenameChannel(ch.ID, "   "); err == nil {
		t.Error("want an error for an empty name")
	}
}

func TestGeneralCannotBeRenamed(t *testing.T) {
	s := channelStore(t)
	_ = s.EnsureGeneral()
	if err := s.RenameChannel(GeneralChannel, "notices"); err == nil {
		t.Error("want an error: the notice board is not somebody's to rename")
	}
	if err := s.RenameChannel("ch_nope", "whatever"); err == nil {
		t.Error("want an error naming the channel that does not exist")
	}
}

func seedSession(t *testing.T, s *Store, id, status string) {
	t.Helper()
	if err := s.UpsertSession(&Session{
		SessionID: id, Source: "claude", CWD: "/tmp/proj", Status: status,
	}); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

// Nobody joins general and nobody is invited: it is every session on the
// daemon, worked out rather than stored.
func TestGeneralHoldsEverySessionThatHasNotEnded(t *testing.T) {
	s := channelStore(t)
	_ = s.EnsureGeneral()
	seedSession(t, s, "s2", "idle")
	seedSession(t, s, "s7", "active")
	seedSession(t, s, "s9", "terminated")

	members, err := s.ChannelMembers(GeneralChannel)
	if err != nil {
		t.Fatalf("members: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("members = %v, want the two that are still running", members)
	}

	// Ending one takes it out, and resuming it brings it back — with no
	// membership row touched either way. That is the point of deriving it.
	seedSession(t, s, "s2", "terminated")
	if after, _ := s.ChannelMembers(GeneralChannel); len(after) != 1 || after[0] != "s7" {
		t.Errorf("after terminating s2: %v, want only s7", after)
	}
	seedSession(t, s, "s2", "active")
	if back, _ := s.ChannelMembers(GeneralChannel); len(back) != 2 {
		t.Errorf("after resuming s2: %v, want it back in", back)
	}
}

// A snippet per post per session is fine for a channel of three. Thirty
// sessions and one sentence is thirty interruptions, which is the cost the
// notice board exists to avoid.
func TestGeneralDeliversToNobody(t *testing.T) {
	s := channelStore(t)
	_ = s.EnsureGeneral()
	for _, id := range []string{"s2", "s7", "s9"} {
		seedSession(t, s, id, "idle")
	}

	to, err := s.Unmuted(GeneralChannel, "")
	if err != nil {
		t.Fatalf("unmuted: %v", err)
	}
	if len(to) != 0 {
		t.Errorf("delivering general to %v, want nobody: it is read, not pushed", to)
	}
}

func TestNobodyJoinsOrLeavesGeneralByHand(t *testing.T) {
	s := channelStore(t)
	_ = s.EnsureGeneral()

	if _, err := s.AddMember(GeneralChannel, "s2"); err == nil {
		t.Error("want an error: every session is in it already")
	}
	if err := s.RemoveMember(GeneralChannel, "s2"); err == nil {
		t.Error("want an error: a session leaves by ending")
	}
}

// The whole of the one-layer rule. Answering a reply has to join the thread
// that reply is in, or a client would have to render a tree it was promised it
// would never see.
func TestAnsweringAReplyJoinsTheSameThread(t *testing.T) {
	s := channelStore(t)
	ch, _, _ := s.CreateChannel("", AuthorUser, []string{"s2"})

	root, _ := s.PostMessage(ch.ID, AuthorUser, "the response shape changed", false, "", nil)
	first, err := s.PostMessage(ch.ID, SessionAuthor("s2"), "which call sites?", false, root.ID, nil)
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	if first.ThreadRoot != root.ID {
		t.Fatalf("thread_root = %q, want the message it answers", first.ThreadRoot)
	}

	// Answering the reply, not the root.
	second, err := s.PostMessage(ch.ID, AuthorUser, "orders.ts only", false, first.ID, nil)
	if err != nil {
		t.Fatalf("reply to reply: %v", err)
	}
	if second.ThreadRoot != root.ID {
		t.Errorf("thread_root = %q, want the root %q — threads are one layer", second.ThreadRoot, root.ID)
	}
}

// The spine is what the channel is about. An aside between two members must not
// push the conversation off the top of it.
func TestRepliesStayOffTheSpine(t *testing.T) {
	s := channelStore(t)
	ch, _, _ := s.CreateChannel("", AuthorUser, []string{"s2"})

	root, _ := s.PostMessage(ch.ID, AuthorUser, "the response shape changed", false, "", nil)
	_, _ = s.PostMessage(ch.ID, SessionAuthor("s2"), "which call sites?", false, root.ID, nil)
	_, _ = s.PostMessage(ch.ID, AuthorUser, "orders.ts only", false, root.ID, nil)
	_, _ = s.PostMessage(ch.ID, AuthorUser, "separately: the flake is mine", false, "", nil)

	spine, err := s.Messages(ch.ID, "", 0)
	if err != nil {
		t.Fatalf("spine: %v", err)
	}
	if len(spine) != 2 {
		t.Fatalf("spine holds %d messages, want the two said to the channel", len(spine))
	}

	thread, err := s.ThreadMessages(ch.ID, root.ID)
	if err != nil {
		t.Fatalf("thread: %v", err)
	}
	if len(thread) != 3 {
		t.Errorf("thread holds %d, want the root and its two replies", len(thread))
	}
	if thread[0].ID != root.ID {
		t.Error("the thread should open with the message it hangs off")
	}
}

func TestTheSpineKnowsWhatHangsOffIt(t *testing.T) {
	s := channelStore(t)
	ch, _, _ := s.CreateChannel("", AuthorUser, []string{"s2", "s7"})

	root, _ := s.PostMessage(ch.ID, AuthorUser, "the response shape changed", false, "", nil)
	_, _ = s.PostMessage(ch.ID, SessionAuthor("s2"), "which call sites?", false, root.ID, nil)
	_, _ = s.PostMessage(ch.ID, SessionAuthor("s2"), "found them", false, root.ID, nil)
	_, _ = s.PostMessage(ch.ID, SessionAuthor("s7"), "same here", false, root.ID, nil)

	summaries, err := s.ThreadSummaries(ch.ID)
	if err != nil {
		t.Fatalf("summaries: %v", err)
	}
	got := summaries[root.ID]
	if got.Replies != 3 {
		t.Errorf("replies = %d, want 3", got.Replies)
	}
	// Distinct, and in the order they first spoke: the line reads "3 replies ·
	// s2, s7", not the same name twice.
	if len(got.Authors) != 2 || got.Authors[0] != SessionAuthor("s2") {
		t.Errorf("authors = %v, want each once, first speaker first", got.Authors)
	}
}

// The delivery set. Everyone who has said something in the thread, and nobody
// else in the channel.
func TestAThreadIsItsParticipants(t *testing.T) {
	s := channelStore(t)
	ch, _, _ := s.CreateChannel("", AuthorUser, []string{"s2", "s7", "s9"})

	root, _ := s.PostMessage(ch.ID, SessionAuthor("s2"), "the response shape changed", false, "", nil)
	_, _ = s.PostMessage(ch.ID, SessionAuthor("s7"), "which call sites?", false, root.ID, nil)

	who, err := s.ThreadParticipants(ch.ID, root.ID)
	if err != nil {
		t.Fatalf("participants: %v", err)
	}
	if len(who) != 2 {
		t.Errorf("participants = %v, want the root's author and the one reply", who)
	}
	for _, one := range who {
		if one == SessionAuthor("s9") {
			t.Error("s9 has said nothing in this thread and should not be in it")
		}
	}
}

// A badge that counts what you were never told about is a badge that never
// clears, so unread has to follow the same rule delivery does.
func TestABusyThreadDoesNotRaiseABadgeForSomebodyOutsideIt(t *testing.T) {
	s := channelStore(t)
	ch, _, _ := s.CreateChannel("", AuthorUser, []string{"s2", "s7", "s9"})

	root, _ := s.PostMessage(ch.ID, SessionAuthor("s2"), "the response shape changed", false, "", nil)
	_ = s.MarkRead(ch.ID, SessionAuthor("s9"))
	for range 5 {
		_, _ = s.PostMessage(ch.ID, SessionAuthor("s7"), "back and forth", false, root.ID, nil)
	}

	// s9 is in the channel but not in the thread.
	outside, err := s.Unread(ch.ID, SessionAuthor("s9"))
	if err != nil {
		t.Fatalf("unread: %v", err)
	}
	if outside != 0 {
		t.Errorf("unread = %d for somebody outside the thread, want 0", outside)
	}

	// s2 opened it, so the replies are theirs to read.
	_ = s.MarkRead(ch.ID, SessionAuthor("s2"))
	_, _ = s.PostMessage(ch.ID, SessionAuthor("s7"), "one more", false, root.ID, nil)
	if inside, _ := s.Unread(ch.ID, SessionAuthor("s2")); inside != 1 {
		t.Errorf("unread = %d for a participant, want 1", inside)
	}
}

func TestAThreadCannotHangOffAnotherChannelsMessage(t *testing.T) {
	s := channelStore(t)
	here, _, _ := s.CreateChannel("here", AuthorUser, []string{"s2"})
	elsewhere, _, _ := s.CreateChannel("elsewhere", AuthorUser, []string{"s2"})
	foreign, _ := s.PostMessage(elsewhere.ID, AuthorUser, "not your conversation", false, "", nil)

	if _, err := s.PostMessage(here.ID, AuthorUser, "answering", false, foreign.ID, nil); err == nil {
		t.Error("want an error: the quote would point at nothing in this channel")
	}
	if _, err := s.PostMessage(here.ID, AuthorUser, "answering", false, "m_nope", nil); err == nil {
		t.Error("want an error for a message that does not exist")
	}
}

func TestMentionsAreCountedApartFromUnread(t *testing.T) {
	s := channelStore(t)
	ch, _, _ := s.CreateChannel("", AuthorUser, []string{"s2"})

	_, _ = s.PostMessage(ch.ID, AuthorUser, "general traffic", false, "", nil)
	_, _ = s.PostMessage(ch.ID, AuthorUser, "@s2 this one is yours", false, "",
		[]string{SessionAuthor("s2")})

	unread, _ := s.Unread(ch.ID, SessionAuthor("s2"))
	mentions, err := s.Mentions(ch.ID, SessionAuthor("s2"))
	if err != nil {
		t.Fatalf("mentions: %v", err)
	}
	if unread != 2 {
		t.Errorf("unread = %d, want both", unread)
	}
	if mentions != 1 {
		t.Errorf("mentions = %d, want only the one that named them", mentions)
	}

	// Reading clears both, off the one receipt.
	_ = s.MarkRead(ch.ID, SessionAuthor("s2"))
	if after, _ := s.Mentions(ch.ID, SessionAuthor("s2")); after != 0 {
		t.Errorf("mentions after reading = %d, want 0", after)
	}
}

func TestAuthorNames(t *testing.T) {
	if got := SessionAuthor("s2"); got != "session:s2" {
		t.Errorf("SessionAuthor = %q", got)
	}
	if got := AuthorSession("session:s2"); got != "s2" {
		t.Errorf("AuthorSession = %q", got)
	}
	// A person's author name is not a session id dressed up as one.
	if got := AuthorSession(AuthorUser); got != AuthorUser {
		t.Errorf("AuthorSession(user) = %q, want it left alone", got)
	}
}
