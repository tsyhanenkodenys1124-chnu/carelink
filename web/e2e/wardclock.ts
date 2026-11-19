// Час за годинником підопічного для наскрізних перевірок.
//
// Справу можна відмітити лише з початку її вікна, тож тести, які натискають
// головну кнопку, мають створювати завдання на час, що вже настав. Константа
// на кшталт «08:00» працювала б лише вранці, тому час рахується від поточного.

const WARD_TIMEZONE = 'Europe/Kyiv';

/** parts повертає складові поточного моменту за годинником підопічного. */
function parts(at: Date): Record<string, string> {
  const formatter = new Intl.DateTimeFormat('uk-UA', {
    timeZone: WARD_TIMEZONE,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  });
  const out: Record<string, string> = {};
  for (const part of formatter.formatToParts(at)) out[part.type] = part.value;
  return out;
}

/**
 * dueNow повертає дату та час завдання, вікно якого щойно відкрилося.
 * Зсув у п'ять хвилин назад лишає запас до кінця вікна виконання.
 */
export function dueNow(): { date: string; time: string } {
  const at = new Date(Date.now() - 5 * 60 * 1000);
  const p = parts(at);
  return { date: `${p.year}-${p.month}-${p.day}`, time: `${p.hour}:${p.minute}` };
}

/** laterToday повертає час за кілька годин попереду — справа ще не настала. */
export function laterToday(): { date: string; time: string } {
  const at = new Date(Date.now() + 5 * 60 * 60 * 1000);
  const p = parts(at);
  return { date: `${p.year}-${p.month}-${p.day}`, time: `${p.hour}:${p.minute}` };
}
