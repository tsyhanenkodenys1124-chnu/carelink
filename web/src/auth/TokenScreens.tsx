// Сторінки, на які ведуть посилання з листів: зміна пароля та підтвердження адреси.

import { FormEvent, useEffect, useState } from 'react';
import { ApiError, api } from '../api';
import { ErrorText, Field, NoticeText } from '../components/ui';

/** Зміна пароля за посиланням із листа. */
export function ResetPasswordScreen({ token, onDone }: { token: string; onDone: () => void }) {
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [busy, setBusy] = useState(false);

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const password = String(form.get('password') ?? '');
    if (password !== String(form.get('repeat') ?? '')) {
      setError('Паролі не збігаються.');
      return;
    }
    setBusy(true);
    setError('');
    try {
      const answer = await api<{ message: string }>('/auth/password-reset/confirm', {
        method: 'POST',
        body: JSON.stringify({ token, password }),
      });
      setNotice(answer.message);
      window.setTimeout(onDone, 1500);
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Не вдалося змінити пароль.');
    } finally {
      setBusy(false);
    }
  };

  return (
    <main className="auth">
      <section className="auth-card">
        <p className="eyebrow">ДОГЛЯД</p>
        <h1>Новий пароль</h1>
        <p className="auth-lead">Після зміни пароля всі відкриті сеанси завершаться.</p>
        <ErrorText>{error}</ErrorText>
        <NoticeText>{notice}</NoticeText>
        <form onSubmit={submit}>
          <Field label="Новий пароль" hint="Щонайменше 10 символів.">
            <input name="password" type="password" minLength={10} required autoFocus autoComplete="new-password" />
          </Field>
          <Field label="Повторіть пароль">
            <input name="repeat" type="password" minLength={10} required autoComplete="new-password" />
          </Field>
          <button className="primary" disabled={busy}>
            {busy ? 'Зберігаємо…' : 'Зберегти пароль'}
          </button>
        </form>
        <button type="button" className="text" onClick={onDone}>
          Повернутися до входу
        </button>
      </section>
    </main>
  );
}

/** Підтвердження електронної адреси за посиланням із листа. */
export function ConfirmEmailScreen({ token, onDone }: { token: string; onDone: () => void }) {
  const [state, setState] = useState<'waiting' | 'done' | 'failed'>('waiting');
  const [message, setMessage] = useState('Перевіряємо посилання…');

  useEffect(() => {
    let cancelled = false;
    void api<{ message: string }>('/auth/email/confirm', {
      method: 'POST',
      body: JSON.stringify({ token }),
    })
      .then((answer) => {
        if (cancelled) return;
        setState('done');
        setMessage(answer.message);
      })
      .catch((cause: unknown) => {
        if (cancelled) return;
        setState('failed');
        setMessage(cause instanceof ApiError ? cause.message : 'Не вдалося підтвердити адресу.');
      });
    return () => {
      cancelled = true;
    };
  }, [token]);

  return (
    <main className="auth">
      <section className="auth-card">
        <p className="eyebrow">ДОГЛЯД</p>
        <h1>Підтвердження адреси</h1>
        {state === 'failed' ? <ErrorText>{message}</ErrorText> : <NoticeText>{message}</NoticeText>}
        <button type="button" className="primary" onClick={onDone}>
          Перейти до застосунку
        </button>
      </section>
    </main>
  );
}

/** Прийняття запрошення від головного помічника. */
export function AcceptInvitationScreen({
  token,
  signedIn,
  onAccepted,
  onNeedSignIn,
}: {
  token: string;
  signedIn: boolean;
  onAccepted: () => void;
  onNeedSignIn: () => void;
}) {
  const [wardName, setWardName] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    void api<{ wardName: string }>(`/invitations/${encodeURIComponent(token)}`)
      .then((preview) => setWardName(preview.wardName))
      .catch((cause: unknown) =>
        setError(cause instanceof ApiError ? cause.message : 'Запрошення недійсне.'),
      );
  }, [token]);

  const accept = async () => {
    setBusy(true);
    setError('');
    try {
      await api('/invitations/accept', { method: 'POST', body: JSON.stringify({ token }) });
      onAccepted();
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Не вдалося прийняти запрошення.');
    } finally {
      setBusy(false);
    }
  };

  return (
    <main className="auth">
      <section className="auth-card">
        <p className="eyebrow">ДОГЛЯД</p>
        <h1>Запрошення допомагати</h1>
        {wardName && <p className="auth-lead">Вас запрошують до профілю «{wardName}».</p>}
        <ErrorText>{error}</ErrorText>
        {signedIn ? (
          <button type="button" className="primary" onClick={() => void accept()} disabled={busy || !wardName}>
            {busy ? 'Приймаємо…' : 'Прийняти запрошення'}
          </button>
        ) : (
          <>
            <p>Щоб прийняти запрошення, увійдіть до свого облікового запису або створіть його.</p>
            <button type="button" className="primary" onClick={onNeedSignIn}>
              Увійти
            </button>
          </>
        )}
      </section>
    </main>
  );
}
