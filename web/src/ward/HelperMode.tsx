// Режим помічника на пристрої підопічного: відкривається за PIN-кодом.
//
// Сюди потрапляє помічник, який стоїть поруч із людиною: відмітити виконане,
// закрити сигнал, відповісти на питання. Налаштувань доступу, пристроїв і
// профілю тут немає навмисно — планшет лишається в чужій квартирі, і чотири
// цифри не мають відкривати обліковий запис. Для решти є власний телефон.

import { useCallback, useEffect, useState } from 'react';
import { ApiError, Occurrence, PINOverview, Signal, deviceApi, pinApi, setPINToken } from '../api';
import { ErrorText, Field, Loading, NoticeText } from '../components/ui';
import { formatClock } from '../format';

const REFRESH_INTERVAL = 15_000;

export function HelperMode({ onClose, onDisconnect }: { onClose: () => void; onDisconnect: () => void }) {
  const [pin, setPin] = useState('');
  const [unlocked, setUnlocked] = useState(false);
  const [overview, setOverview] = useState<PINOverview | null>(null);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    try {
      setOverview(await pinApi<PINOverview>('/device/helper/overview'));
      setError('');
    } catch (cause) {
      if (cause instanceof ApiError && cause.status === 401) {
        // Сеанс короткий навмисно: планшет, залишений на столі, закривається сам.
        setUnlocked(false);
        setOverview(null);
        setError('Режим помічника завершено. Введіть PIN-код ще раз.');
        return;
      }
      setError(cause instanceof ApiError ? cause.message : 'Немає зв’язку з сервером.');
    }
  }, []);

  useEffect(() => {
    if (!unlocked) return;
    void load();
    const timer = window.setInterval(() => void load(), REFRESH_INTERVAL);
    return () => window.clearInterval(timer);
  }, [unlocked, load]);

  const unlock = async () => {
    setBusy(true);
    setError('');
    try {
      const opened = await deviceApi<{ pinToken: string }>('/device/pin-login', {
        method: 'POST',
        body: JSON.stringify({ pin }),
      });
      setPINToken(opened.pinToken);
      setPin('');
      setUnlocked(true);
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Не вдалося перевірити PIN-код.');
      setPin('');
    } finally {
      setBusy(false);
    }
  };

  /** act виконує дію режиму й одразу перечитує огляд. */
  const act = async (run: () => Promise<unknown>, done: string) => {
    setBusy(true);
    setError('');
    setNotice('');
    try {
      await run();
      setNotice(done);
      await load();
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Не вдалося виконати дію.');
    } finally {
      setBusy(false);
    }
  };

  const leave = async () => {
    try {
      await pinApi('/device/helper/logout', { method: 'POST' });
    } catch {
      // Навіть якщо запит не пройшов, локально режим закриваємо.
    }
    setPINToken('');
    onClose();
  };

  return (
    <div className="backdrop" onMouseDown={(event) => event.target === event.currentTarget && onClose()}>
      <section className="dialog pin-dialog" role="dialog" aria-modal="true" aria-label="Режим помічника">
        <header className="dialog-head">
          <h2>Режим помічника</h2>
          <button type="button" className="icon-button" onClick={onClose} aria-label="Закрити">
            ×
          </button>
        </header>

        <div className="dialog-body stack">
          {!unlocked ? (
            <>
              <p>Введіть PIN-код, який задав головний помічник. Після п’яти помилок вхід блокується на 15 хвилин.</p>
              <ErrorText>{error}</ErrorText>
              <Field label="PIN-код">
                <input
                  name="pin"
                  value={pin}
                  inputMode="numeric"
                  maxLength={6}
                  autoFocus
                  onChange={(event) => setPin(event.target.value.replace(/\D/g, '').slice(0, 6))}
                />
              </Field>
              <button type="button" className="primary" onClick={() => void unlock()} disabled={busy || pin.length < 4}>
                {busy ? 'Перевіряємо…' : 'Відкрити'}
              </button>
            </>
          ) : !overview ? (
            <Loading what="режим помічника" />
          ) : (
            <>
              <p className="pin-ward">{overview.ward.name}</p>
              <ErrorText>{error}</ErrorText>
              <NoticeText>{notice}</NoticeText>

              <PINSignals overview={overview} busy={busy} act={act} />
              <PINTasks overview={overview} busy={busy} act={act} />

              <p className="pin-limits">
                Розклад, налаштування екрана й доступ помічників змінюють у застосунку на власному телефоні: з
                PIN-коду вони не відкриваються.
              </p>

              <button type="button" onClick={() => void leave()}>
                Вийти з режиму помічника
              </button>
              <button
                type="button"
                className="danger"
                onClick={() => {
                  if (window.confirm('Відключити цей пристрій? Щоб повернути його, знадобиться новий код.')) {
                    onDisconnect();
                  }
                }}
              >
                Відключити цей пристрій
              </button>
            </>
          )}
        </div>
      </section>
    </div>
  );
}

type ActFn = (run: () => Promise<unknown>, done: string) => Promise<void>;

/** PINSignals показує відкриті сигнали цього підопічного. */
function PINSignals({ overview, busy, act }: { overview: PINOverview; busy: boolean; act: ActFn }) {
  if (overview.signals.length === 0) {
    return (
      <section className="pin-block">
        <h3>Сигнали</h3>
        <p className="pin-empty">Відкритих сигналів немає.</p>
      </section>
    );
  }
  return (
    <section className="pin-block">
      <h3>Сигнали</h3>
      <ul className="pin-list">
        {overview.signals.map((item) => (
          <li key={item.type + item.id}>
            <PINSignal signal={item} timezone={overview.ward.timezone} busy={busy} act={act} />
          </li>
        ))}
      </ul>
    </section>
  );
}

function PINSignal({
  signal,
  timezone,
  busy,
  act,
}: {
  signal: Signal;
  timezone: string;
  busy: boolean;
  act: ActFn;
}) {
  const [answer, setAnswer] = useState('');

  return (
    <>
      <div className="pin-row-head">
        <strong>{signal.title}</strong>
        <span className="pin-time">{formatClock(signal.createdAt, timezone)}</span>
      </div>
      {signal.message && <p className="pin-note">{signal.message}</p>}

      {signal.type === 'непевність' ? (
        <div className="stack">
          <Field label="Відповідь підопічному">
            <input
              value={answer}
              placeholder="Так, ви вже прийняли"
              onChange={(event) => setAnswer(event.target.value)}
            />
          </Field>
          <div className="pin-actions">
            <button
              type="button"
              className="primary"
              disabled={busy || answer.trim() === ''}
              onClick={() =>
                void act(
                  () =>
                    pinApi(`/device/helper/unsure/${signal.id}/answer`, {
                      method: 'POST',
                      body: JSON.stringify({ answer, markAsDone: false }),
                    }),
                  'Відповідь надіслано.',
                )
              }
            >
              Відповісти
            </button>
            <button
              type="button"
              disabled={busy || answer.trim() === ''}
              onClick={() =>
                void act(
                  () =>
                    pinApi(`/device/helper/unsure/${signal.id}/answer`, {
                      method: 'POST',
                      body: JSON.stringify({ answer, markAsDone: true }),
                    }),
                  'Відповідь надіслано, справу відмічено.',
                )
              }
            >
              Відповісти й відмітити
            </button>
          </div>
        </div>
      ) : (
        <div className="pin-actions">
          <button
            type="button"
            className="primary"
            disabled={busy}
            onClick={() =>
              void act(
                () =>
                  signal.type === 'допомога'
                    ? pinApi(`/device/helper/help/${signal.id}/close`, { method: 'POST' })
                    : pinApi(`/device/helper/escalations/${signal.id}/close`, {
                        method: 'POST',
                        body: JSON.stringify({ comment: 'Закрито на пристрої підопічного.' }),
                      }),
                'Сигнал закрито.',
              )
            }
          >
            Питання вирішено
          </button>
        </div>
      )}
    </>
  );
}

/** PINTasks показує справи дня з можливістю відмітити виконання. */
function PINTasks({ overview, busy, act }: { overview: PINOverview; busy: boolean; act: ActFn }) {
  if (overview.occurrences.length === 0) {
    return (
      <section className="pin-block">
        <h3>Справи</h3>
        <p className="pin-empty">На сьогодні справ немає.</p>
      </section>
    );
  }
  return (
    <section className="pin-block">
      <h3>Справи</h3>
      <ul className="pin-list">
        {overview.occurrences.map((item) => (
          <li key={item.id}>
            <PINTask occurrence={item} timezone={overview.ward.timezone} busy={busy} act={act} />
          </li>
        ))}
      </ul>
    </section>
  );
}

const STATUS_TEXT: Record<string, string> = {
  planned: 'Заплановано',
  done: 'Виконано',
  missed: 'Пропущено',
};

function PINTask({
  occurrence,
  timezone,
  busy,
  act,
}: {
  occurrence: Occurrence;
  timezone: string;
  busy: boolean;
  act: ActFn;
}) {
  // Відмітити можна лише з початку вікна справи: те саме правило, що й на
  // великому екрані підопічного, бо йдеться про ті самі ліки.
  const ready = !occurrence.openFrom || new Date(occurrence.openFrom).getTime() <= Date.now();

  return (
    <>
      <div className="pin-row-head">
        <strong>{occurrence.title}</strong>
        <span className="pin-time">{formatClock(occurrence.scheduledAt, timezone)}</span>
      </div>
      <p className="pin-note">
        {STATUS_TEXT[occurrence.status] ?? occurrence.status}
        {occurrence.critical ? ' · критична' : ''}
      </p>
      <div className="pin-actions">
        {occurrence.status === 'planned' ? (
          ready ? (
            <button
              type="button"
              className="primary"
              disabled={busy}
              onClick={() =>
                void act(
                  () =>
                    pinApi(`/device/helper/occurrences/${occurrence.id}/mark`, {
                      method: 'POST',
                      body: JSON.stringify({ status: 'done' }),
                    }),
                  'Справу відмічено.',
                )
              }
            >
              Відмітити виконаним
            </button>
          ) : (
            <span className="pin-note">Ще не час.</span>
          )
        ) : (
          <button
            type="button"
            disabled={busy}
            onClick={() =>
              void act(
                () => pinApi(`/device/helper/occurrences/${occurrence.id}/undo`, { method: 'POST' }),
                'Відмітку скасовано.',
              )
            }
          >
            Скасувати відмітку
          </button>
        )}
      </div>
    </>
  );
}
