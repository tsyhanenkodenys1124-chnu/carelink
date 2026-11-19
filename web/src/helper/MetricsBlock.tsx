// Показники дослідження: чи застосунок справді робить те, заради чого створений.

import { ResearchMetrics } from '../api';

/** formatDuration перетворює секунди у зрозумілий запис часу реакції. */
function formatDuration(seconds: number): string {
  if (seconds <= 0) return '—';
  if (seconds < 60) return `${seconds} с`;
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes} хв`;
  const hours = Math.floor(minutes / 60);
  const rest = minutes % 60;
  return rest === 0 ? `${hours} год` : `${hours} год ${rest} хв`;
}

/** percentOf рахує частку у відсотках без ділення на нуль. */
function percentOf(part: number, total: number): number {
  return total === 0 ? 0 : Math.round((part / total) * 100);
}

export function MetricsBlock({ metrics }: { metrics: ResearchMetrics }) {
  const completions = metrics.completedFirstTry + metrics.completedWithCorrection;
  const decided = metrics.suggestionsAccepted + metrics.suggestionsDeclined;

  const rows = [
    {
      title: 'Реакція на «Потрібна допомога»',
      value: formatDuration(metrics.medianHelpResponseSeconds),
      hint: 'Медіанний час від натискання кнопки до відповіді помічника.',
    },
    {
      title: 'Реакція на ескалацію',
      value: formatDuration(metrics.medianEscalationResponseSeconds),
      hint: 'Медіанний час від появи сигналу про невиконану критичну справу до реакції.',
    },
    {
      title: 'Сигнали без реакції',
      value: String(metrics.unansweredSignals),
      hint: 'Скільки сигналів лишилися без жодної відповіді. Має прагнути нуля.',
      warning: metrics.unansweredSignals > 0,
    },
    {
      title: 'Заблоковано повторних приймань',
      value: String(metrics.blockedDoubleDoses),
      hint: 'Скільки разів застосунок не дав прийняти ті самі ліки вдруге.',
    },
    {
      title: 'Заблоковано завчасних відміток',
      value: String(metrics.blockedEarlyMarks),
      hint: 'Скільки разів застосунок не дав відмітити справу раніше за її час.',
    },
    {
      title: 'Виконано з першого разу',
      value: completions === 0 ? '—' : `${percentOf(metrics.completedFirstTry, completions)}%`,
      hint: `Без скасувань і питань «чи я вже приймав». Усього виконань: ${completions}.`,
    },
    {
      title: 'Поради прийнято',
      value: decided === 0 ? '—' : `${percentOf(metrics.suggestionsAccepted, decided)}%`,
      hint:
        decided === 0
          ? 'Поки що рішень за порадами не було.'
          : `Прийнято ${metrics.suggestionsAccepted}, відхилено ${metrics.suggestionsDeclined}. Показує, чи автопідбір влучає.`,
    },
  ];

  return (
    <section>
      <h3>Показники роботи</h3>
      <p className="field-hint">
        Ці числа показують не те, скільки справ у розкладі, а те, чи застосунок допомагає: чи швидко приходить
        допомога, чи вдається підопічному впоратися самому і чи не сталося подвійного приймання ліків.
      </p>
      <ul className="metrics-list">
        {rows.map((row) => (
          <li key={row.title} className={row.warning ? 'metric metric-warning' : 'metric'}>
            <span className="metric-value">{row.value}</span>
            <span className="metric-title">{row.title}</span>
            <small>{row.hint}</small>
          </li>
        ))}
      </ul>
    </section>
  );
}
