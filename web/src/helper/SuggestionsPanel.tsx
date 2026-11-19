// Поради застосунку. Жодна порада не застосовується сама — рішення завжди за помічником.

import { useCallback, useEffect, useState } from 'react';
import { ApiError, Suggestion, Ward, api } from '../api';
import { formatDateTime } from '../format';
import { EmptyState, ErrorText, Loading, NoticeText } from '../components/ui';

export function SuggestionsPanel({ ward }: { ward: Ward }) {
  const [items, setItems] = useState<Suggestion[] | null>(null);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [busyID, setBusyID] = useState('');

  const load = useCallback(async () => {
    try {
      setItems(await api<Suggestion[]>(`/suggestions?wardId=${ward.id}`));
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Не вдалося отримати поради.');
      setItems([]);
    }
  }, [ward.id]);

  useEffect(() => {
    setItems(null);
    void load();
  }, [load]);

  const decide = async (suggestion: Suggestion, decision: 'accepted' | 'declined' | 'deferred') => {
    setBusyID(suggestion.id);
    setError('');
    setNotice('');
    try {
      await api(`/suggestions/${suggestion.id}/decision`, {
        method: 'POST',
        body: JSON.stringify({ decision }),
      });
      setNotice(
        decision === 'accepted'
          ? 'Пораду застосовано. Зміну видно в розділі «Екран» або «Розклад».'
          : decision === 'declined'
            ? 'Пораду відхилено. Ми не повторюватимемо її місяць.'
            : 'Пораду відкладено.',
      );
      await load();
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Не вдалося зберегти рішення.');
    } finally {
      setBusyID('');
    }
  };

  if (!items) return <Loading what="поради" />;

  const open = items.filter((item) => item.status === 'created');
  const decided = items.filter((item) => item.status !== 'created');

  return (
    <section className="panel">
      <div className="panel-head">
        <div>
          <h2>Поради</h2>
          <p>
            Застосунок помічає закономірності — наприклад, що екран часто збільшують — і пропонує зміну.
            Нічого не змінюється, поки ви не погодитеся.
          </p>
        </div>
      </div>

      <ErrorText>{error}</ErrorText>
      <NoticeText>{notice}</NoticeText>

      {open.length === 0 ? (
        <EmptyState
          title="Порад поки немає"
          hint="Вони з'являються, коли назбирується достатньо спостережень: зазвичай за тиждень користування."
        />
      ) : (
        <ul className="suggestion-list">
          {open.map((suggestion) => (
            <li key={suggestion.id} className="suggestion-card">
              <p className="suggestion-why">{suggestion.explanation}</p>
              <h3>{suggestion.humanProposal}</h3>
              {suggestion.taskTitle && <p className="field-hint">Стосується справи «{suggestion.taskTitle}».</p>}
              <div className="row-actions">
                <button
                  type="button"
                  className="primary"
                  disabled={busyID === suggestion.id}
                  onClick={() => void decide(suggestion, 'accepted')}
                >
                  Прийняти
                </button>
                <button type="button" disabled={busyID === suggestion.id} onClick={() => void decide(suggestion, 'deferred')}>
                  Відкласти
                </button>
                <button
                  type="button"
                  className="danger"
                  disabled={busyID === suggestion.id}
                  onClick={() => void decide(suggestion, 'declined')}
                >
                  Не треба
                </button>
              </div>
            </li>
          ))}
        </ul>
      )}

      {decided.length > 0 && (
        <>
          <h3>Раніше</h3>
          <ul className="row-list">
            {decided.map((suggestion) => (
              <li key={suggestion.id} className="row">
                <div className="row-main">
                  <strong>{suggestion.humanProposal}</strong>
                  <small>
                    {decisionName(suggestion.status)}
                    {suggestion.decidedBy ? ` · ${suggestion.decidedBy}` : ''}
                    {suggestion.decidedAt ? ` · ${formatDateTime(suggestion.decidedAt, ward.timezone)}` : ''}
                  </small>
                </div>
              </li>
            ))}
          </ul>
        </>
      )}
    </section>
  );
}

function decisionName(status: string): string {
  switch (status) {
    case 'accepted':
      return 'прийнято';
    case 'declined':
      return 'відхилено';
    case 'deferred':
      return 'відкладено';
    default:
      return status;
  }
}
