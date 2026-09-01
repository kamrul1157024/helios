package server

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/kamrul1157024/helios/internal/notifications"
	"github.com/kamrul1157024/helios/internal/provider"
	"github.com/kamrul1157024/helios/internal/store"
)

// The daemon's own notification namespace.
//
// The type is `helios.question` rather than `helios.schedule_missed` for a
// reason that saves every client a card: both dispatch on the part *after* the
// first dot — kindOf in desktop/src/shared/notifications.ts, notification.kind
// in mobile/lib/providers/card_registry.dart — so a question raised by the
// daemon renders in the question card the apps already have, and is answered
// with the body those cards already send.
const (
	SystemProviderID    = "helios"
	NotifScheduleMissed = "helios.question"
)

// scheduler is the one the action handler answers into. Set when the daemon
// builds it, following the package-var idiom the claude provider uses for the
// same problem.
var scheduler *Scheduler

// RegisterScheduleActions makes the daemon's questions answerable.
func RegisterScheduleActions(s *Scheduler) {
	scheduler = s
	provider.RegisterSystemActor(SystemProviderID, map[string]provider.ActionRoute{
		NotifScheduleMissed: {
			Handler:      handleScheduleQuestion,
			Label:        "Helios asks",
			Detail:       "A scheduled run that was missed, and anything else Helios needs to ask",
			Blocking:     false,
			Group:        "action_required",
			DefaultAlert: true,
		},
	})
}

// handleScheduleQuestion answers a missed-run question.
//
// The body is the one the question card sends: option 0 is "Run now", option 1
// is "Skip". Nothing is held waiting on this — unlike a permission request,
// there is no agent blocked on the answer — so the decision is recorded and the
// run, if asked for, starts here.
func handleScheduleQuestion(notif *store.Notification, body json.RawMessage) (notifications.Decision, error) {
	var req struct {
		Action     string `json:"action"`
		Selections []struct {
			QuestionIndex int `json:"question_index"`
			OptionIndex   int `json:"option_index"`
		} `json:"selections"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return notifications.Decision{}, fmt.Errorf("invalid answer: %w", err)
	}
	if len(req.Selections) == 0 {
		return notifications.Decision{}, fmt.Errorf("missing selections")
	}

	// The schedule is on the notification's source_session, which is where a
	// question about a schedule belongs: the column is NOT NULL and every
	// sweep keys on it.
	scheduleID := notif.SourceSession
	if req.Selections[0].OptionIndex == 0 && scheduler != nil {
		if err := scheduler.RunNow(scheduleID); err != nil {
			log.Printf("schedule question: run %s now: %v", scheduleID, err)
		}
	}

	response, err := json.Marshal(map[string]interface{}{"selections": req.Selections})
	if err != nil {
		return notifications.Decision{}, err
	}
	return notifications.Decision{Status: "answered", Response: response}, nil
}
