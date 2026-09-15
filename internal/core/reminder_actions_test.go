package core

import (
	"testing"
	"time"
)

func TestReminderQuickActions(t *testing.T) {
	for _, tc := range []struct {
		action ReminderQuickAction
		delay  time.Duration
		name   string
	}{{ReminderComplete, 0, "reminder.complete"}, {ReminderSnooze10, 10 * time.Minute, "reminder.snooze"},
		{ReminderSnooze60, time.Hour, "reminder.snooze"}} {
		if !tc.action.Valid() || tc.action.Delay() != tc.delay || tc.action.ActionName() != tc.name {
			t.Fatalf("invalid quick action definition: %q", tc.action)
		}
	}
	if action := ReminderQuickAction("delete"); action.Valid() || action.ActionName() != "" || action.Delay() != 0 {
		t.Fatal("unknown quick action accepted")
	}
}
