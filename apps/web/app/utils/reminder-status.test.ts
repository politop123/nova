import { describe, expect, it } from 'vitest';
import { canEditReminder, reminderLabels } from './reminder-status';

describe('reminder completion state', () => {
  it('distinguishes sending a notification from completing the action', () => {
    expect(reminderLabels.delivered).toBe('Надіслано');
    expect(reminderLabels.completed).toBe('Виконано');
  });

  it('keeps completed and cancelled reminders as read-only history', () => {
    expect(canEditReminder('completed')).toBe(false);
    expect(canEditReminder('cancelled')).toBe(false);
    expect(canEditReminder('scheduled')).toBe(true);
    expect(canEditReminder('delivered')).toBe(true);
  });
});
