interface ChannelMessage {
  id: string;
  role: string;
  channel?: string;
}

export function hasNewTelegramReply(previousIds: Set<string>, messages: ChannelMessage[]) {
  return messages.some(
    (message) =>
      !previousIds.has(message.id) &&
      message.role === 'assistant' &&
      message.channel === 'telegram',
  );
}

export function shouldRefreshTasks(payload: { createdTask?: unknown; refreshTasks?: boolean }) {
  return Boolean(payload.createdTask || payload.refreshTasks);
}
