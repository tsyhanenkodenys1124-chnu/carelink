// Звіт за проміжок часу та експорт у CSV.

import { useCallback, useEffect, useState } from 'react';
import { ApiError, ReportSummary, Ward, api } from '../api';
import { categoryNames } from '../format';
import { ErrorText, Field, Loading } from '../components/ui';
import { MetricsBlock } from './MetricsBlock';

const ranges = [
  { id: '7', label: 'Тиждень', days: 7 },
  { id: '30', label: 'Місяць', days: 30 },
  { id: '90', label: 'Три місяці', days: 90 },
];

export function ReportPanel({ ward }: { ward: Ward }) {
  const [days, setDays] = useState(7);
  const [summary, setSummary] = useState<ReportSummary | null>(null);
  const [error, setError] = useState('');

  const range = useCallback(() => {
    const to = new Date();
    const from = new Date(to.getTime() - days * 24 * 60 * 60 * 1000);
    return { from: from.toISOString(), to: to.toISOString() };
  }, [days]);

  const load = useCallback(async () => {
    setSummary(null);
    setError('');
    const { from, to } = range();
    try {
      setSummary(
        await api<ReportSummary>(
          `/reports?wardId=${ward.id}&from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`,
        ),
      );
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Не вдалося побудувати звіт.');
    }
  }, [range, ward.id]);

  useEffect(() => {
    void load();
  }, [load]);

  const exportCSV = () => {
    const { from, to } = range();
    const url = `/api/reports/export.csv?wardId=${ward.id}&from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`;
    window.open(url, '_blank', 'noopener');
  };

  return (
    <section className="panel">
      <div className="panel-head">
        <div>
          <h2>Звіт</h2>
          <p>Скільки справ виконано вчасно, скільки пропущено та як часто траплялися ескалації.</p>
        </div>
        <button type="button" onClick={exportCSV}>
          Завантажити CSV
        </button>
      </div>

      <Field label="Проміжок">
        <select value={String(days)} onChange={(event) => setDays(Number(event.target.value))}>
          {ranges.map((item) => (
            <option key={item.id} value={item.days}>
              {item.label}
            </option>
          ))}
        </select>
      </Field>

      <ErrorText>{error}</ErrorText>
      {!summary ? (
        <Loading what="звіт" />
      ) : (
        <>
          <div className="cards">
            <article className="card">
              <p className="card-label">Усього справ</p>
              <h2>{summary.total}</h2>
            </article>
            <article className="card">
              <p className="card-label">Виконано</p>
              <h2>{summary.done}</h2>
            </article>
            <article className={summary.missed > 0 ? 'card card-warning' : 'card'}>
              <p className="card-label">Пропущено</p>
              <h2>{summary.missed}</h2>
            </article>
            <article className="card">
              <p className="card-label">Вчасно</p>
              <h2>{summary.timelyPercent}%</h2>
            </article>
            <article className="card">
              <p className="card-label">Ескалацій</p>
              <h2>{summary.escalations}</h2>
            </article>
            <article className="card">
              <p className="card-label">Запитів допомоги</p>
              <h2>{summary.helpRequests}</h2>
            </article>
          </div>

          <MetricsBlock metrics={summary.metrics} />

          <h3>Виконано за категоріями</h3>
          {Object.keys(summary.byCategory).length === 0 ? (
            <p className="field-hint">За цей проміжок виконаних справ немає.</p>
          ) : (
            <ul className="bar-list">
              {Object.entries(summary.byCategory).map(([category, count]) => (
                <li key={category}>
                  <span className="bar-label">{categoryNames[category] ?? category}</span>
                  <span className="bar-track">
                    <span
                      className="bar-fill"
                      style={{ width: `${Math.round((count / Math.max(summary.done, 1)) * 100)}%` }}
                    />
                  </span>
                  <span className="bar-value">{count}</span>
                </li>
              ))}
            </ul>
          )}
        </>
      )}
    </section>
  );
}
