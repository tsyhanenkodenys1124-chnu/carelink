// Накопичення знеособлених сигналів зручності екрана.
//
// Збираються лише лічильники подій: скільки разів збільшували екран, вмикали лупу,
// скасовували дію, слухали озвучення або натискали «Я не пам'ятаю». Ні тексту,
// ні часу конкретних дій сервер не отримує.

import { deviceApi, deviceToken } from './api';

type Counters = {
  date: string;
  viewportZoomSessions: number;
  magnifierUses: number;
  quickUndos: number;
  speechUses: number;
  unsureUses: number;
};

const STORAGE_KEY = 'doglyad_adaptation';

function today(): string {
  return new Date().toISOString().slice(0, 10);
}

function empty(): Counters {
  return {
    date: today(),
    viewportZoomSessions: 0,
    magnifierUses: 0,
    quickUndos: 0,
    speechUses: 0,
    unsureUses: 0,
  };
}

function read(): Counters {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return empty();
    const parsed = JSON.parse(raw) as Counters;
    return parsed.date === today() ? parsed : empty();
  } catch {
    return empty();
  }
}

function write(counters: Counters): void {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(counters));
  } catch {
    // Якщо сховище недоступне, сигнали просто не збережуться.
  }
}

type CounterName = Exclude<keyof Counters, 'date'>;

/** count збільшує лічильник події на пристрої. */
export function count(name: CounterName, amount = 1): void {
  const counters = read();
  counters[name] += amount;
  write(counters);
}

/** flushAdaptationSignals надсилає накопичені лічильники та обнуляє їх. */
export async function flushAdaptationSignals(): Promise<void> {
  if (!deviceToken()) return;
  const counters = read();
  const sum =
    counters.viewportZoomSessions +
    counters.magnifierUses +
    counters.quickUndos +
    counters.speechUses +
    counters.unsureUses;
  if (sum === 0) return;
  try {
    await deviceApi('/device/signals', { method: 'POST', body: JSON.stringify(counters) });
    write(empty());
  } catch {
    // Надішлемо наступного разу.
  }
}

/** watchViewportZoom помічає, що екран масштабували жестом. */
export function watchViewportZoom(): () => void {
  if (!window.visualViewport) return () => undefined;
  let counted = false;
  const handler = () => {
    const scale = window.visualViewport?.scale ?? 1;
    if (scale > 1.2 && !counted) {
      counted = true;
      count('viewportZoomSessions');
    }
    if (scale <= 1.05) counted = false;
  };
  window.visualViewport.addEventListener('resize', handler);
  return () => window.visualViewport?.removeEventListener('resize', handler);
}
