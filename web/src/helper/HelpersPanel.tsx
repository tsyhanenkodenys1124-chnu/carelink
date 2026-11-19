// Помічники профілю: права, запрошення та передача ролі головного.

import { useCallback, useEffect, useState } from 'react';
import { ApiError, CareLink, Invitation, Ward, api } from '../api';
import { formatDateTime } from '../format';
import { Checkbox, Dialog, ErrorText, Field, Loading, NoticeText } from '../components/ui';

export function HelpersPanel({ ward, onChanged }: { ward: Ward; onChanged: () => void }) {
  const [links, setLinks] = useState<CareLink[] | null>(null);
  const [invitations, setInvitations] = useState<Invitation[]>([]);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [inviting, setInviting] = useState(false);
  const [editing, setEditing] = useState<CareLink | null>(null);

  const load = useCallback(async () => {
    try {
      const list = await api<CareLink[]>(`/wards/${ward.id}/links`);
      setLinks(list);
      if (ward.rights.primary) {
        setInvitations(await api<Invitation[]>(`/wards/${ward.id}/invitations`));
      }
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Не вдалося отримати помічників.');
      setLinks([]);
    }
  }, [ward.id, ward.rights.primary]);

  useEffect(() => {
    setLinks(null);
    void load();
  }, [load]);

  const revoke = async (link: CareLink) => {
    if (!window.confirm(`Відкликати доступ у ${link.name}?`)) return;
    try {
      await api(`/wards/${ward.id}/links/${link.id}`, { method: 'DELETE' });
      await load();
      onChanged();
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Не вдалося відкликати доступ.');
    }
  };

  const makePrimary = async (link: CareLink) => {
    if (!window.confirm(`Передати роль головного помічника: ${link.name}? Ви втратите частину прав.`)) return;
    try {
      await api(`/wards/${ward.id}/links/${link.id}`, {
        method: 'PATCH',
        body: JSON.stringify({ makePrimary: true }),
      });
      await load();
      onChanged();
      setNotice('Роль головного помічника передано.');
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Не вдалося передати роль.');
    }
  };

  const cancelInvitation = async (invitation: Invitation) => {
    try {
      await api(`/wards/${ward.id}/invitations/${invitation.id}`, { method: 'DELETE' });
      await load();
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Не вдалося скасувати запрошення.');
    }
  };

  if (!links) return <Loading what="помічників" />;

  return (
    <section className="panel">
      <div className="panel-head">
        <div>
          <h2>Помічники</h2>
          <p>Усі запити «Потрібна допомога» та «Я не пам’ятаю» бачать усі підключені помічники.</p>
        </div>
        {ward.rights.primary && (
          <button type="button" className="primary" onClick={() => setInviting(true)}>
            Запросити помічника
          </button>
        )}
      </div>

      <ErrorText>{error}</ErrorText>
      <NoticeText>{notice}</NoticeText>

      <ul className="row-list">
        {links.map((link) => (
          <li key={link.id} className="row">
            <div className="row-main">
              <strong>
                {link.name}
                {link.rights.primary && <span className="tag">головний</span>}
              </strong>
              <small>
                {link.email} · {describeRights(link.rights)}
              </small>
            </div>
            {ward.rights.primary && !link.rights.primary && (
              <div className="row-actions">
                <button type="button" onClick={() => setEditing(link)}>
                  Права
                </button>
                <button type="button" onClick={() => void makePrimary(link)}>
                  Зробити головним
                </button>
                <button type="button" className="danger" onClick={() => void revoke(link)}>
                  Відкликати
                </button>
              </div>
            )}
          </li>
        ))}
      </ul>

      {ward.rights.primary && invitations.length > 0 && (
        <>
          <h3>Надіслані запрошення</h3>
          <ul className="row-list">
            {invitations.map((invitation) => (
              <li key={invitation.id} className="row">
                <div className="row-main">
                  <strong>{invitation.email || 'Без адреси — за посиланням'}</strong>
                  <small>діє до {formatDateTime(invitation.expiresAt, ward.timezone)}</small>
                </div>
                <div className="row-actions">
                  <button type="button" className="danger" onClick={() => void cancelInvitation(invitation)}>
                    Скасувати
                  </button>
                </div>
              </li>
            ))}
          </ul>
        </>
      )}

      {inviting && (
        <Dialog title="Запросити помічника" onClose={() => setInviting(false)}>
          <InviteForm
            wardID={ward.id}
            onDone={() => {
              setInviting(false);
              void load();
            }}
          />
        </Dialog>
      )}

      {editing && (
        <Dialog title={`Права: ${editing.name}`} onClose={() => setEditing(null)}>
          <RightsForm
            wardID={ward.id}
            link={editing}
            onDone={() => {
              setEditing(null);
              void load();
            }}
          />
        </Dialog>
      )}
    </section>
  );
}

function InviteForm({ wardID, onDone }: { wardID: string; onDone: () => void }) {
  const [email, setEmail] = useState('');
  const [editTasks, setEditTasks] = useState(true);
  const [editSettings, setEditSettings] = useState(false);
  const [escalations, setEscalations] = useState(true);
  const [link, setLink] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const submit = async () => {
    setBusy(true);
    setError('');
    try {
      const created = await api<{ url: string }>(`/wards/${wardID}/links`, {
        method: 'POST',
        body: JSON.stringify({ email, editTasks, editSettings, escalations }),
      });
      setLink(created.url);
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Не вдалося створити запрошення.');
    } finally {
      setBusy(false);
    }
  };

  if (link) {
    return (
      <div className="stack">
        <NoticeText>Запрошення створено. Воно діє 72 години.</NoticeText>
        <Field label="Посилання" hint="Якщо ви вказали адресу, ми також надіслали лист.">
          <input readOnly value={link} onFocus={(event) => event.currentTarget.select()} />
        </Field>
        <button type="button" className="primary" onClick={onDone}>
          Готово
        </button>
      </div>
    );
  }

  return (
    <div className="stack">
      <ErrorText>{error}</ErrorText>
      <Field label="Електронна пошта" hint="Не обов'язково: без адреси запрошення можна передати посиланням.">
        <input type="email" value={email} onChange={(event) => setEmail(event.target.value)} />
      </Field>
      <Checkbox label="Може змінювати завдання та відмічати виконання" checked={editTasks} onChange={setEditTasks} />
      <Checkbox label="Може змінювати налаштування екрана й потреби" checked={editSettings} onChange={setEditSettings} />
      <Checkbox label="Отримує ескалації критичних справ" checked={escalations} onChange={setEscalations} />
      <button type="button" className="primary" onClick={() => void submit()} disabled={busy}>
        {busy ? 'Створюємо…' : 'Створити запрошення'}
      </button>
    </div>
  );
}

function RightsForm({ wardID, link, onDone }: { wardID: string; link: CareLink; onDone: () => void }) {
  const [editTasks, setEditTasks] = useState(link.rights.editTasks);
  const [editSettings, setEditSettings] = useState(link.rights.editSettings);
  const [escalations, setEscalations] = useState(link.rights.escalations);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const submit = async () => {
    setBusy(true);
    setError('');
    try {
      await api(`/wards/${wardID}/links/${link.id}`, {
        method: 'PATCH',
        body: JSON.stringify({ editTasks, editSettings, escalations, makePrimary: false }),
      });
      onDone();
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Не вдалося зберегти права.');
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="stack">
      <ErrorText>{error}</ErrorText>
      <Checkbox label="Може змінювати завдання та відмічати виконання" checked={editTasks} onChange={setEditTasks} />
      <Checkbox label="Може змінювати налаштування екрана й потреби" checked={editSettings} onChange={setEditSettings} />
      <Checkbox label="Отримує ескалації критичних справ" checked={escalations} onChange={setEscalations} />
      <button type="button" className="primary" onClick={() => void submit()} disabled={busy}>
        {busy ? 'Зберігаємо…' : 'Зберегти права'}
      </button>
    </div>
  );
}

function describeRights(rights: CareLink['rights']): string {
  if (rights.primary) return 'усі права';
  const parts: string[] = ['перегляд'];
  if (rights.editTasks) parts.push('завдання');
  if (rights.editSettings) parts.push('налаштування');
  if (rights.escalations) parts.push('ескалації');
  return parts.join(', ');
}
