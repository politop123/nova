<script setup lang="ts">
const config = useRuntimeConfig();
const apiState = ref<'checking' | 'online' | 'offline'>('checking');
const conversation = ref<Conversation | null>(null);
const messages = ref<NovaMessage[]>([]);
const input = ref('');
const sending = ref(false);
const streamingText = ref('');
const errorMessage = ref('');

interface Conversation {
  id: string;
  title?: string;
  createdAt: string;
  updatedAt: string;
}

interface NovaMessage {
  id: string;
  role: 'user' | 'assistant' | 'tool' | 'system';
  content: string;
  createdAt: string;
}

onMounted(async () => {
  await bootstrapChat();
});

async function bootstrapChat() {
  try {
    await $fetch(`${config.public.apiBase}/health`);
    apiState.value = 'online';
    const result = await $fetch<{ conversations: Conversation[] }>(
      `${config.public.apiBase}/api/v1/conversations`,
    );
    conversation.value = result.conversations[0] ?? (await createConversation());
    await loadMessages();
  } catch {
    apiState.value = 'offline';
    errorMessage.value = 'Core недоступний. Запусти Go API та PostgreSQL.';
  }
}

async function createConversation(): Promise<Conversation> {
  return await $fetch<Conversation>(`${config.public.apiBase}/api/v1/conversations`, {
    method: 'POST',
    body: { title: 'Нова розмова' },
  });
}

async function loadMessages() {
  if (!conversation.value) return;
  const result = await $fetch<{ messages: NovaMessage[] }>(
    `${config.public.apiBase}/api/v1/conversations/${conversation.value.id}/messages`,
  );
  messages.value = result.messages;
}

async function sendMessage() {
  const text = input.value.trim();
  if (!text || sending.value || !conversation.value) return;
  sending.value = true;
  streamingText.value = '';
  errorMessage.value = '';
  try {
    const response = await fetch(
      `${config.public.apiBase}/api/v1/conversations/${conversation.value.id}/messages/stream`,
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ text }),
      },
    );
    if (!response.ok) {
      const payload = await response.json().catch(() => null);
      throw new Error(payload?.error ?? 'Не вдалося обробити повідомлення.');
    }
    if (!response.body) {
      throw new Error('Сервер не повернув потік відповіді.');
    }

    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = '';
    while (true) {
      const chunk = await reader.read();
      buffer += decoder.decode(chunk.value ?? new Uint8Array(), { stream: !chunk.done });
      const frames = buffer.split('\n\n');
      buffer = frames.pop() ?? '';
      for (const frame of frames) consumeSSEFrame(frame);
      if (chunk.done) break;
    }
    if (buffer.trim()) consumeSSEFrame(buffer);
    input.value = '';
  } catch (error: any) {
    errorMessage.value = error?.message ?? 'Не вдалося обробити повідомлення.';
  } finally {
    sending.value = false;
    streamingText.value = '';
  }
}

function consumeSSEFrame(frame: string) {
  const lines = frame.replaceAll('\r', '').split('\n');
  const event = lines
    .find((line) => line.startsWith('event:'))
    ?.slice(6)
    .trim();
  const data = lines
    .filter((line) => line.startsWith('data:'))
    .map((line) => line.slice(5).trim())
    .join('\n');
  if (!event || !data) return;
  const payload = JSON.parse(data);
  if (event === 'ready' && payload.userMessage) {
    messages.value.push(payload.userMessage);
  } else if (event === 'delta') {
    streamingText.value += payload.text ?? '';
  } else if (event === 'done' && payload.assistantMessage) {
    messages.value.push(payload.assistantMessage);
    streamingText.value = '';
  } else if (event === 'error') {
    throw new Error(payload.message ?? 'Потік відповіді завершився з помилкою.');
  }
}

const capabilities = [
  { label: 'Conversation', detail: 'One thread across Web and Telegram', state: 'Ready' },
  { label: 'Memory', detail: 'Bounded rolling conversation summary', state: 'Foundation' },
  { label: 'Reminders', detail: 'Deterministic scheduling and delivery', state: 'Planned' },
  { label: 'Policy', detail: 'READ, WRITE, and CONFIRM boundaries', state: 'Foundation' },
];
</script>

<template>
  <main class="shell">
    <nav class="nav">
      <div class="brand"><span class="brand-mark">N</span><span>NOVA</span></div>
      <div class="status" :data-state="apiState">
        <span class="status-dot" /> Core {{ apiState }}
      </div>
    </nav>

    <section class="hero">
      <p class="eyebrow">Personal AI Operating System</p>
      <h1>One calm place for memory, action, and attention.</h1>
      <p class="hero-copy">
        NOVA keeps the durable context on your server and calls AI only when language or reasoning
        is actually useful.
      </p>
      <div v-if="messages.length" class="messages" aria-live="polite">
        <article
          v-for="message in messages"
          :key="message.id"
          class="message"
          :data-role="message.role"
        >
          <span class="message-role">{{ message.role === 'user' ? 'You' : 'NOVA' }}</span>
          <p>{{ message.content }}</p>
        </article>
        <article v-if="streamingText" class="message" data-role="assistant">
          <span class="message-role">NOVA</span>
          <p>{{ streamingText }}</p>
        </article>
      </div>
      <div class="composer" aria-label="NOVA message composer">
        <textarea
          v-model="input"
          rows="1"
          placeholder="Ask NOVA or create a reminder..."
          :disabled="apiState !== 'online' || sending"
          @keydown.enter.exact.prevent="sendMessage"
        />
        <button type="button" :disabled="!input.trim() || sending" @click="sendMessage">
          {{ sending ? '...' : 'Send' }}
        </button>
      </div>
      <p v-if="errorMessage" class="error-note">{{ errorMessage }}</p>
      <p v-else class="preview-note">Enter sends the first message to the Go NOVA Core.</p>
    </section>

    <section class="capabilities" aria-label="MVP capabilities">
      <article v-for="item in capabilities" :key="item.label" class="capability">
        <div>
          <p class="capability-label">{{ item.label }}</p>
          <p>{{ item.detail }}</p>
        </div>
        <span>{{ item.state }}</span>
      </article>
    </section>
  </main>
</template>
