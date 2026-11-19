// Живі сигнали від підопічних: «Потрібна допомога», «Я не пам'ятаю» та ескалації.
// Панель показує їх постійно, тому помічник бачить запит одразу, а не лише в пошті.

import { useState } from 'react';
import { ApiError, Signal, api } from '../api';
import { formatAgo, formatDateTime } from '../format';
import { Dialog, EmptyState, ErrorText, Field } from '../components/ui';

export function SignalsPanel({ signals, onChanged }: { signals: Signal[]; onChanged: () => void }) {
  const [error, setError] = useState('');
  const [busyID, setBusyID] = useState('');
  const [answering, setAnswering] = useState<Signal | null>(null);
  const [closing, setClosing] = useState<Signal | null>(null);

  const act = async (signal: Signal, path: string, body?: unknown) => {
    setBusyID(signal.id);
    setError('');
    try {
      await api(path, { method: 'POST', body: body ? JSON.stringify(body) : '{}' });
      onChanged();
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Не вдалося виконати дію.');
    } finally {
      setBusyID('');
    }
  };

  if (signals.length === 0) {
    return (
      <EmptyState
        title="Відкритих сигналів немає"
        hint="Тут з'являться запити «Потрібна допомога», питання «Я не пам'ятаю» та ескалації критичних справ. Сигнал приходить одразу, зі звуком."
      />
    );
  }

  return (
    <section className="panel">
      <div className="panel-head">
        <div>
          <h2>Відкриті сигнали</h2>
          <p>Сигнали надходять одразу. Візьміть запит у роботу, щоб інші помічники бачили, що ви ним займаєтеся.</p>
        </div>
      </div>

      <ErrorText>{error}</ErrorText>

      <ul className="signal-list">
        {signals.map((signal) => (
          <li key={signal.id} className={`signal-card signal-${signalKind(signal.type)}`}>
            <div className="signal-main">
              <p className="signal-meta">
                <span className="signal-ward">{signal.wardName}</span>
                <time dateTime={signal.createdAt} title={formatDateTime(signal.createdAt)}>
                  {formatAgo(signal.createdAt)}
                </time>
              </p>
              <h3>{signal.title}</h3>
              {signal.message && <p className="signal-message">{signal.message}</p>}
              {signal.takenBy && <p className="signal-taken">У роботі: {signal.takenBy}</p>}
              {signal.acknowledged && <p className="signal-taken">Ви взяли цей запит у роботу.</p>}
            </div>

            <div className="signal-actions">
              {signal.type === 'допомога' && (
                <>
                  {!signal.acknowledged && (
                    <button
                      type="button"
                      onClick={() => void act(signal, `/help-requests/${signal.id}/ack`)}
                      disabled={busyID === signal.id}
                    >
                      Беру на себе
                    </button>
                  )}
                  <button
                    type="button"
                    className="primary"
                    onClick={() => void act(signal, `/help-requests/${signal.id}/close`)}
                    disabled={busyID === signal.id}
                  >
                    Питання вирішено
                  </button>
                </>
              )}

              {signal.type === 'непевність' && (
                <button type="button" className="primary" onClick={() => setAnswering(signal)}>
                  Відповісти
                </button>
              )}

              {signal.type === 'ескалація' && (
                <>
                  {signal.status === 'open' && (
                    <button
                      type="button"
                      onClick={() => void act(signal, `/escalations/${signal.id}/take`)}
                      disabled={busyID === signal.id}
                    >
                      Беру на себе
                    </button>
                  )}
                  <button type="button" className="primary" onClick={() => setClosing(signal)}>
                    Закрити
                  </button>
                </>
              )}
            </div>
          </li>
        ))}
      </ul>

      {answering && (
        <Dialog title="Відповідь підопічному" onClose={() => setAnswering(null)}>
          <UnsureAnswerForm
            signal={answering}
            onDone={() => {
              setAnswering(null);
              onChanged();
            }}
          />
        </Dialog>
      )}

      {closing && (
        <Dialog title="Закрити ескалацію" onClose={() => setClosing(null)}>
          <CloseEscalationForm
            signal={closing}
            onDone={() => {
              setClosing(null);
              onChanged();
            }}
          />
        </Dialog>
      )}
    </section>
  );
}

function UnsureAnswerForm({ signal, onDone }: { signal: Signal; onDone: () => void }) {
  const [answer, setAnswer] = useState('');
  const [markAsDone, setMarkAsDone] = useState(true);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const submit = async () => {
    setBusy(true);
    setError('');
    try {
      await api(`/unsure-requests/${signal.id}/answer`, {
        method: 'POST',
        body: JSON.stringify({ answer, markAsDone }),
      });
      onDone();
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Не вдалося надіслати відповідь.');
    } finally {
      setBusy(false);
    }
  };

  const suggestions = [
    'Так, ви вже прийняли. Більше не треба.',
    'Ще не приймали. Прийміть зараз, будь ласка.',
    'Я скоро буду і допоможу.',
  ];

  return (
    <div className="stack">
      <p>
        Питання про справу: <strong>{signal.message}</strong>
      </p>
      <ErrorText>{error}</ErrorText>
      <div className="quick-answers">
        {suggestions.map((text) => (
          <button type="button" key={text} onClick={() => setAnswer(text)}>
            {text}
          </button>
        ))}
      </div>
      <Field label="Відповідь великим текстом на екрані підопічного">
        <textarea rows={3} value={answer} onChange={(event) => setAnswer(event.target.value)} maxLength={200} />
      </Field>
      <label className="checkbox">
        <input type="checkbox" checked={markAsDone} onChange={(event) => setMarkAsDone(event.target.checked)} />
        <span>Одночасно відмітити справу виконаною</span>
      </label>
      <button type="button" className="primary" onClick={() => void submit()} disabled={busy || !answer.trim()}>
        {busy ? 'Надсилаємо…' : 'Надіслати відповідь'}
      </button>
    </div>
  );
}

function CloseEscalationForm({ signal, onDone }: { signal: Signal; onDone: () => void }) {
  const [comment, setComment] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const submit = async () => {
    setBusy(true);
    setError('');
    try {
      await api(`/escalations/${signal.id}/close`, { method: 'POST', body: JSON.stringify({ comment }) });
      onDone();
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Не вдалося закрити ескалацію.');
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="stack">
      <p>
        Справа: <strong>{signal.message}</strong>
      </p>
      <ErrorText>{error}</ErrorText>
      <Field label="Що зробили" hint="Коментар побачать інші помічники в журналі.">
        <textarea rows={3} value={comment} onChange={(event) => setComment(event.target.value)} maxLength={300} />
      </Field>
      <button type="button" className="primary" onClick={() => void submit()} disabled={busy}>
        {busy ? 'Закриваємо…' : 'Закрити ескалацію'}
      </button>
    </div>
  );
}

function signalKind(type: Signal['type']): string {
  switch (type) {
    case 'допомога':
      return 'help';
    case 'непевність':
      return 'unsure';
    default:
      return 'escalation';
  }
}
