// Журнал дій за профілем підопічного. Доступний лише головному помічникові.

import { useEffect, useState } from 'react';
import { ApiError, AuditEntry, Ward, api } from '../api';
import { formatDateTime } from '../format';
import { EmptyState, ErrorText, Loading } from '../components/ui';

export function AuditPanel({ ward }: { ward: Ward }) {
  const [entries, setEntries] = useState<AuditEntry[] | null>(null);
  const [error, setError] = useState('');

  useEffect(() => {
    let cancelled = false;
    setEntries(null);
    setError('');
    void api<AuditEntry[]>(`/audit?wardId=${ward.id}`)
      .then((list) => !cancelled && setEntries(list))
      .catch((cause: unknown) => {
        if (cancelled) return;
        setError(cause instanceof ApiError ? cause.message : 'Не вдалося отримати журнал.');
        setEntries([]);
      });
    return () => {
      cancelled = true;
    };
  }, [ward.id]);

  if (!ward.rights.primary) {
    return (
      <EmptyState
        title="Журнал бачить головний помічник"
        hint="Це зроблено навмисно: журнал показує, хто і коли змінював налаштування та відмічав справи."
      />
    );
  }

  if (!entries) return <Loading what="журнал" />;

  return (
    <section className="panel">
      <div className="panel-head">
        <div>
          <h2>Журнал дій</h2>
          <p>Останні 200 записів: хто, що і коли змінив.</p>
        </div>
      </div>

      <ErrorText>{error}</ErrorText>

      {entries.length === 0 ? (
        <EmptyState title="Записів ще немає" hint="Тут з'являться зміни налаштувань, відмітки та підключення пристроїв." />
      ) : (
        <ul className="row-list">
          {entries.map((entry) => (
            <li key={entry.id} className="row">
              <time dateTime={entry.createdAt}>{formatDateTime(entry.createdAt, ward.timezone)}</time>
              <div className="row-main">
                <strong>
                  {entry.action} · {entry.entity}
                </strong>
                <small>{entry.actor}</small>
              </div>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
