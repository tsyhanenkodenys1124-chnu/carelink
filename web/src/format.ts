// Форматування дат і назв українською.
//
// Час завжди показуємо в часовому поясі підопічного: помічник може бути в іншій
// країні, але «ранкові ліки о 08:00» означають 08:00 там, де живе підопічний.

const cache = new Map<string, Intl.DateTimeFormat>();

function formatter(key: string, options: Intl.DateTimeFormatOptions, timeZone?: string): Intl.DateTimeFormat {
  const id = `${key}|${timeZone ?? 'local'}`;
  let found = cache.get(id);
  if (!found) {
    found = new Intl.DateTimeFormat('uk-UA', timeZone ? { ...options, timeZone } : options);
    cache.set(id, found);
  }
  return found;
}

function parse(value: string): Date | null {
  if (!value) return null;
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? null : parsed;
}

/** «27 вересня о 14:30» у часовому поясі підопічного. */
export function formatDateTime(value: string, timeZone?: string): string {
  const parsed = parse(value);
  if (!parsed) return '';
  const day = formatter('day', { day: 'numeric', month: 'long' }, timeZone).format(parsed);
  const clock = formatter('clock', { hour: '2-digit', minute: '2-digit' }, timeZone).format(parsed);
  return `${day} о ${clock}`;
}

/** «14:30» */
export function formatClock(value: string, timeZone?: string): string {
  const parsed = parse(value);
  if (!parsed) return '';
  return formatter('clock', { hour: '2-digit', minute: '2-digit' }, timeZone).format(parsed);
}

/**
 * «неділя, 27 вересня» — день тижня в називному відмінку.
 * Окремий виклик потрібен саме тому, що спільний формат дає «неділю».
 */
export function formatWeekday(date: Date, timeZone?: string): string {
  const weekday = formatter('weekday', { weekday: 'long' }, timeZone).format(date);
  const day = formatter('day', { day: 'numeric', month: 'long' }, timeZone).format(date);
  return `${weekday}, ${day}`;
}

/** «щойно», «5 хв тому», інакше повна дата. */
export function formatAgo(value: string, timeZone?: string): string {
  const parsed = parse(value);
  if (!parsed) return '';
  const minutes = Math.round((Date.now() - parsed.getTime()) / 60000);
  if (minutes < 1) return 'щойно';
  if (minutes < 60) return `${minutes} хв тому`;
  if (minutes < 60 * 12) return `${Math.round(minutes / 60)} год тому`;
  return formatDateTime(value, timeZone);
}

export const categoryNames: Record<string, string> = {
  medicine: 'Ліки',
  procedure: 'Процедура',
  visit: 'Візит',
  food: 'Їжа',
  other: 'Інше',
};

export const statusNames: Record<string, string> = {
  planned: 'Заплановано',
  done: 'Виконано',
  missed: 'Пропущено',
};

export const weekdayNames = ['Нд', 'Пн', 'Вт', 'Ср', 'Чт', 'Пт', 'Сб'];

/** Текст головної кнопки залежно від категорії справи. */
export function actionLabel(category: string): string {
  return category === 'medicine' ? 'Прийняв ліки' : 'Виконано';
}

/** Скільки секунд лишилося до кінця вікна скасування. */
export function secondsLeft(until: string): number {
  const parsed = parse(until);
  if (!parsed) return 0;
  return Math.max(0, Math.round((parsed.getTime() - Date.now()) / 1000));
}
