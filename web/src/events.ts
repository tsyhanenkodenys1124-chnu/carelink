// Живі події з сервера для панелі помічника.
// Потік SSE тримає з'єднання відкритим, тому сигнал про допомогу
// з'являється на екрані одразу, без перезавантаження сторінки.

const base = import.meta.env.VITE_API_URL ?? '/api';

export type LiveEvent = {
  type: 'допомога' | 'непевність' | 'ескалація' | 'виконання' | 'сигнал закрито';
  wardId: string;
  title: string;
  urgent: boolean;
  at: string;
  payload?: Record<string, unknown>;
};

/**
 * subscribeToEvents відкриває потік подій і повертає функцію відписки.
 * Браузер сам відновлює з'єднання, якщо воно урветься.
 */
export function subscribeToEvents(onEvent: (event: LiveEvent) => void, onStateChange?: (online: boolean) => void): () => void {
  let source: EventSource | null = null;
  let closed = false;

  const handle = (raw: MessageEvent) => {
    try {
      onEvent(JSON.parse(raw.data) as LiveEvent);
    } catch {
      // Пошкоджену подію просто пропускаємо: стан однаково оновиться за таймером.
    }
  };

  const connect = () => {
    if (closed) return;
    source = new EventSource(base + '/events', { withCredentials: true });
    source.onopen = () => onStateChange?.(true);
    source.onerror = () => onStateChange?.(false);
    for (const type of ['допомога', 'непевність', 'ескалація', 'виконання', 'сигнал закрито']) {
      source.addEventListener(type, handle as EventListener);
    }
  };

  connect();
  return () => {
    closed = true;
    source?.close();
  };
}

let audioContext: AudioContext | null = null;

/**
 * playAlert подає короткий звук для термінового сигналу.
 * Звук синтезується на місці, щоб не тягнути аудіофайл у застосунок.
 */
export function playAlert(): void {
  try {
    audioContext ??= new AudioContext();
    if (audioContext.state === 'suspended') void audioContext.resume();
    const now = audioContext.currentTime;
    const oscillator = audioContext.createOscillator();
    const gain = audioContext.createGain();
    oscillator.type = 'sine';
    oscillator.frequency.setValueAtTime(880, now);
    oscillator.frequency.setValueAtTime(660, now + 0.18);
    gain.gain.setValueAtTime(0.0001, now);
    gain.gain.exponentialRampToValueAtTime(0.25, now + 0.03);
    gain.gain.exponentialRampToValueAtTime(0.0001, now + 0.5);
    oscillator.connect(gain).connect(audioContext.destination);
    oscillator.start(now);
    oscillator.stop(now + 0.55);
  } catch {
    // Браузер може заборонити звук до першої дії користувача — це не помилка.
  }
}

/** showDesktopNotification показує системне сповіщення, якщо користувач дозволив. */
export function showDesktopNotification(title: string, body: string): void {
  if (!('Notification' in window) || Notification.permission !== 'granted') return;
  try {
    new Notification(title, { body, tag: 'doglyad-signal', renotify: true } as NotificationOptions);
  } catch {
    // Деякі браузери дозволяють сповіщення лише через service worker.
  }
}

/** askNotificationPermission просить дозвіл на системні сповіщення. */
export async function askNotificationPermission(): Promise<boolean> {
  if (!('Notification' in window)) return false;
  if (Notification.permission === 'granted') return true;
  if (Notification.permission === 'denied') return false;
  return (await Notification.requestPermission()) === 'granted';
}
