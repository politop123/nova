export type ReminderStatus = 'scheduled' | 'delivered' | 'completed' | 'cancelled';

export const reminderLabels: Record<ReminderStatus, string> = {
  scheduled: 'Заплановано',
  delivered: 'Надіслано',
  completed: 'Виконано',
  cancelled: 'Скасовано',
};

export function canEditReminder(status: ReminderStatus): boolean {
  return status === 'scheduled' || status === 'delivered';
}
