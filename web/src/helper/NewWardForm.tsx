// Створення профілю підопічного.

import { FormEvent, useState } from 'react';
import { ApiError, api } from '../api';
import { ErrorText, Field } from '../components/ui';

export function NewWardForm({ onCreated }: { onCreated: (wardID: string) => void }) {
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    setBusy(true);
    setError('');
    try {
      const created = await api<{ id: string }>('/wards', {
        method: 'POST',
        body: JSON.stringify({
          addressName: String(form.get('addressName') ?? ''),
          timezone: String(form.get('timezone') ?? 'Europe/Kyiv'),
          emergencyPhone: String(form.get('emergencyPhone') ?? ''),
        }),
      });
      onCreated(created.id);
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Не вдалося створити профіль.');
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} className="stack">
      <ErrorText>{error}</ErrorText>
      <Field label="Як звертатися" hint="Саме це побачить підопічний на своєму екрані.">
        <input name="addressName" placeholder="Маріє Іванівно" required autoFocus maxLength={60} />
      </Field>
      <Field label="Часовий пояс" hint="За ним рахується час усіх справ.">
        <select name="timezone" defaultValue="Europe/Kyiv">
          <option value="Europe/Kyiv">Київ</option>
          <option value="Europe/Warsaw">Варшава</option>
          <option value="Europe/Berlin">Берлін</option>
          <option value="Europe/Lisbon">Лісабон</option>
          <option value="America/New_York">Нью-Йорк</option>
        </select>
      </Field>
      <Field label="Телефон для екрана підопічного" hint="Показується, коли немає зв’язку. Не обов’язково.">
        <input name="emergencyPhone" type="tel" maxLength={30} />
      </Field>
      <button className="primary" disabled={busy}>
        {busy ? 'Створюємо…' : 'Зберегти профіль'}
      </button>
    </form>
  );
}
