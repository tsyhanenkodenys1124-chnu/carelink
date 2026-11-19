// Пристрої підопічного: підключення кодом або QR, перелік і відключення, PIN режиму помічника.

import { useCallback, useEffect, useState } from 'react';
import { ApiError, DeviceRecord, Ward, api } from '../api';
import { formatAgo, formatDateTime } from '../format';
import { ErrorText, Field, Loading, NoticeText } from '../components/ui';

type Pairing = { code: string; expiresAt: string; pairingURL: string; qr?: string };

export function DevicesPanel({ ward }: { ward: Ward }) {
  const [devices, setDevices] = useState<DeviceRecord[] | null>(null);
  const [pairing, setPairing] = useState<Pairing | null>(null);
  const [pin, setPin] = useState('');
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');

  const load = useCallback(async () => {
    try {
      setDevices(await api<DeviceRecord[]>(`/wards/${ward.id}/devices`));
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Не вдалося отримати пристрої.');
      setDevices([]);
    }
  }, [ward.id]);

  useEffect(() => {
    setDevices(null);
    setPairing(null);
    void load();
  }, [load]);

  const createPairing = async () => {
    setError('');
    setNotice('');
    try {
      setPairing(await api<Pairing>(`/wards/${ward.id}/pairing`, { method: 'POST', body: '{}' }));
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Не вдалося створити код.');
    }
  };

  const revoke = async (device: DeviceRecord) => {
    if (!window.confirm(`Відключити пристрій «${device.label}»? Він більше не показуватиме справи.`)) return;
    try {
      await api(`/wards/${ward.id}/devices/${device.id}`, { method: 'DELETE' });
      await load();
      setNotice('Пристрій відключено.');
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Не вдалося відключити пристрій.');
    }
  };

  const savePIN = async () => {
    setError('');
    setNotice('');
    try {
      await api(`/wards/${ward.id}/pin`, { method: 'PUT', body: JSON.stringify({ pin }) });
      setPin('');
      setNotice('PIN збережено. Він відкриває режим помічника просто на пристрої підопічного.');
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Не вдалося зберегти PIN.');
    }
  };

  if (!devices) return <Loading what="пристрої" />;
  const editable = ward.rights.editSettings;

  return (
    <section className="panel">
      <ErrorText>{error}</ErrorText>
      <NoticeText>{notice}</NoticeText>

      {/* Дві рівні картки: в загальній сітці одиночна картка стискалася
          до ширини колонки, і текст переносився по одному слову. */}
      <div className="cards cards-pair">
        <article className="card">
          <h2>Підключити пристрій</h2>
          <p>
            На пристрої підопічного відкрийте застосунок і натисніть «Підключити пристрій підопічного».
            Введіть код або наведіть камеру на QR-код. Код діє 15 хвилин.
          </p>
          {editable ? (
            <button type="button" className="primary" onClick={() => void createPairing()}>
              Створити код
            </button>
          ) : (
            <p className="field-hint">Створювати коди може помічник із правом на налаштування.</p>
          )}

          {pairing && (
            <div className="pairing">
              <p className="pairing-code">{pairing.code}</p>
              {pairing.qr && <img className="pairing-qr" src={pairing.qr} alt="QR-код для підключення пристрою" />}
              <p className="field-hint">Дійсний до {formatDateTime(pairing.expiresAt, ward.timezone)}</p>
              <p className="field-hint">{pairing.pairingURL}</p>
            </div>
          )}
        </article>

        <article className="card">
          <h2>PIN режиму помічника</h2>
          <p>
            Від 4 до 6 цифр. Дозволяє відкрити керування просто на пристрої підопічного.
            Після п’яти помилок вхід блокується на 15 хвилин, а головний помічник отримує сповіщення.
          </p>
          <p className="field-hint">
            Щоб відкрити цей режим, утримуйте ім’я підопічного вгорі його екрана дві секунди. Кнопки там
            немає навмисно: підопічний не повинен натрапити на неї випадково.
          </p>
          <Field label="Новий PIN">
            <input
              value={pin}
              inputMode="numeric"
              maxLength={6}
              disabled={!editable}
              onChange={(event) => setPin(event.target.value.replace(/\D/g, '').slice(0, 6))}
            />
          </Field>
          <button type="button" onClick={() => void savePIN()} disabled={!editable || pin.length < 4}>
            Зберегти PIN
          </button>
        </article>
      </div>

      <h3>Підключені пристрої</h3>
      {devices.length === 0 ? (
        <p className="field-hint">Поки що пристроїв немає.</p>
      ) : (
        <ul className="row-list">
          {devices.map((device) => (
            <li key={device.id} className="row">
              <div className="row-main">
                <strong>{device.label}</strong>
                <small>
                  {device.active
                    ? `активний, востаннє на зв’язку ${formatAgo(device.lastActiveAt, ward.timezone) || 'щойно'}`
                    : `відключено ${formatDateTime(device.revokedAt, ward.timezone)}`}
                </small>
              </div>
              {device.active && editable && (
                <div className="row-actions">
                  <button type="button" className="danger" onClick={() => void revoke(device)}>
                    Відключити
                  </button>
                </div>
              )}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
