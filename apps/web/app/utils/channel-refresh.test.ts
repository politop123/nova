import { describe, expect, it } from 'vitest';
import { hasNewTelegramReply, shouldRefreshTasks } from './channel-refresh';

describe('reminder refresh after Telegram replies', () => {
  const telegramReply = { id: 'reply', role: 'assistant', channel: 'telegram' };
  it('refreshes after a new bot reply and ignores already merged replies', () => {
    expect(hasNewTelegramReply(new Set(), [telegramReply])).toBe(true);
    expect(hasNewTelegramReply(new Set(['reply']), [telegramReply])).toBe(false);
  });
  it('waits for the assistant action and ignores web messages', () => {
    expect(hasNewTelegramReply(new Set(), [{ ...telegramReply, role: 'user' }])).toBe(false);
    expect(hasNewTelegramReply(new Set(), [{ ...telegramReply, channel: 'web' }])).toBe(false);
  });
});

describe('task refresh after chat actions', () => {
  it('refreshes task creation and mutations even within mixed-intent plans', () => {
    expect(shouldRefreshTasks({ createdTask: { id: 'task' } })).toBe(true);
    expect(shouldRefreshTasks({ refreshTasks: true })).toBe(true);
    expect(shouldRefreshTasks({ refreshTasks: false })).toBe(false);
    expect(shouldRefreshTasks({})).toBe(false);
  });
});
