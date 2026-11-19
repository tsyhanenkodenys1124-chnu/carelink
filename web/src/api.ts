// Клієнт HTTP для API «Догляд».
// Помічник працює за cookie-сесією та токеном CSRF, пристрій підопічного — за токеном пристрою.

const base = import.meta.env.VITE_API_URL ?? '/api';

const DEVICE_TOKEN_KEY = 'doglyad_device_token';
const MARK_QUEUE_KEY = 'doglyad_mark_queue';

let csrfToken = '';

/** Помилка API з кодом відповіді, щоб інтерфейс міг розрізняти випадки. */
export class ApiError extends Error {
  readonly status: number;

  constructor(message: string, status: number) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
  }
}

async function parseBody(response: Response): Promise<any> {
  if (response.status === 204) return undefined;
  const text = await response.text();
  if (!text) return undefined;
  try {
    return JSON.parse(text);
  } catch {
    throw new ApiError('Сервер повернув неочікувану відповідь.', response.status);
  }
}

/** Запит від імені помічника. */
export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const method = (init.method ?? 'GET').toUpperCase();
  const headers = new Headers(init.headers);
  if (init.body && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json');
  if (csrfToken && !['GET', 'HEAD', 'OPTIONS'].includes(method)) headers.set('X-CSRF-Token', csrfToken);

  let response: Response;
  try {
    response = await fetch(base + path, { credentials: 'include', ...init, headers });
  } catch {
    throw new ApiError('Немає зв’язку з сервером. Перевірте інтернет.', 0);
  }
  const body = await parseBody(response);
  if (!response.ok) {
    throw new ApiError(body?.error ?? 'Сталася помилка.', response.status);
  }
  if (body && typeof body === 'object' && 'csrfToken' in body) {
    csrfToken = (body as { csrfToken: string }).csrfToken;
  }
  return body as T;
}

/** Завантаження файлу від імені помічника. */
export async function upload<T>(path: string, file: File): Promise<T> {
  const form = new FormData();
  form.append('photo', file);
  const headers = new Headers();
  if (csrfToken) headers.set('X-CSRF-Token', csrfToken);
  let response: Response;
  try {
    response = await fetch(base + path, { method: 'POST', credentials: 'include', headers, body: form });
  } catch {
    throw new ApiError('Немає зв’язку з сервером.', 0);
  }
  const body = await parseBody(response);
  if (!response.ok) throw new ApiError(body?.error ?? 'Не вдалося завантажити файл.', response.status);
  return body as T;
}

export function deviceToken(): string | null {
  return localStorage.getItem(DEVICE_TOKEN_KEY);
}

export function saveDeviceToken(token: string): void {
  localStorage.setItem(DEVICE_TOKEN_KEY, token);
}

export function forgetDevice(): void {
  localStorage.removeItem(DEVICE_TOKEN_KEY);
  localStorage.removeItem(MARK_QUEUE_KEY);
}

/** Запит від імені пристрою підопічного. */
export async function deviceApi<T>(path: string, init: RequestInit = {}): Promise<T> {
  const token = deviceToken();
  if (!token) throw new ApiError('Пристрій не підключено.', 401);
  const headers = new Headers(init.headers);
  headers.set('X-Device-Token', token);
  if (init.body && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json');

  let response: Response;
  try {
    response = await fetch(base + path, { ...init, headers });
  } catch {
    throw new ApiError('Немає зв’язку з сервером.', 0);
  }
  const body = await parseBody(response);
  if (!response.ok) throw new ApiError(body?.error ?? 'Сталася помилка.', response.status);
  return body as T;
}

/**
 * pinApi — запити режиму помічника на пристрої підопічного.
 *
 * Токен PIN живе лише в пам'яті вкладки й навмисно не потрапляє в localStorage:
 * планшет лишається в чужій квартирі, і закритий режим має лишатися закритим
 * після перезавантаження сторінки.
 */
let pinToken = '';

export function setPINToken(token: string): void {
  pinToken = token;
}

export function hasPINSession(): boolean {
  return pinToken !== '';
}

export async function pinApi<T>(path: string, init: RequestInit = {}): Promise<T> {
  if (!pinToken) throw new ApiError('Режим помічника не відкрито.', 401);
  const headers = new Headers(init.headers);
  headers.set('X-PIN-Token', pinToken);
  if (init.body && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json');

  let response: Response;
  try {
    response = await fetch(base + path, { ...init, headers });
  } catch {
    throw new ApiError('Немає зв’язку з сервером.', 0);
  }
  const body = await parseBody(response);
  if (!response.ok) {
    if (response.status === 401) pinToken = '';
    throw new ApiError(body?.error ?? 'Сталася помилка.', response.status);
  }
  return body as T;
}

/** Огляд режиму помічника: профіль, справи дня та відкриті сигнали. */
export type PINOverview = {
  ward: { id: string; name: string; timezone: string };
  occurrences: Occurrence[];
  signals: Signal[];
};

type QueuedMark = { body: string; createdAt: string };

function readQueue(): QueuedMark[] {
  try {
    const raw = localStorage.getItem(MARK_QUEUE_KEY);
    return raw ? (JSON.parse(raw) as QueuedMark[]) : [];
  } catch {
    return [];
  }
}

function writeQueue(queue: QueuedMark[]): void {
  localStorage.setItem(MARK_QUEUE_KEY, JSON.stringify(queue));
}

/** Кількість відміток, які чекають на зв’язок. */
export function pendingMarks(): number {
  return readQueue().length;
}

/**
 * Відмітка виконання з пристрою. Якщо зв’язку немає, відмітка зберігається
 * на пристрої та надсилається пізніше. Ключ ідемпотентності гарантує,
 * що повторна відправка не створить другий запис.
 */
export async function markOccurrence(occurrenceID: string): Promise<{ message: string; undoUntil?: string }> {
  const payload = JSON.stringify({
    occurrenceID,
    idempotencyKey: crypto.randomUUID(),
    deviceTime: new Date().toISOString(),
  });
  try {
    return await deviceApi<{ message: string; undoUntil?: string }>('/device/mark', {
      method: 'POST',
      body: payload,
    });
  } catch (error) {
    if (error instanceof ApiError && error.status === 0) {
      const queue = readQueue();
      queue.push({ body: payload, createdAt: new Date().toISOString() });
      writeQueue(queue);
      return { message: 'Відмітку збережено на пристрої. Вона надійде, коли з’явиться зв’язок.' };
    }
    throw error;
  }
}

/** Надсилає накопичені офлайн-відмітки. */
export async function syncOfflineMarks(): Promise<number> {
  const token = deviceToken();
  const queue = readQueue();
  if (!token || queue.length === 0) return 0;

  const remaining: QueuedMark[] = [];
  let delivered = 0;
  for (const entry of queue) {
    try {
      const response = await fetch(base + '/device/mark', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-Device-Token': token },
        body: entry.body,
      });
      // 409 означає, що ліки вже відмічено: повторювати не треба.
      if (response.ok || response.status === 409) {
        delivered += 1;
      } else if (response.status >= 500) {
        remaining.push(entry);
      } else {
        delivered += 1;
      }
    } catch {
      remaining.push(entry);
    }
  }
  writeQueue(remaining);
  return delivered;
}

// --- Типи даних ---

export type Rights = {
  view: boolean;
  editTasks: boolean;
  editSettings: boolean;
  escalations: boolean;
  primary: boolean;
};

export type OpenSignals = { help: number; unsure: number; escalations: number; total: number };

export type Ward = {
  id: string;
  addressName: string;
  timezone: string;
  photo: string;
  emergencyPhone: string;
  rights: Rights;
  openSignals: OpenSignals;
};

export type Me = {
  id: string;
  email: string;
  name: string;
  timezone: string;
  phone: string;
  emailConfirmed: boolean;
  telegramLinked: boolean;
  pushDevices: number;
  csrfToken: string;
};

export type Occurrence = {
  id: string;
  taskID: string;
  title: string;
  category: string;
  description: string;
  scheduledAt: string;
  /** openFrom — мить, з якої справу вже можна відмітити. */
  openFrom: string;
  deadline: string;
  status: 'planned' | 'done' | 'missed';
  completedAt: string;
  critical: boolean;
  markedBy: string;
  undoUntil: string;
};

export type DeviceOccurrence = Occurrence & { steps: string[]; photo: string };

export type Task = {
  id: string;
  title: string;
  category: string;
  description: string;
  critical: boolean;
  localStartDate: string;
  localTime: string;
  timezone: string;
  recurrenceKind: 'once' | 'daily' | 'weekly' | 'interval';
  recurrenceDays: number[];
  recurrenceInterval: number;
  completionWindowMinutes: number;
  escalationWindowMinutes: number;
  warningMinutes: number;
  reminderRepeats: number;
  reminderIntervalMinutes: number;
  photo: string;
  steps: string[];
};

export type Signal = {
  id: string;
  type: 'допомога' | 'непевність' | 'ескалація';
  wardId: string;
  wardName: string;
  title: string;
  message: string;
  status: string;
  createdAt: string;
  occurrenceID?: string;
  takenBy?: string;
  acknowledged: boolean;
};

/** Одне правило адаптації, що спрацювало: що змінилося і через яку відповідь. */
export type AdaptationChange = {
  setting: string;
  value: string;
  reason: string;
};

export type AccessibilitySettings = {
  scale: number;
  palette: 'standard' | 'high-contrast' | 'color-safe';
  magnifier: boolean;
  magnifierScale: number;
  largeControls: boolean;
  dyslexicFont: boolean;
  letterSpacing: string;
  wordSpacing: string;
  lineHeight: string;
  dementiaMode: boolean;
  speechEnabled: boolean;
  speechRate: number;
};

export type WardScreenData = {
  ward: { name: string; timezone: string; phone: string; photo: string; wakeLock: boolean };
  settings: AccessibilitySettings;
  occurrences: DeviceOccurrence[];
  helperAnswer: string;
  helpRequestOpen: boolean;
  serverTime: string;
};

export type Suggestion = {
  id: string;
  taskID: string;
  taskTitle: string;
  rule: string;
  status: string;
  explanation: string;
  humanProposal: string;
  createdAt: string;
  decidedAt: string;
  decidedBy: string;
};

/** Показники, за якими перевіряється, чи застосунок досягає своєї мети. */
export type ResearchMetrics = {
  medianHelpResponseSeconds: number;
  medianEscalationResponseSeconds: number;
  unansweredSignals: number;
  blockedDoubleDoses: number;
  blockedEarlyMarks: number;
  completedFirstTry: number;
  completedWithCorrection: number;
  suggestionsAccepted: number;
  suggestionsDeclined: number;
  suggestionsPending: number;
};

export type ReportSummary = {
  from: string;
  to: string;
  total: number;
  done: number;
  missed: number;
  planned: number;
  timelyPercent: number;
  escalations: number;
  helpRequests: number;
  byCategory: Record<string, number>;
  metrics: ResearchMetrics;
};

export type CareLink = {
  id: string;
  helperID: string;
  name: string;
  email: string;
  createdAt: string;
  rights: Rights;
};

export type Invitation = {
  id: string;
  email: string;
  expiresAt: string;
  createdAt: string;
  rights: Rights;
};

export type DeviceRecord = {
  id: string;
  label: string;
  lastActiveAt: string;
  createdAt: string;
  revokedAt: string;
  active: boolean;
};

export type NotificationPref = {
  channel: 'push' | 'telegram' | 'email';
  enabled: boolean;
  quietStart: string;
  quietEnd: string;
};

export type SessionRecord = {
  id: string;
  createdAt: string;
  lastSeenAt: string;
  userAgent: string;
};

export type AuditEntry = {
  id: string;
  action: string;
  entity: string;
  entityID: string;
  actor: string;
  createdAt: string;
};

export type HelpRequest = {
  id: string;
  message: string;
  status: string;
  createdAt: string;
  closedAt: string;
  closedBy: string;
  acknowledgements: number;
};
