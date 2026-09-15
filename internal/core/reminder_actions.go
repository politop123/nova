package core

import "time"

type ReminderQuickAction string

const (
	ReminderComplete ReminderQuickAction = "done"
	ReminderSnooze10 ReminderQuickAction = "s10"
	ReminderSnooze60 ReminderQuickAction = "s60"
)

func (a ReminderQuickAction) Valid() bool {
	return a == ReminderComplete || a == ReminderSnooze10 || a == ReminderSnooze60
}

func (a ReminderQuickAction) Delay() time.Duration {
	switch a {
	case ReminderSnooze10:
		return 10 * time.Minute
	case ReminderSnooze60:
		return time.Hour
	default:
		return 0
	}
}

func (a ReminderQuickAction) ActionName() string {
	if a == ReminderComplete {
		return "reminder.complete"
	}
	if a == ReminderSnooze10 || a == ReminderSnooze60 {
		return "reminder.snooze"
	}
	return ""
}
