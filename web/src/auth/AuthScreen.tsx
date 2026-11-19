// Вхід помічника, створення облікового запису та підключення пристрою підопічного.

import { FormEvent, useState } from 'react';
import { ApiError, api, saveDeviceToken } from '../api';
import { ErrorText, Field, NoticeText } from '../components/ui';

type Mode = 'login' | 'register' | 'pair' | 'forgot';

export function AuthScreen({
  initialMode,
  initialCode,
  onSignedIn,
  onDevicePaired,
}: {
  initialMode?: Mode;
  initialCode?: string;
  onSignedIn: () => void;
  onDevicePaired: () => void;
}) {
  const [mode, setMode] = useState<Mode>(initialMode ?? 'login');
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [busy, setBusy] = useState(false);

  const reset = (next: Mode) => {
    setMode(next);
    setError('');
    setNotice('');
  };

  const submitCredentials = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    setBusy(true);
    setError('');
    try {
      if (mode === 'register') {
        await api('/auth/register', {
          method: 'POST',
          body: JSON.stringify({
            name: String(form.get('name') ?? ''),
            email: String(form.get('email') ?? ''),
            password: String(form.get('password') ?? ''),
            timezone: Intl.DateTimeFormat().resolvedOptions().timeZone || 'Europe/Kyiv',
          }),
        });
      } else {
        await api('/auth/login', {
          method: 'POST',
          body: JSON.stringify({
            email: String(form.get('email') ?? ''),
            password: String(form.get('password') ?? ''),
          }),
        });
      }
      onSignedIn();
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Не вдалося увійти.');
    } finally {
      setBusy(false);
    }
  };

  const submitForgot = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    setBusy(true);
    setError('');
    try {
      const answer = await api<{ message: string }>('/auth/password-reset', {
        method: 'POST',
        body: JSON.stringify({ email: String(form.get('email') ?? '') }),
      });
      setNotice(answer.message);
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Не вдалося надіслати лист.');
    } finally {
      setBusy(false);
    }
  };

  const submitPairing = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    setBusy(true);
    setError('');
    try {
      const result = await api<{ deviceToken: string }>('/pairing', {
        method: 'POST',
        body: JSON.stringify({
          code: String(form.get('code') ?? ''),
          label: String(form.get('label') ?? '') || 'Пристрій підопічного',
        }),
      });
      saveDeviceToken(result.deviceToken);
      onDevicePaired();
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Не вдалося підключити пристрій.');
    } finally {
      setBusy(false);
    }
  };

  return (
    <main className="auth">
      <section className="auth-card">
        <p className="eyebrow">ДОГЛЯД</p>
        <h1>{titleFor(mode)}</h1>
        <p className="auth-lead">{leadFor(mode)}</p>

        <ErrorText>{error}</ErrorText>
        <NoticeText>{notice}</NoticeText>

        {mode === 'pair' && (
          <form onSubmit={submitPairing}>
            <Field label="Шестизначний код" hint="Код створює помічник у розділі «Пристрої».">
              <input
                name="code"
                inputMode="numeric"
                pattern="[0-9]{6}"
                maxLength={6}
                required
                autoFocus
                defaultValue={initialCode ?? ''}
                autoComplete="one-time-code"
              />
            </Field>
            <Field label="Назва пристрою" hint="Наприклад: планшет на кухні.">
              <input name="label" defaultValue="Пристрій підопічного" />
            </Field>
            <button className="primary" disabled={busy}>
              {busy ? 'Підключаємо…' : 'Підключити пристрій'}
            </button>
          </form>
        )}

        {mode === 'forgot' && (
          <form onSubmit={submitForgot}>
            <Field label="Електронна пошта">
              <input name="email" type="email" required autoFocus autoComplete="email" />
            </Field>
            <button className="primary" disabled={busy}>
              {busy ? 'Надсилаємо…' : 'Надіслати посилання'}
            </button>
          </form>
        )}

        {(mode === 'login' || mode === 'register') && (
          <form onSubmit={submitCredentials}>
            {mode === 'register' && (
              <Field label="Ваше ім'я">
                <input name="name" required autoFocus autoComplete="name" />
              </Field>
            )}
            <Field label="Електронна пошта">
              <input name="email" type="email" required autoComplete="email" />
            </Field>
            <Field
              label="Пароль"
              hint={mode === 'register' ? 'Щонайменше 10 символів.' : undefined}
            >
              <input
                name="password"
                type="password"
                minLength={mode === 'register' ? 10 : undefined}
                required
                autoComplete={mode === 'register' ? 'new-password' : 'current-password'}
              />
            </Field>
            <button className="primary" disabled={busy}>
              {busy ? 'Хвилинку…' : mode === 'register' ? 'Створити обліковий запис' : 'Увійти'}
            </button>
          </form>
        )}

        <nav className="auth-links">
          {mode !== 'login' && (
            <button type="button" className="text" onClick={() => reset('login')}>
              Увійти в обліковий запис
            </button>
          )}
          {mode !== 'register' && mode !== 'pair' && (
            <button type="button" className="text" onClick={() => reset('register')}>
              Створити обліковий запис
            </button>
          )}
          {mode === 'login' && (
            <button type="button" className="text" onClick={() => reset('forgot')}>
              Я забув пароль
            </button>
          )}
          {mode !== 'pair' && (
            <button type="button" className="text" onClick={() => reset('pair')}>
              Підключити пристрій підопічного
            </button>
          )}
        </nav>

        <p className="auth-footnote">
          Це не медичний виріб і не засіб екстреного виклику. В екстреній ситуації телефонуйте 103.
        </p>
      </section>
    </main>
  );
}

function titleFor(mode: Mode): string {
  switch (mode) {
    case 'pair':
      return 'Підключити пристрій';
    case 'register':
      return 'Створити обліковий запис';
    case 'forgot':
      return 'Відновлення пароля';
    default:
      return 'Підтримка щоденних справ';
  }
}

function leadFor(mode: Mode): string {
  switch (mode) {
    case 'pair':
      return 'Введіть код, який створив помічник. Після підключення екран більше не проситиме входу.';
    case 'register':
      return 'Обліковий запис потрібен лише помічникові. Підопічний користується пристроєм без входу.';
    case 'forgot':
      return 'Надішлемо посилання для зміни пароля. Воно діє одну годину.';
    default:
      return 'Для помічників. Підопічний не створює обліковий запис.';
  }
}
