<script setup lang="ts">
const config = useRuntimeConfig();

type ApiState = 'checking' | 'online' | 'offline';
type ViewName = 'chat' | 'memory' | 'tasks';

interface Conversation {
  id: string;
  title?: string;
  createdAt: string;
  updatedAt: string;
}

interface NovaMessage {
  id: string;
  role: 'user' | 'assistant' | 'tool' | 'system';
  channel?: 'web' | 'telegram' | string;
  content: string;
  createdAt: string;
}

interface MemoryRecord {
  id: string;
  kind: string;
  content: string;
  confidence: number;
  sourceMessageId?: string;
  projectKey?: string;
  expiresAt?: string;
  createdAt: string;
  updatedAt: string;
}

interface TaskRecord {
  id: string;
  title: string;
  details?: string;
  status: 'open' | 'done' | 'cancelled';
  dueAt?: string;
  completedAt?: string;
  createdAt: string;
  updatedAt: string;
}

interface ReminderRecord {
  id: string;
  title: string;
  triggerAt: string;
  timezone: string;
  recurrenceRule?: string;
  priority: 'low' | 'normal' | 'high';
  deliveryMethod: 'web' | 'telegram';
  status: 'scheduled' | 'delivered' | 'cancelled';
  createdAt: string;
  updatedAt: string;
}

interface GitStatus {
  repository: string;
  branch: string;
  latestCommit: GitCommit;
  deployedCommit: GitCommitRef;
  latestDeployRun?: GitWorkflowRun;
  deployment: GitDeploymentState;
  actionsError?: string;
  checkedAt: string;
}

interface GitCommit {
  sha: string;
  shortSha: string;
  message: string;
  title: string;
  authorName?: string;
  committedAt?: string;
  url?: string;
}

interface GitCommitRef {
  sha?: string;
  shortSha?: string;
}

interface GitWorkflowRun {
  id: number;
  name: string;
  path?: string;
  status: string;
  conclusion?: string;
  headSha: string;
  shortHeadSha: string;
  url?: string;
  createdAt?: string;
  updatedAt?: string;
}

interface GitDeploymentState {
  state: 'unknown' | 'current' | 'deploying' | 'published' | 'failed' | 'behind' | string;
  isLatest: boolean;
  summary: string;
}

interface OperationalStatus {
  service: string;
  status: 'ok' | 'degraded' | string;
  version: string;
  timestamp: string;
  checks: Record<string, OperationalStatusCheck>;
  git?: GitStatus;
}

interface OperationalStatusCheck {
  label: string;
  status: 'ok' | 'degraded' | 'not_configured' | string;
  detail?: string;
  lastSeenAt?: string;
  ageSeconds?: number;
}

const apiState = ref<ApiState>('checking');
const activeView = ref<ViewName>('chat');
const timezone = ref('Europe/Kyiv');
const lastSync = ref('');
const errorMessage = ref('');

const conversation = ref<Conversation | null>(null);
const messages = ref<NovaMessage[]>([]);
const input = ref('');
const sending = ref(false);
const streamingText = ref('');
let messagePoll: number | undefined;

const memories = ref<MemoryRecord[]>([]);
const memorySearch = ref('');
const memorySaving = ref(false);
const editingMemoryId = ref<string | null>(null);
const memoryForm = reactive({
  kind: 'preference',
  content: '',
  confidence: 0.8,
  projectKey: '',
  expiresAt: '',
});

const tasks = ref<TaskRecord[]>([]);
const taskSaving = ref(false);
const editingTaskId = ref<string | null>(null);
const taskForm = reactive({
  title: '',
  details: '',
  dueAt: '',
});

const reminders = ref<ReminderRecord[]>([]);
const reminderSaving = ref(false);
const editingReminderId = ref<string | null>(null);
const reminderForm = reactive({
  title: '',
  triggerAt: '',
  priority: 'normal' as ReminderRecord['priority'],
  deliveryMethod: 'web' as ReminderRecord['deliveryMethod'],
});

const gitStatus = ref<GitStatus | null>(null);
const gitStatusLoading = ref(false);
const gitStatusError = ref('');
const operationalStatus = ref<OperationalStatus | null>(null);
const operationalStatusLoading = ref(false);
const operationalStatusError = ref('');

const tabs: Array<{ key: ViewName; label: string }> = [
  { key: 'chat', label: 'Чат' },
  { key: 'memory', label: 'Памʼять' },
  { key: 'tasks', label: 'Задачі' },
];

const statusLabels: Record<ApiState, string> = {
  checking: 'перевірка',
  online: 'онлайн',
  offline: 'офлайн',
};

const taskLabels: Record<TaskRecord['status'], string> = {
  open: 'В роботі',
  done: 'Готово',
  cancelled: 'Скасовано',
};

const reminderLabels: Record<ReminderRecord['status'], string> = {
  scheduled: 'Заплановано',
  delivered: 'Доставлено',
  cancelled: 'Скасовано',
};

const priorityLabels: Record<ReminderRecord['priority'], string> = {
  low: 'Низький',
  normal: 'Звичайний',
  high: 'Високий',
};

const gitDeploymentLabels: Record<string, string> = {
  unknown: 'SHA невідомий',
  current: 'Прод актуальний',
  deploying: 'Деплой у процесі',
  published: 'Workflow успішний',
  failed: 'Деплой впав',
  behind: 'Прод відстає',
};

const activeTasks = computed(() => tasks.value.filter((task) => task.status === 'open').length);
const scheduledReminders = computed(
  () => reminders.value.filter((reminder) => reminder.status === 'scheduled').length,
);
const memoryCount = computed(() => memories.value.length);
const telegramMessageCount = computed(
  () => messages.value.filter((message) => message.channel === 'telegram').length,
);
const upcomingReminder = computed(() => {
  return [...reminders.value]
    .filter((reminder) => reminder.status === 'scheduled')
    .sort((left, right) => {
      return new Date(left.triggerAt).getTime() - new Date(right.triggerAt).getTime();
    })[0];
});
const metricCards = computed(() => [
  {
    label: 'Повідомлення',
    value: messages.value.length,
    detail: telegramMessageCount.value
      ? `${telegramMessageCount.value} з Telegram`
      : 'Web + Telegram',
    icon: '✦',
  },
  {
    label: 'Памʼять',
    value: memoryCount.value,
    detail: 'факти й вподобання',
    icon: '◇',
  },
  {
    label: 'Задачі',
    value: activeTasks.value,
    detail: 'активні зараз',
    icon: '✓',
  },
  {
    label: 'Нагадування',
    value: scheduledReminders.value,
    detail: upcomingReminder.value
      ? `найближче ${formatDate(upcomingReminder.value.triggerAt)}`
      : 'немає черги',
    icon: '◴',
  },
]);
const gitStatusTitle = computed(() => {
  if (gitStatusLoading.value) return 'Перевіряю GitHub';
  if (gitStatusError.value) return 'Git недоступний';
  if (!gitStatus.value) return 'Git ще без даних';
  return gitStatus.value.deployment.isLatest
    ? `актуальний · ${gitStatus.value.latestCommit.shortSha}`
    : `${gitStatus.value.latestCommit.shortSha} очікує`;
});
const gitStatusDetail = computed(() => {
  if (gitStatusError.value) return gitStatusError.value;
  if (!gitStatus.value) return 'NOVA зчитує repo/status із Core API';
  const deployed = gitStatus.value.deployedCommit.shortSha
    ? `prod ${gitStatus.value.deployedCommit.shortSha}`
    : 'prod SHA невідомий';
  const workflow = gitStatus.value.latestDeployRun
    ? `workflow ${workflowStatusLabel(gitStatus.value.latestDeployRun)}`
    : 'workflow без даних';
  return `${gitDeploymentLabels[gitStatus.value.deployment.state] ?? gitStatus.value.deployment.state} · ${deployed} · ${workflow}`;
});
const operationalStatusTitle = computed(() => {
  if (operationalStatusLoading.value) return 'Перевіряю системи';
  if (operationalStatusError.value) return 'Статус недоступний';
  if (!operationalStatus.value) return 'Очікую Core';
  return operationalStatus.value.status === 'ok' ? 'все живе' : 'є нюанси';
});
const operationalStatusDetail = computed(() => {
  if (operationalStatusError.value) return operationalStatusError.value;
  if (!operationalStatus.value) return 'API, база, Redis, worker, Telegram';
  const attention = Object.values(operationalStatus.value.checks)
    .filter((check) => check.status !== 'ok')
    .map((check) => `${check.label}: ${checkStatusLabel(check.status)}`);
  if (!attention.length) return 'API, DB, Redis, Worker, Telegram — ok';
  return attention.slice(0, 3).join(' · ');
});
const quickPrompts = [
  'Нагадай через 10 хвилин перевірити NOVA',
  'Запамʼятай, що Telegram — мій основний канал',
  'Які задачі зараз відкриті?',
  'NOVA, що з тобою? Чи все працює?',
  'Який останній commit у NOVA?',
  'Чи задеплоївся останній commit?',
];

onMounted(async () => {
  timezone.value = Intl.DateTimeFormat().resolvedOptions().timeZone || 'Europe/Kyiv';
  reminderForm.triggerAt = nextHourInput();
  await bootstrapApp();
});

onBeforeUnmount(() => {
  stopMessagePolling();
});

async function bootstrapApp() {
  try {
    await apiFetch('/health');
    apiState.value = 'online';
    const result = await apiFetch<{ conversations: Conversation[] }>('/api/v1/conversations');
    conversation.value = result.conversations[0] ?? (await createConversation());
    await Promise.all([
      loadMessages(),
      loadMemories(),
      loadTasks(),
      loadReminders(),
      loadOperationalStatus(),
    ]);
    startMessagePolling();
    markSynced();
  } catch (error: any) {
    apiState.value = 'offline';
    stopMessagePolling();
    errorMessage.value = normalizeError(error, 'Core недоступний. Перевір сервер NOVA.');
  }
}

async function createConversation(): Promise<Conversation> {
  return await apiFetch<Conversation>('/api/v1/conversations', {
    method: 'POST',
    body: { title: 'Нова розмова' },
  });
}

async function loadMessages(options: { merge?: boolean } = {}) {
  if (!conversation.value) return;
  const result = await apiFetch<{ messages: NovaMessage[] }>(
    `/api/v1/conversations/${conversation.value.id}/messages`,
  );
  if (options.merge) {
    mergeMessages(result.messages);
  } else {
    messages.value = result.messages;
  }
  markSynced();
}

function startMessagePolling() {
  if (messagePoll !== undefined || !conversation.value) return;
  messagePoll = window.setInterval(() => {
    void refreshMessagesFromChannels();
  }, 5000);
}

function stopMessagePolling() {
  if (messagePoll === undefined) return;
  window.clearInterval(messagePoll);
  messagePoll = undefined;
}

async function refreshMessagesFromChannels() {
  if (!conversation.value || sending.value) return;
  try {
    await loadMessages({ merge: true });
  } catch {
    // Keep the current chat visible when a background refresh misses one beat.
  }
}

function mergeMessages(nextMessages: NovaMessage[]) {
  const merged = new Map(messages.value.map((message) => [message.id, message]));
  for (const message of nextMessages) {
    merged.set(message.id, message);
  }
  messages.value = Array.from(merged.values()).sort((left, right) => {
    return new Date(left.createdAt).getTime() - new Date(right.createdAt).getTime();
  });
}

function upsertMessage(message?: NovaMessage) {
  if (!message) return;
  mergeMessages([message]);
}

async function loadMemories() {
  const query = memorySearch.value.trim();
  const suffix = query ? `?q=${encodeURIComponent(query)}` : '';
  const result = await apiFetch<{ memories: MemoryRecord[] }>(`/api/v1/memories${suffix}`);
  memories.value = result.memories;
  markSynced();
}

async function loadTasks() {
  const result = await apiFetch<{ tasks: TaskRecord[] }>('/api/v1/tasks');
  tasks.value = result.tasks;
  markSynced();
}

async function loadReminders() {
  const result = await apiFetch<{ reminders: ReminderRecord[] }>('/api/v1/reminders');
  reminders.value = result.reminders;
  markSynced();
}

async function loadGitStatus() {
  gitStatusLoading.value = true;
  try {
    gitStatus.value = await apiFetch<GitStatus>('/api/v1/devops/git/status');
    gitStatusError.value = '';
  } catch (error: any) {
    gitStatus.value = null;
    gitStatusError.value = normalizeError(error, 'Git статус тимчасово недоступний.');
  } finally {
    gitStatusLoading.value = false;
  }
}

async function loadOperationalStatus() {
  operationalStatusLoading.value = true;
  try {
    operationalStatus.value = await apiFetch<OperationalStatus>('/api/v1/status');
    operationalStatusError.value = '';
    if (operationalStatus.value.git) {
      gitStatus.value = operationalStatus.value.git;
      gitStatusError.value = '';
    } else {
      gitStatus.value = null;
      gitStatusError.value = operationalStatus.value.checks.git?.detail ?? '';
    }
  } catch (error: any) {
    operationalStatus.value = null;
    operationalStatusError.value = normalizeError(error, 'Статус NOVA тимчасово недоступний.');
  } finally {
    operationalStatusLoading.value = false;
  }
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
    markSynced();
  } catch (error: any) {
    errorMessage.value = normalizeError(error, 'Не вдалося обробити повідомлення.');
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
    upsertMessage(payload.userMessage);
  } else if (event === 'delta') {
    streamingText.value += payload.text ?? '';
  } else if (event === 'done' && payload.assistantMessage) {
    upsertMessage(payload.assistantMessage);
    streamingText.value = '';
    if (payload.createdMemory) {
      void loadMemories().catch((error: any) => {
        errorMessage.value = normalizeError(error, 'Памʼять створено, але список не оновився.');
      });
    }
    if (payload.createdTask) {
      void loadTasks().catch((error: any) => {
        errorMessage.value = normalizeError(error, 'Задачу створено, але список не оновився.');
      });
    }
    if (payload.createdReminder) {
      void loadReminders().catch((error: any) => {
        errorMessage.value = normalizeError(error, 'Нагадування створено, але список не оновився.');
      });
    }
    if (payload.route?.intent === 'git.status') {
      void loadGitStatus();
    }
    if (payload.route?.intent === 'system.status') {
      void loadOperationalStatus();
    }
  } else if (event === 'error') {
    throw new Error(payload.message ?? 'Потік відповіді завершився з помилкою.');
  }
}

async function saveMemory() {
  if (!memoryForm.content.trim() || memorySaving.value) return;
  memorySaving.value = true;
  errorMessage.value = '';
  const body = {
    kind: memoryForm.kind,
    content: memoryForm.content.trim(),
    confidence: Number(memoryForm.confidence),
    projectKey: memoryForm.projectKey.trim(),
    expiresAt: memoryForm.expiresAt ? new Date(memoryForm.expiresAt).toISOString() : '',
  };
  try {
    if (editingMemoryId.value) {
      await apiFetch(`/api/v1/memories/${editingMemoryId.value}`, { method: 'PATCH', body });
    } else {
      await apiFetch('/api/v1/memories', { method: 'POST', body });
    }
    resetMemoryForm();
    await loadMemories();
  } catch (error: any) {
    errorMessage.value = normalizeError(error, 'Не вдалося зберегти памʼять.');
  } finally {
    memorySaving.value = false;
  }
}

function editMemory(memory: MemoryRecord) {
  editingMemoryId.value = memory.id;
  memoryForm.kind = memory.kind;
  memoryForm.content = memory.content;
  memoryForm.confidence = memory.confidence;
  memoryForm.projectKey = memory.projectKey ?? '';
  memoryForm.expiresAt = toDatetimeInput(memory.expiresAt);
}

async function deleteMemory(memory: MemoryRecord) {
  await apiFetch(`/api/v1/memories/${memory.id}`, { method: 'DELETE' });
  if (editingMemoryId.value === memory.id) resetMemoryForm();
  await loadMemories();
}

function resetMemoryForm() {
  editingMemoryId.value = null;
  memoryForm.kind = 'preference';
  memoryForm.content = '';
  memoryForm.confidence = 0.8;
  memoryForm.projectKey = '';
  memoryForm.expiresAt = '';
}

async function saveTask() {
  if (!taskForm.title.trim() || taskSaving.value) return;
  taskSaving.value = true;
  errorMessage.value = '';
  const body = {
    title: taskForm.title.trim(),
    details: taskForm.details.trim(),
    dueAt: taskForm.dueAt,
    timezone: timezone.value,
  };
  try {
    if (editingTaskId.value) {
      await apiFetch(`/api/v1/tasks/${editingTaskId.value}`, { method: 'PATCH', body });
    } else {
      await apiFetch('/api/v1/tasks', { method: 'POST', body });
    }
    resetTaskForm();
    await loadTasks();
  } catch (error: any) {
    errorMessage.value = normalizeError(error, 'Не вдалося зберегти задачу.');
  } finally {
    taskSaving.value = false;
  }
}

function editTask(task: TaskRecord) {
  editingTaskId.value = task.id;
  taskForm.title = task.title;
  taskForm.details = task.details ?? '';
  taskForm.dueAt = toDatetimeInput(task.dueAt);
}

async function setTaskStatus(task: TaskRecord, status: TaskRecord['status']) {
  await apiFetch(`/api/v1/tasks/${task.id}`, { method: 'PATCH', body: { status } });
  await loadTasks();
}

async function cancelTask(task: TaskRecord) {
  await apiFetch(`/api/v1/tasks/${task.id}`, { method: 'DELETE' });
  if (editingTaskId.value === task.id) resetTaskForm();
  await loadTasks();
}

function resetTaskForm() {
  editingTaskId.value = null;
  taskForm.title = '';
  taskForm.details = '';
  taskForm.dueAt = '';
}

async function saveReminder() {
  if (!reminderForm.title.trim() || !reminderForm.triggerAt || reminderSaving.value) return;
  reminderSaving.value = true;
  errorMessage.value = '';
  const body = {
    title: reminderForm.title.trim(),
    triggerAt: reminderForm.triggerAt,
    timezone: timezone.value,
    priority: reminderForm.priority,
    deliveryMethod: reminderForm.deliveryMethod,
  };
  try {
    if (editingReminderId.value) {
      await apiFetch(`/api/v1/reminders/${editingReminderId.value}`, { method: 'PATCH', body });
    } else {
      await apiFetch('/api/v1/reminders', { method: 'POST', body });
    }
    resetReminderForm();
    await loadReminders();
  } catch (error: any) {
    errorMessage.value = normalizeError(error, 'Не вдалося зберегти нагадування.');
  } finally {
    reminderSaving.value = false;
  }
}

function editReminder(reminder: ReminderRecord) {
  editingReminderId.value = reminder.id;
  reminderForm.title = reminder.title;
  reminderForm.triggerAt = toDatetimeInput(reminder.triggerAt);
  reminderForm.priority = reminder.priority;
  reminderForm.deliveryMethod = reminder.deliveryMethod;
}

async function cancelReminder(reminder: ReminderRecord) {
  await apiFetch(`/api/v1/reminders/${reminder.id}`, { method: 'DELETE' });
  if (editingReminderId.value === reminder.id) resetReminderForm();
  await loadReminders();
}

function resetReminderForm() {
  editingReminderId.value = null;
  reminderForm.title = '';
  reminderForm.triggerAt = nextHourInput();
  reminderForm.priority = 'normal';
  reminderForm.deliveryMethod = 'web';
}

async function apiFetch<T>(path: string, options: Record<string, any> = {}): Promise<T> {
  return await $fetch<T>(`${config.public.apiBase}${path}`, options);
}

function formatDate(value?: string) {
  if (!value) return 'Без дати';
  return new Intl.DateTimeFormat('uk-UA', {
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(new Date(value));
}

function toDatetimeInput(value?: string) {
  if (!value) return '';
  return formatDatetimeInput(new Date(value));
}

function nextHourInput() {
  const date = new Date(Date.now() + 60 * 60 * 1000);
  date.setMinutes(0, 0, 0);
  return formatDatetimeInput(date);
}

function formatDatetimeInput(date: Date) {
  const pad = (value: number) => value.toString().padStart(2, '0');
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(
    date.getHours(),
  )}:${pad(date.getMinutes())}`;
}

function markSynced() {
  lastSync.value = new Intl.DateTimeFormat('uk-UA', {
    hour: '2-digit',
    minute: '2-digit',
  }).format(new Date());
}

function normalizeError(error: any, fallback: string) {
  const message = error?.data?.error ?? error?.message ?? fallback;
  if (message.includes('OPENAI_API_KEY')) {
    return 'OpenAI API key ще не підключений. Чат працює для детермінованих команд, повні AI-відповіді увімкнемо після додавання ключа.';
  }
  if (message.includes('storage')) return 'Сховище тимчасово недоступне.';
  return message;
}

function messageAuthorLabel(message: NovaMessage) {
  if (message.role === 'user') {
    return message.channel === 'telegram' ? 'Ти · Telegram' : 'Ти';
  }
  if (message.role === 'assistant') {
    return message.channel === 'telegram' ? 'NOVA · Telegram' : 'NOVA';
  }
  return message.role;
}

function usePrompt(prompt: string) {
  activeView.value = 'chat';
  input.value = prompt;
}

function deliveryMethodLabel(method: ReminderRecord['deliveryMethod']) {
  return method === 'telegram' ? 'Telegram' : 'Web';
}

function workflowStatusLabel(run: GitWorkflowRun) {
  if (run.status === 'completed') {
    if (run.conclusion === 'success') return 'успішний';
    if (run.conclusion === 'failure') return 'впав';
    if (run.conclusion === 'cancelled') return 'скасований';
    return run.conclusion || 'завершений';
  }
  if (run.status === 'in_progress') return 'в процесі';
  if (run.status === 'queued') return 'у черзі';
  return run.status || 'невідомий';
}

function checkStatusLabel(status: OperationalStatusCheck['status']) {
  if (status === 'ok') return 'ok';
  if (status === 'not_configured') return 'не налаштовано';
  if (status === 'degraded') return 'проблема';
  return status;
}

function memoryKindLabel(kind: string) {
  const labels: Record<string, string> = {
    preference: 'Вподобання',
    fact: 'Факт',
    project: 'Проєкт',
    constraint: 'Обмеження',
  };
  return labels[kind] ?? kind;
}
</script>

<template>
  <main class="app-shell">
    <header class="topbar">
      <div class="topbar-left">
        <div class="brand">
          <span class="brand-mark">N</span>
          <div>
            <p>NOVA</p>
            <span>персональна ОС</span>
          </div>
        </div>

        <nav class="tabs" aria-label="Розділи NOVA">
          <button
            v-for="tab in tabs"
            :key="tab.key"
            type="button"
            :class="{ active: activeView === tab.key }"
            @click="activeView = tab.key"
          >
            {{ tab.label }}
          </button>
        </nav>
      </div>

      <div class="topbar-actions">
        <span class="status-pill" :data-state="apiState">
          <span class="status-dot" />
          Core: {{ statusLabels[apiState] }}
        </span>
        <button class="ghost-button" type="button" @click="bootstrapApp">Оновити</button>
      </div>
    </header>

    <section class="command-deck" aria-label="Поточний контекст NOVA">
      <div class="command-copy">
        <p class="eyebrow">NOVA Core</p>
        <h1>Операційний центр</h1>
        <p>
          Web — допоміжна панель для памʼяті, задач і діагностики. Основне спілкування лишається в
          Telegram.
        </p>
      </div>

      <div class="command-status-card">
        <span>Найближче нагадування</span>
        <strong>{{
          upcomingReminder ? formatDate(upcomingReminder.triggerAt) : 'Немає в черзі'
        }}</strong>
        <p>
          {{
            upcomingReminder
              ? deliveryMethodLabel(upcomingReminder.deliveryMethod)
              : 'Канал оберемо під задачу'
          }}
        </p>
      </div>

      <div class="command-status-card">
        <span>Стан NOVA</span>
        <strong>{{ operationalStatusTitle }}</strong>
        <p>{{ operationalStatusDetail }}</p>
      </div>

      <div class="command-status-card">
        <span>Git / Deploy</span>
        <strong>{{ gitStatusTitle }}</strong>
        <p>{{ gitStatusDetail }}</p>
      </div>

      <div class="command-status-card">
        <span>Оновлено</span>
        <strong>{{ lastSync || 'ще немає' }}</strong>
        <p>{{ timezone }}</p>
      </div>
    </section>

    <section class="metrics" aria-label="Поточний стан">
      <article v-for="card in metricCards" :key="card.label" class="metric-card">
        <div>
          <span class="metric-icon">{{ card.icon }}</span>
          <p>{{ card.label }}</p>
        </div>
        <strong>{{ card.value }}</strong>
        <small>{{ card.detail }}</small>
      </article>
    </section>

    <p v-if="errorMessage" class="error-note">{{ errorMessage }}</p>

    <section v-if="activeView === 'chat'" class="workspace chat-view">
      <section class="work-panel chat-panel" aria-label="Чат з NOVA">
        <div class="panel-title panel-title-row">
          <div>
            <p>Жива розмова</p>
            <h1>{{ conversation?.title || 'Нова розмова' }}</h1>
          </div>
          <span class="panel-chip">{{ messages.length }} повідомлень</span>
        </div>

        <div class="messages" aria-live="polite">
          <article
            v-for="message in messages"
            :key="message.id"
            class="message"
            :data-role="message.role"
          >
            <span>{{ messageAuthorLabel(message) }}</span>
            <p>{{ message.content }}</p>
          </article>
          <article v-if="streamingText" class="message" data-role="assistant">
            <span>NOVA</span>
            <p>{{ streamingText }}</p>
          </article>
          <div v-if="!messages.length" class="empty-state">
            <strong>Розмова ще порожня</strong>
            <span>Почни тут або напиши боту в Telegram — історія буде спільною.</span>
          </div>
        </div>

        <div class="composer" aria-label="Нове повідомлення">
          <textarea
            v-model="input"
            rows="1"
            placeholder="Напиши NOVA або створи нагадування..."
            :disabled="apiState !== 'online' || sending"
            @keydown.enter.exact.prevent="sendMessage"
          />
          <button type="button" :disabled="!input.trim() || sending" @click="sendMessage">
            {{ sending ? '...' : 'Надіслати' }}
          </button>
        </div>
      </section>

      <aside class="side-rail" aria-label="Стан робочої області">
        <div class="rail-card">
          <p class="rail-label">Канали</p>
          <strong>Telegram + Web</strong>
          <span>Єдине ядро, одна памʼять, одна історія.</span>
        </div>
        <div class="rail-card">
          <p class="rail-label">Швидкий старт</p>
          <button
            v-for="prompt in quickPrompts"
            :key="prompt"
            class="prompt-chip"
            type="button"
            @click="usePrompt(prompt)"
          >
            {{ prompt }}
          </button>
        </div>
      </aside>
    </section>

    <section v-else-if="activeView === 'memory'" class="workspace two-column">
      <form class="work-panel editor-panel" aria-label="Форма памʼяті" @submit.prevent="saveMemory">
        <div class="panel-title">
          <p>Памʼять NOVA</p>
          <h1>{{ editingMemoryId ? 'Редагування' : 'Новий запис' }}</h1>
        </div>

        <label>
          Тип
          <select v-model="memoryForm.kind">
            <option value="preference">Вподобання</option>
            <option value="fact">Факт</option>
            <option value="project">Проєкт</option>
            <option value="constraint">Обмеження</option>
          </select>
        </label>

        <label>
          Зміст
          <textarea v-model="memoryForm.content" rows="5" placeholder="Що NOVA має памʼятати?" />
        </label>

        <div class="form-grid">
          <label>
            Впевненість
            <input
              v-model.number="memoryForm.confidence"
              type="range"
              min="0"
              max="1"
              step="0.05"
            />
          </label>
          <span class="range-value">{{ Math.round(memoryForm.confidence * 100) }}%</span>
        </div>

        <label>
          Проєкт
          <input v-model="memoryForm.projectKey" type="text" placeholder="nova, tsell..." />
        </label>

        <label>
          Діє до
          <input v-model="memoryForm.expiresAt" type="datetime-local" />
        </label>

        <div class="button-row">
          <button type="submit" :disabled="!memoryForm.content.trim() || memorySaving">
            {{ editingMemoryId ? 'Зберегти' : 'Додати' }}
          </button>
          <button
            v-if="editingMemoryId"
            class="ghost-button"
            type="button"
            @click="resetMemoryForm"
          >
            Скасувати
          </button>
        </div>
      </form>

      <section class="work-panel list-panel" aria-label="Список памʼяті">
        <div class="toolbar">
          <div class="panel-title">
            <p>Активна памʼять</p>
            <h1>{{ memories.length }} записів</h1>
          </div>
          <div class="search-box">
            <input
              v-model="memorySearch"
              type="search"
              placeholder="Пошук"
              @keyup.enter="loadMemories"
            />
            <button class="ghost-button" type="button" @click="loadMemories">Знайти</button>
          </div>
        </div>

        <div class="record-list">
          <article v-for="memory in memories" :key="memory.id" class="record">
            <div>
              <span class="tag">{{ memoryKindLabel(memory.kind) }}</span>
              <p>{{ memory.content }}</p>
              <small>
                {{ Math.round(memory.confidence * 100) }}% · оновлено
                {{ formatDate(memory.updatedAt) }}
              </small>
            </div>
            <div class="record-actions">
              <button class="ghost-button" type="button" @click="editMemory(memory)">
                Редагувати
              </button>
              <button class="danger-button" type="button" @click="deleteMemory(memory)">
                Видалити
              </button>
            </div>
          </article>
          <div v-if="!memories.length" class="empty-state">Записів памʼяті немає.</div>
        </div>
      </section>
    </section>

    <section v-else class="workspace two-column tasks-grid">
      <section class="work-panel editor-panel" aria-label="Задачі">
        <div class="panel-title">
          <p>Задачі</p>
          <h1>{{ editingTaskId ? 'Редагування' : 'Нова задача' }}</h1>
        </div>

        <form class="stack-form" @submit.prevent="saveTask">
          <label>
            Назва
            <input v-model="taskForm.title" type="text" placeholder="Що потрібно зробити?" />
          </label>
          <label>
            Деталі
            <textarea
              v-model="taskForm.details"
              rows="4"
              placeholder="Контекст, посилання, нотатки"
            />
          </label>
          <label>
            Дедлайн
            <input v-model="taskForm.dueAt" type="datetime-local" />
          </label>
          <div class="button-row">
            <button type="submit" :disabled="!taskForm.title.trim() || taskSaving">
              {{ editingTaskId ? 'Зберегти' : 'Додати' }}
            </button>
            <button v-if="editingTaskId" class="ghost-button" type="button" @click="resetTaskForm">
              Скасувати
            </button>
          </div>
        </form>

        <div class="record-list compact">
          <article v-for="task in tasks" :key="task.id" class="record" :data-status="task.status">
            <div>
              <span class="tag">{{ taskLabels[task.status] }}</span>
              <p>{{ task.title }}</p>
              <small>{{ task.dueAt ? formatDate(task.dueAt) : 'Без дедлайну' }}</small>
            </div>
            <div class="record-actions">
              <button
                v-if="task.status !== 'done'"
                class="ghost-button"
                type="button"
                @click="setTaskStatus(task, 'done')"
              >
                Готово
              </button>
              <button class="ghost-button" type="button" @click="editTask(task)">Редагувати</button>
              <button class="danger-button" type="button" @click="cancelTask(task)">
                Скасувати
              </button>
            </div>
          </article>
          <div v-if="!tasks.length" class="empty-state">Задач немає.</div>
        </div>
      </section>

      <section class="work-panel editor-panel" aria-label="Нагадування">
        <div class="panel-title">
          <p>Нагадування</p>
          <h1>{{ editingReminderId ? 'Редагування' : 'Новий час' }}</h1>
        </div>

        <form class="stack-form" @submit.prevent="saveReminder">
          <label>
            Текст
            <input v-model="reminderForm.title" type="text" placeholder="Про що нагадати?" />
          </label>
          <label>
            Коли
            <input v-model="reminderForm.triggerAt" type="datetime-local" />
          </label>
          <div class="form-grid two">
            <label>
              Пріоритет
              <select v-model="reminderForm.priority">
                <option value="low">Низький</option>
                <option value="normal">Звичайний</option>
                <option value="high">Високий</option>
              </select>
            </label>
            <label>
              Канал
              <select v-model="reminderForm.deliveryMethod">
                <option value="web">Web</option>
                <option value="telegram">Telegram</option>
              </select>
            </label>
          </div>
          <div class="button-row">
            <button
              type="submit"
              :disabled="!reminderForm.title.trim() || !reminderForm.triggerAt || reminderSaving"
            >
              {{ editingReminderId ? 'Зберегти' : 'Запланувати' }}
            </button>
            <button
              v-if="editingReminderId"
              class="ghost-button"
              type="button"
              @click="resetReminderForm"
            >
              Скасувати
            </button>
          </div>
        </form>

        <div class="record-list compact">
          <article
            v-for="reminder in reminders"
            :key="reminder.id"
            class="record"
            :data-status="reminder.status"
          >
            <div>
              <span class="tag">{{ reminderLabels[reminder.status] }}</span>
              <p>{{ reminder.title }}</p>
              <small>
                {{ formatDate(reminder.triggerAt) }} · {{ priorityLabels[reminder.priority] }} ·
                {{ deliveryMethodLabel(reminder.deliveryMethod) }}
              </small>
            </div>
            <div class="record-actions">
              <button class="ghost-button" type="button" @click="editReminder(reminder)">
                Редагувати
              </button>
              <button class="danger-button" type="button" @click="cancelReminder(reminder)">
                Скасувати
              </button>
            </div>
          </article>
          <div v-if="!reminders.length" class="empty-state">Нагадувань немає.</div>
        </div>
      </section>
    </section>
  </main>
</template>
