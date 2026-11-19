// Профіль помічника: особисті дані, канали сповіщень, сеанси.

import { FormEvent, useCallback, useEffect, useState } from 'react';
import { ApiError, Me, NotificationPref, SessionRecord, api } from '../api';
import { askNotificationPermission } from '../events';
import { formatAgo } from '../format';
import { Checkbox, ErrorText, Field, Loading, NoticeText } from '../components/ui';

const channelNames: Record<NotificationPref['channel'], string> = {
  push: 'Сповіщення у браузері',
  telegram: 'Telegram',
  email: 'Електронна пошта',
};

export function ProfilePanel({ me }: { me: Me }) {
  const [prefs, setPrefs] = useState<NotificationPref[] | null>(null);
  const [sessions, setSessions] = useState<SessionRecord[]>([]);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [pushState, setPushState] = useState(me.pushDevices > 0 ? 'увімкнено' : 'вимкнено');

  const load = useCallback(async () => {
    try {
      setPrefs(await api<NotificationPref[]>('/me/notifications'));
      setSessions(await api<SessionRecord[]>('/me/sessions'));
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Не вдалося отримати налаштування.');
      setPrefs([]);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const saveProfile = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    setError('');
    setNotice('');
    try {
      await api('/me', {
        method: 'PATCH',
        body: JSON.stringify({
          name: String(form.get('name') ?? ''),
          timezone: String(form.get('timezone') ?? 'Europe/Kyiv'),
          phone: String(form.get('phone') ?? ''),
        }),
      });
      setNotice('Профіль збережено.');
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Не вдалося зберегти профіль.');
    }
  };

  const savePrefs = async (next: NotificationPref[]) => {
    setPrefs(next);
    try {
      await api('/me/notifications', { method: 'PUT', body: JSON.stringify(next) });
      setNotice('Налаштування сповіщень збережено.');
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Не вдалося зберегти налаштування.');
    }
  };

  const enablePush = async () => {
    setError('');
    setNotice('');
    try {
      const allowed = await askNotificationPermission();
      if (!allowed) {
        setError('Браузер не дозволив сповіщення. Дозвольте їх у налаштуваннях сайту.');
        return;
      }
      const key = await api<{ publicKey: string; enabled: boolean }>('/me/push/key');
      if (!key.enabled || !key.publicKey) {
        setError('Сервер не налаштовано для push-сповіщень. Додайте ключі VAPID.');
        return;
      }
      const registration = await navigator.serviceWorker.ready;
      const subscription = await registration.pushManager.subscribe({
        userVisibleOnly: true,
        applicationServerKey: base64ToBytes(key.publicKey),
      });
      const raw = subscription.toJSON() as { endpoint?: string; keys?: { p256dh: string; auth: string } };
      await api('/me/push', {
        method: 'POST',
        body: JSON.stringify({ endpoint: raw.endpoint, keys: raw.keys }),
      });
      setPushState('увімкнено');
      setNotice('Сповіщення у браузері увімкнено.');
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Не вдалося увімкнути сповіщення у браузері.');
    }
  };

  const connectTelegram = async () => {
    setError('');
    try {
      const link = await api<{ url: string }>('/me/telegram', { method: 'POST', body: '{}' });
      window.open(link.url, '_blank', 'noopener');
      setNotice('Відкрийте бота і натисніть «Почати». Посилання діє годину.');
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Не вдалося створити посилання.');
    }
  };

  const revokeSession = async (session: SessionRecord) => {
    try {
      await api(`/me/sessions/${session.id}`, { method: 'DELETE' });
      await load();
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Не вдалося завершити сеанс.');
    }
  };

  if (!prefs) return <Loading what="профіль" />;

  return (
    <div className="stack">
      <ErrorText>{error}</ErrorText>
      <NoticeText>{notice}</NoticeText>

      <section>
        <h3>Ваші дані</h3>
        <form onSubmit={saveProfile} className="stack">
          <Field label="Ім'я">
            <input name="name" defaultValue={me.name} required maxLength={80} />
          </Field>
          <Field label="Часовий пояс" hint="За ним рахуються тихі години.">
            <select name="timezone" defaultValue={me.timezone}>
              <option value="Europe/Kyiv">Київ</option>
              <option value="Europe/Warsaw">Варшава</option>
              <option value="Europe/Berlin">Берлін</option>
              <option value="Europe/Lisbon">Лісабон</option>
              <option value="America/New_York">Нью-Йорк</option>
            </select>
          </Field>
          <Field label="Телефон">
            <input name="phone" type="tel" defaultValue={me.phone} maxLength={30} />
          </Field>
          <Field label="Електронна пошта">
            <input value={me.email} readOnly />
          </Field>
          <button className="primary">Зберегти</button>
        </form>
      </section>

      <section>
        <h3>Куди надсилати сигнали</h3>
        <p className="field-hint">
          Термінові сигнали — «Потрібна допомога» та ескалації — приходять навіть у тихі години.
          Вони завжди видно і в панелі, у розділі «Сигнали».
        </p>
        <ul className="pref-list">
          {prefs.map((pref, index) => (
            <li key={pref.channel}>
              <Checkbox
                label={channelNames[pref.channel]}
                checked={pref.enabled}
                onChange={(value) => {
                  const next = [...prefs];
                  next[index] = { ...pref, enabled: value };
                  void savePrefs(next);
                }}
              />
              <div className="quiet-hours">
                <label>
                  Тихі години з
                  <input
                    type="time"
                    value={pref.quietStart}
                    onChange={(event) => {
                      const next = [...prefs];
                      next[index] = { ...pref, quietStart: event.target.value };
                      setPrefs(next);
                    }}
                    onBlur={() => void savePrefs(prefs)}
                  />
                </label>
                <label>
                  до
                  <input
                    type="time"
                    value={pref.quietEnd}
                    onChange={(event) => {
                      const next = [...prefs];
                      next[index] = { ...pref, quietEnd: event.target.value };
                      setPrefs(next);
                    }}
                    onBlur={() => void savePrefs(prefs)}
                  />
                </label>
              </div>
              {pref.channel === 'push' && (
                <p className="field-hint">
                  Стан на цьому пристрої: {pushState}.{' '}
                  <button type="button" className="text" onClick={() => void enablePush()}>
                    Увімкнути на цьому пристрої
                  </button>
                </p>
              )}
              {pref.channel === 'telegram' && (
                <p className="field-hint">
                  {me.telegramLinked ? 'Telegram підключено.' : 'Telegram не підключено.'}{' '}
                  <button type="button" className="text" onClick={() => void connectTelegram()}>
                    {me.telegramLinked ? 'Підключити інший чат' : 'Підключити Telegram'}
                  </button>
                </p>
              )}
              {pref.channel === 'email' && !me.emailConfirmed && (
                <p className="field-hint">Адресу ще не підтверджено, тому листи можуть не надходити.</p>
              )}
            </li>
          ))}
        </ul>
      </section>

      <section>
        <h3>Відкриті сеанси</h3>
        <ul className="row-list">
          {sessions.map((session) => (
            <li key={session.id} className="row">
              <div className="row-main">
                <strong>{shortAgent(session.userAgent)}</strong>
                <small>активність {formatAgo(session.lastSeenAt)}</small>
              </div>
              <div className="row-actions">
                <button type="button" onClick={() => void revokeSession(session)}>
                  Завершити
                </button>
              </div>
            </li>
          ))}
        </ul>
      </section>
    </div>
  );
}

/** base64ToBytes перетворює ключ VAPID у формат, який приймає браузер. */
function base64ToBytes(value: string): ArrayBuffer {
  const padding = '='.repeat((4 - (value.length % 4)) % 4);
  const normalized = (value + padding).replace(/-/g, '+').replace(/_/g, '/');
  const binary = atob(normalized);
  const buffer = new ArrayBuffer(binary.length);
  const bytes = new Uint8Array(buffer);
  for (let index = 0; index < binary.length; index += 1) bytes[index] = binary.charCodeAt(index);
  return buffer;
}

/** shortAgent показує зрозумілу назву пристрою замість повного рядка браузера. */
function shortAgent(agent: string): string {
  if (!agent) return 'Невідомий пристрій';
  if (/android/i.test(agent)) return 'Android';
  if (/iphone|ipad/i.test(agent)) return 'iPhone або iPad';
  if (/macintosh/i.test(agent)) return 'Mac';
  if (/windows/i.test(agent)) return 'Windows';
  if (/linux/i.test(agent)) return 'Linux';
  return agent.slice(0, 40);
}
