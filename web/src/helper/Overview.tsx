// Огляд дня: найближча справа, лічильники та стан сигналів.

import { useEffect, useState } from 'react';
import { Occurrence, Signal, Ward, api } from '../api';
import { formatDateTime, statusNames } from '../format';
import { Loading, StatusPill } from '../components/ui';

export function Overview({
  ward,
  signals,
  onOpenSignals,
}: {
  ward: Ward;
  signals: Signal[];
  onOpenSignals: () => void;
}) {
  const [items, setItems] = useState<Occurrence[] | null>(null);

  useEffect(() => {
    let cancelled = false;
    setItems(null);
    void api<Occurrence[]>(`/occurrences?wardId=${ward.id}`)
      .then((list) => !cancelled && setItems(list))
      .catch(() => !cancelled && setItems([]));
    return () => {
      cancelled = true;
    };
  }, [ward.id]);

  if (!items) return <Loading what="розклад" />;

  const now = Date.now();
  const planned = items.filter((item) => item.status === 'planned');
  const next = planned.find((item) => new Date(item.scheduledAt).getTime() >= now) ?? planned[0];
  const done = items.filter((item) => item.status === 'done').length;
  const missed = items.filter((item) => item.status === 'missed').length;
  const wardSignals = signals.filter((item) => item.wardId === ward.id);

  return (
    <section className="panel">
      <div className="cards">
        <article className="card card-hero">
          <p className="card-label">Найближча справа</p>
          <h2>{next ? next.title : 'На найближчий час справ немає'}</h2>
          {next && (
            <>
              <p className="card-meta">{formatDateTime(next.scheduledAt, ward.timezone)}</p>
              {next.critical && <span className="tag tag-critical">Критична справа</span>}
            </>
          )}
        </article>

        <article className="card">
          <p className="card-label">Заплановано</p>
          <h2>{planned.length}</h2>
        </article>
        <article className="card">
          <p className="card-label">Виконано</p>
          <h2>{done}</h2>
        </article>
        <article className={missed > 0 ? 'card card-warning' : 'card'}>
          <p className="card-label">Пропущено</p>
          <h2>{missed}</h2>
        </article>
      </div>

      <article className={wardSignals.length > 0 ? 'card card-alert' : 'card'}>
        <p className="card-label">Сигнали від підопічного</p>
        {wardSignals.length === 0 ? (
          <p>Відкритих сигналів немає. Запит про допомогу з’явиться тут одразу.</p>
        ) : (
          <>
            <ul className="plain-list">
              {wardSignals.map((signal) => (
                <li key={signal.id}>
                  <strong>{signal.title}</strong>
                  {signal.message ? ` — ${signal.message}` : ''}
                </li>
              ))}
            </ul>
            <button type="button" className="primary" onClick={onOpenSignals}>
              Перейти до сигналів
            </button>
          </>
        )}
      </article>

      <section>
        <h3>Найближчі справи</h3>
        <ul className="row-list">
          {items.slice(0, 8).map((item) => (
            <li key={item.id} className="row">
              <time dateTime={item.scheduledAt}>{formatDateTime(item.scheduledAt, ward.timezone)}</time>
              <div className="row-main">
                <strong>{item.title}</strong>
                <small>
                  {statusNames[item.status]}
                  {item.markedBy ? ` · відмітив ${item.markedBy}` : ''}
                </small>
              </div>
              <StatusPill status={item.status} />
            </li>
          ))}
        </ul>
      </section>
    </section>
  );
}
