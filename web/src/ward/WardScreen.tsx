// Екран підопічного: одна поточна справа великим текстом і три зрозумілі дії.

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  ApiError,
  DeviceOccurrence,
  WardScreenData,
  deviceApi,
  forgetDevice,
  markOccurrence,
  pendingMarks,
  syncOfflineMarks,
} from '../api';
import { count, flushAdaptationSignals, watchViewportZoom } from '../adaptation';
import { actionLabel, formatClock, formatWeekday, secondsLeft } from '../format';
import { speak, speechAvailable, stopSpeaking } from '../speech';
import { useMagnifier } from './Magnifier';
import { HelperMode } from './HelperMode';

const REFRESH_INTERVAL = 30_000;

export function WardScreen({ onDisconnect }: { onDisconnect: () => void }) {
  const [data, setData] = useState<WardScreenData | null>(null);
  const [error, setError] = useState('');
  const [message, setMessage] = useState('');
  const [undoUntil, setUndoUntil] = useState('');
  const [tick, setTick] = useState(0);
  const [online, setOnline] = useState(navigator.onLine);
  const [queued, setQueued] = useState(pendingMarks());
  const [helperMode, setHelperMode] = useState(false);
  const wakeLock = useRef<WakeLockSentinel | null>(null);
  const holdTimer = useRef<number | null>(null);

  // Прихований вхід у режим помічника: утримання імені вгорі екрана.
  const startHold = () => {
    cancelHold();
    holdTimer.current = window.setTimeout(() => setHelperMode(true), 2000);
  };
  const cancelHold = () => {
    if (holdTimer.current !== null) {
      window.clearTimeout(holdTimer.current);
      holdTimer.current = null;
    }
  };
  useEffect(() => cancelHold, []);

  const load = useCallback(async () => {
    try {
      const screen = await deviceApi<WardScreenData>('/device/today');
      setData(screen);
      setError('');
    } catch (cause) {
      if (cause instanceof ApiError && cause.status === 401) {
        setError('Цей пристрій відключено. Зверніться до помічника.');
        return;
      }
      setError(cause instanceof ApiError ? cause.message : 'Немає зв’язку з сервером.');
    }
  }, []);

  // Оновлення екрана, синхронізація офлайн-відміток і реакція на повернення зв’язку.
  useEffect(() => {
    void load();
    void flushAdaptationSignals();

    const refresh = window.setInterval(() => void load(), REFRESH_INTERVAL);
    const second = window.setInterval(() => setTick((value) => value + 1), 1000);
    const stopZoomWatch = watchViewportZoom();

    const goOnline = async () => {
      setOnline(true);
      const delivered = await syncOfflineMarks();
      setQueued(pendingMarks());
      if (delivered > 0) setMessage('Збережені відмітки надіслано.');
      await flushAdaptationSignals();
      await load();
    };
    const goOffline = () => setOnline(false);

    window.addEventListener('online', () => void goOnline());
    window.addEventListener('offline', goOffline);
    return () => {
      window.clearInterval(refresh);
      window.clearInterval(second);
      stopZoomWatch();
      window.removeEventListener('online', () => void goOnline());
      window.removeEventListener('offline', goOffline);
      stopSpeaking();
    };
  }, [load]);

  // Екран підопічного не має гаснути посеред справи.
  useEffect(() => {
    if (!data?.ward.wakeLock || !('wakeLock' in navigator)) return;
    let released = false;
    const request = async () => {
      try {
        wakeLock.current = await navigator.wakeLock.request('screen');
      } catch {
        // Браузер може відмовити — екран просто гаситиметься як завжди.
      }
    };
    void request();
    const onVisible = () => {
      if (document.visibilityState === 'visible' && !released) void request();
    };
    document.addEventListener('visibilitychange', onVisible);
    return () => {
      released = true;
      document.removeEventListener('visibilitychange', onVisible);
      void wakeLock.current?.release().catch(() => undefined);
    };
  }, [data?.ward.wakeLock]);

  const settings = data?.settings;
  const magnifier = useMagnifier(settings?.magnifierScale ?? 2, () => count('magnifierUses'));

  const current = useMemo<DeviceOccurrence | undefined>(() => {
    if (!data) return undefined;
    const planned = data.occurrences.filter((item) => item.status === 'planned');
    const now = Date.now();
    // Спершу те, що вже настало; якщо такого немає — найближче попереду.
    return planned.find((item) => new Date(item.scheduledAt).getTime() <= now) ?? planned[0];
  }, [data]);

  // Справа з'являється на екрані заздалегідь, щоб підопічний устиг підготуватися.
  // Але доти кнопку показувати не можна: підпис «Зараз» над завданням, до якого
  // ще вісім годин, підштовхнув би прийняти ліки завчасно. Лічильник tick
  // перемальовує екран щосекунди, тож мить готовності настає без перезавантаження.
  const ready = Boolean(current) && (!current?.openFrom || new Date(current.openFrom).getTime() <= Date.now());

  const justDone = useMemo(() => {
    if (!data) return undefined;
    return data.occurrences.find((item) => item.status === 'done' && secondsLeft(item.undoUntil) > 0);
  }, [data]);

  // Поки можна скасувати відмітку, картка лишається про щойно виконану справу.
  // Інакше «Готово. Дякуємо.» і кнопка «Я натиснув помилково» опинялися б під
  // назвою вже наступного завдання, і незрозуміло, що саме скасовуєш.
  const shown = justDone ?? current;

  if (error && !data) {
    return (
      <main className="ward ward-error">
        <h1>Немає зв’язку</h1>
        <p>{error}</p>
        <button type="button" className="ward-secondary" onClick={() => void load()}>
          Спробувати ще раз
        </button>
        <p
          className="ward-name"
          onPointerDown={startHold}
          onPointerUp={cancelHold}
          onPointerLeave={cancelHold}
          onPointerCancel={cancelHold}
        >
          Догляд
        </p>
        {helperMode && <HelperMode onClose={() => setHelperMode(false)} onDisconnect={onDisconnect} />}
      </main>
    );
  }

  if (!data || !settings) {
    return (
      <main className="ward">
        <p className="ward-loading">Завантажуємо…</p>
      </main>
    );
  }

  const doMark = async () => {
    if (!current) return;
    try {
      const result = await markOccurrence(current.id);
      setMessage(result.message);
      if (result.undoUntil) setUndoUntil(result.undoUntil);
      setQueued(pendingMarks());
      await load();
    } catch (cause) {
      setMessage(cause instanceof ApiError ? cause.message : 'Не вдалося зберегти відмітку.');
    }
  };

  const doUndo = async (occurrenceID: string) => {
    try {
      const result = await deviceApi<{ message: string }>('/device/undo', {
        method: 'POST',
        body: JSON.stringify({ occurrenceID }),
      });
      count('quickUndos');
      setMessage(result.message);
      setUndoUntil('');
      await load();
    } catch (cause) {
      setMessage(cause instanceof ApiError ? cause.message : 'Не вдалося скасувати.');
    }
  };

  const doHelp = async () => {
    try {
      const result = await deviceApi<{ message: string }>('/device/help', {
        method: 'POST',
        body: JSON.stringify({ message: '' }),
      });
      setMessage(result.message);
      await load();
    } catch (cause) {
      setMessage(cause instanceof ApiError ? cause.message : 'Не вдалося надіслати запит.');
    }
  };

  const doUnsure = async () => {
    if (!current) return;
    try {
      const result = await deviceApi<{ message: string }>('/device/unsure', {
        method: 'POST',
        body: JSON.stringify({ occurrenceID: current.id }),
      });
      count('unsureUses');
      setMessage(result.message);
      await load();
    } catch (cause) {
      setMessage(cause instanceof ApiError ? cause.message : 'Не вдалося надіслати запит.');
    }
  };

  const readAloud = () => {
    count('speechUses');
    const parts = [
      current ? 'Зараз: ' + current.title : 'Зараз нічого робити не треба.',
      current?.description ?? '',
      ...(current?.steps ?? []),
      data.helperAnswer,
    ].filter(Boolean);
    speak(parts.join('. '), settings.speechRate);
  };

  const undoSeconds = secondsLeft(undoUntil || justDone?.undoUntil || '');
  const screenClasses = [
    'ward',
    `palette-${settings.palette}`,
    settings.largeControls ? 'large-controls' : '',
    settings.dyslexicFont ? 'font-dyslexic' : '',
  ]
    .filter(Boolean)
    .join(' ');

  return (
    <main
      className={screenClasses}
      style={{
        fontSize: `${settings.scale}%`,
        letterSpacing: settings.letterSpacing,
        wordSpacing: settings.wordSpacing,
        lineHeight: settings.lineHeight,
      }}
    >
      {magnifier.wrap(
        <>
          <header className="ward-head">
            {settings.dementiaMode && (
              <p className="ward-today">
                {formatWeekday(new Date(), data.ward.timezone)} · {formatClock(new Date().toISOString(), data.ward.timezone)}
              </p>
            )}
            {/* Режим помічника відкривається утриманням імені дві секунди.
                Кнопки тут немає навмисно: підопічний не має натрапити на неї випадково. */}
            <p
              className="ward-name"
              onPointerDown={startHold}
              onPointerUp={cancelHold}
              onPointerLeave={cancelHold}
              onPointerCancel={cancelHold}
              onContextMenu={(event) => event.preventDefault()}
            >
              {data.ward.name}
            </p>
          </header>

          {!online && (
            <p className="ward-offline" role="status">
              Немає зв’язку. Відмітки збережуться на пристрої{queued > 0 ? ` (чекають: ${queued})` : ''}.
            </p>
          )}

          {data.helperAnswer && (
            <p className="ward-answer" role="status">
              {data.helperAnswer}
            </p>
          )}

          <section className="ward-card">
            <p className="ward-label">
              {justDone ? 'Виконано' : !current ? 'Зараз нічого робити не треба' : ready ? 'Зараз' : 'Наступна справа'}
            </p>
            <h1>{shown ? shown.title : 'Наступна справа буде пізніше'}</h1>
            {shown && <p className="ward-time">Час: {formatClock(shown.scheduledAt, data.ward.timezone)}</p>}
            {shown?.description && <p className="ward-description">{shown.description}</p>}

            {shown?.photo && (
              <img className="ward-photo" src={`/api/media/${shown.photo}`} alt={`Фото: ${shown.title}`} />
            )}

            {shown && !justDone && shown.steps.length > 0 && (
              <ol className="ward-steps">
                {shown.steps.map((step, index) => (
                  <li key={index}>{step}</li>
                ))}
              </ol>
            )}

            {message && (
              <p className="ward-message" role="status">
                {message}
              </p>
            )}
          </section>

          {!justDone && current && ready && (
            <button type="button" className="ward-main" onClick={() => void doMark()}>
              {actionLabel(current.category)}
            </button>
          )}

          {!justDone && current && !ready && (
            <p className="ward-wait" role="status">
              Поки нічого робити не треба. Нагадаємо о {formatClock(current.scheduledAt, data.ward.timezone)}.
            </p>
          )}

          {undoSeconds > 0 && (
            <button
              type="button"
              className="ward-secondary"
              onClick={() => void doUndo(justDone?.id ?? current?.id ?? '')}
            >
              Я натиснув помилково ({undoSeconds} с)
            </button>
          )}

          {settings.dementiaMode && !justDone && current?.category === 'medicine' && (
            <button type="button" className="ward-secondary" onClick={() => void doUnsure()}>
              Я не пам’ятаю, чи приймав
            </button>
          )}

          <button
            type="button"
            className={data.helpRequestOpen ? 'ward-help ward-help-sent' : 'ward-help'}
            onClick={() => void doHelp()}
            disabled={data.helpRequestOpen}
          >
            {data.helpRequestOpen ? 'Помічників уже повідомлено' : 'Потрібна допомога'}
          </button>

          {data.ward.phone && (
            <a className="ward-phone" href={`tel:${data.ward.phone}`}>
              <span>Подзвонити:</span>
              {/* Номер не можна розривати: «...45 67» в різних рядках не прочитати. */}
              <span className="ward-phone-number">{data.ward.phone}</span>
            </a>
          )}

          <footer className="ward-foot">
            <span>Це не екстрений виклик. У небезпеці телефонуйте 103.</span>
            <div className="ward-tools">
              {settings.speechEnabled && speechAvailable() && (
                <button type="button" className="ward-tool" onClick={readAloud}>
                  Прочитати вголос
                </button>
              )}
              {settings.magnifier && (
                <button
                  type="button"
                  className={magnifier.active ? 'ward-tool active' : 'ward-tool'}
                  aria-pressed={magnifier.active}
                  onClick={magnifier.toggle}
                >
                  {magnifier.active ? 'Вимкнути лупу' : 'Лупа'}
                </button>
              )}
            </div>
          </footer>
        </>,
      )}

      {helperMode && (
        <HelperMode
          onClose={() => setHelperMode(false)}
          onDisconnect={() => {
            forgetDevice();
            onDisconnect();
          }}
        />
      )}

      {/* Оновлення секундоміра скасування. */}
      <span hidden>{tick}</span>
    </main>
  );
}
