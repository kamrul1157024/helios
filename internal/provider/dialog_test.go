package provider

import (
	"strings"
	"testing"
)

// Verbatim from a Claude Code 2.1.x session under a helios ptyhost. Note the
// highlight: the default is the destructive option.
const claudeTrustScreen = `
 Accessing workspace:
 /home/u/workspace/opal-app/primary-agent

 Quick safety check: Is this a project you created or one you trust?

 Security guide
❯ No, exit
  Yes, I trust this folder
 Enter to confirm · Esc to cancel
`

// Verbatim from codex-cli 0.150.1.
const codexTrustScreen = `
You are in /tmp/proj
Do you trust the contents of this directory?
› 1. Yes, continue
  2. No, quit
Press enter to continue
`

func TestLocateChoiceFindsTheAffirmativeRow(t *testing.T) {
	cursor, target, ok := locateChoice(claudeTrustScreen, "trust this folder")
	if !ok {
		t.Fatal("did not find both the highlight and the option")
	}
	if cursor == target {
		t.Fatal("read the highlight as already on the affirmative option; " +
			"this is the mistake that made helios answer 'No, exit'")
	}
	if target <= cursor {
		t.Errorf("affirmative row %d should be below the highlight at %d", target, cursor)
	}
}

// Codex highlights the affirmative option, so nothing needs moving. Both
// layouts must be read correctly by the same code, or one agent gets answered
// wrongly whenever the other changes.
func TestLocateChoiceHandlesAnAlreadyCorrectHighlight(t *testing.T) {
	cursor, target, ok := locateChoice(codexTrustScreen, "yes, continue")
	if !ok {
		t.Fatal("did not find both the highlight and the option")
	}
	if cursor != target {
		t.Errorf("cursor %d, target %d: codex already highlights the affirmative row", cursor, target)
	}
}

// The prose above a dialog often names the option before offering it. The
// offer is the row to move to.
func TestLocateChoicePrefersTheOfferOverTheProse(t *testing.T) {
	screen := "Do you want to trust this folder?\n❯ No, exit\n  Yes, I trust this folder\n"
	cursor, target, ok := locateChoice(screen, "trust this folder")
	if !ok {
		t.Fatal("not found")
	}
	if target <= cursor {
		t.Errorf("matched the question at row %d instead of the option below the highlight at %d",
			target, cursor)
	}
}

func TestLocateChoiceReportsAMissingOption(t *testing.T) {
	if _, _, ok := locateChoice(claudeTrustScreen, "definitely not on screen"); ok {
		t.Error("claimed to find an option that is not there")
	}
	// No highlight at all is also a miss: pressing Return blind is how a
	// mis-read screen becomes a destructive answer.
	if _, _, ok := locateChoice("just some text\nand more\n", "some"); ok {
		t.Error("claimed a highlight on a screen with none")
	}
}

func TestConfirmChoiceRefusesWithoutATerminal(t *testing.T) {
	if err := ConfirmChoice(nil, "sess-1", "yes"); err == nil {
		t.Error("accepted a nil backend")
	} else if !strings.Contains(err.Error(), "no terminal") {
		t.Errorf("unhelpful error: %v", err)
	}
}
