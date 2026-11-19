// Профіль підопічного: звертання, часовий пояс, телефон, фото та видалення.

import { FormEvent, useCallback, useEffect, useState } from 'react';
import { ApiError, Rights, Ward, api, upload } from '../api';
import { Checkbox, ErrorText, Field, Loading, NoticeText } from '../components/ui';

type WardProfile = {
  id: string;
  addressName: string;
  timezone: string;
  emergencyPhone: string;
  photo: string;
  wakeLockEnabled: boolean;
  pinConfigured: boolean;
  rights: Rights;
};

export function WardProfilePanel({ ward, onChanged }: { ward: Ward; onChanged: () => void }) {
  const [profile, setProfile] = useState<WardProfile | null>(null);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    try {
      setProfile(await api<WardProfile>(`/wards/${ward.id}`));
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Не вдалося отримати профіль.');
    }
  }, [ward.id]);

  useEffect(() => {
    setProfile(null);
    setNotice('');
    void load();
  }, [load]);

  if (!profile) return <Loading what="профіль підопічного" />;
  const editable = ward.rights.editSettings;

  const save = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setBusy(true);
    setError('');
    setNotice('');
    try {
      await api(`/wards/${ward.id}`, {
        method: 'PATCH',
        body: JSON.stringify({
          addressName: profile.addressName,
          timezone: profile.timezone,
          emergencyPhone: profile.emergencyPhone,
          wakeLockEnabled: profile.wakeLockEnabled,
        }),
      });
      setNotice('Профіль збережено. Розклад перераховано за новим часовим поясом.');
      onChanged();
      await load();
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Не вдалося зберегти профіль.');
    } finally {
      setBusy(false);
    }
  };

  const uploadPhoto = async (file: File) => {
    setError('');
    setNotice('');
    try {
      await upload(`/wards/${ward.id}/photo`, file);
      setNotice('Фото збережено.');
      onChanged();
      await load();
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Не вдалося завантажити фото.');
    }
  };

  const remove = async () => {
    const confirmation = window.prompt(
      `Видалення профілю відключить усі пристрої та прибере майбутні справи. Історія та журнал залишаться.\nЩоб підтвердити, введіть звертання: ${profile.addressName}`,
    );
    if (confirmation !== profile.addressName) return;
    try {
      await api(`/wards/${ward.id}`, { method: 'DELETE' });
      onChanged();
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Не вдалося видалити профіль.');
    }
  };

  return (
    <section className="panel">
      <div className="panel-head">
        <div>
          <h2>Профіль підопічного</h2>
          <p>Звертання бачить сам підопічний на своєму екрані. Часовий пояс визначає час усіх справ.</p>
        </div>
      </div>

      <ErrorText>{error}</ErrorText>
      <NoticeText>{notice}</NoticeText>

      <form onSubmit={save} className="stack">
        <div className="grid-two">
          <Field label="Як звертатися">
            <input
              value={profile.addressName}
              disabled={!editable}
              maxLength={60}
              required
              onChange={(event) => setProfile({ ...profile, addressName: event.target.value })}
            />
          </Field>
          <Field label="Часовий пояс">
            <select
              value={profile.timezone}
              disabled={!editable}
              onChange={(event) => setProfile({ ...profile, timezone: event.target.value })}
            >
              <option value="Europe/Kyiv">Київ</option>
              <option value="Europe/Warsaw">Варшава</option>
              <option value="Europe/Berlin">Берлін</option>
              <option value="Europe/Lisbon">Лісабон</option>
              <option value="America/New_York">Нью-Йорк</option>
            </select>
          </Field>
        </div>

        <Field label="Телефон на екрані підопічного" hint="Показується великою кнопкою, коли потрібно подзвонити.">
          <input
            type="tel"
            value={profile.emergencyPhone}
            disabled={!editable}
            maxLength={30}
            onChange={(event) => setProfile({ ...profile, emergencyPhone: event.target.value })}
          />
        </Field>

        <Checkbox
          label="Не давати екрану гаснути, поки відкрита справа"
          checked={profile.wakeLockEnabled}
          disabled={!editable}
          onChange={(value) => setProfile({ ...profile, wakeLockEnabled: value })}
        />

        {editable && (
          <button className="primary" disabled={busy}>
            {busy ? 'Зберігаємо…' : 'Зберегти профіль'}
          </button>
        )}
      </form>

      <section>
        <h3>Фотографія</h3>
        <p className="field-hint">Допомагає впізнати свій пристрій. До 5 МБ, формати JPEG, PNG або WebP.</p>
        {profile.photo && (
          <img className="task-photo-preview" src={`/api/media/${profile.photo}`} alt="Фото підопічного" />
        )}
        {editable && (
          <Field label="Нове фото">
            <input
              type="file"
              accept="image/jpeg,image/png,image/webp"
              onChange={(event) => {
                const file = event.target.files?.[0];
                if (file) void uploadPhoto(file);
              }}
            />
          </Field>
        )}
      </section>

      {ward.rights.primary && (
        <section>
          <h3>Видалити профіль</h3>
          <p className="field-hint">
            {profile.pinConfigured ? 'PIN режиму помічника встановлено. ' : 'PIN режиму помічника ще не встановлено. '}
            Видалення відключить пристрої та прибере майбутні справи. Скасувати цю дію не можна.
          </p>
          <button type="button" className="danger" onClick={() => void remove()}>
            Видалити профіль підопічного
          </button>
        </section>
      )}
    </section>
  );
}
