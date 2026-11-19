// Форма завдання: час, повторення, кроки, фото та параметри нагадувань.

import { FormEvent, useState } from 'react';
import { ApiError, Task, api, upload } from '../api';
import { weekdayNames } from '../format';
import { Checkbox, ErrorText, Field } from '../components/ui';

type Draft = {
  title: string;
  category: string;
  description: string;
  critical: boolean;
  localStartDate: string;
  localTime: string;
  recurrenceKind: Task['recurrenceKind'];
  recurrenceDays: number[];
  recurrenceInterval: number;
  completionWindowMinutes: number;
  escalationWindowMinutes: number;
  warningMinutes: number;
  reminderRepeats: number;
  reminderIntervalMinutes: number;
  steps: string[];
};

function draftFrom(task?: Task): Draft {
  if (task) {
    return {
      title: task.title,
      category: task.category,
      description: task.description,
      critical: task.critical,
      localStartDate: task.localStartDate,
      localTime: task.localTime,
      recurrenceKind: task.recurrenceKind,
      recurrenceDays: task.recurrenceDays ?? [],
      recurrenceInterval: task.recurrenceInterval || 1,
      completionWindowMinutes: task.completionWindowMinutes,
      escalationWindowMinutes: task.escalationWindowMinutes,
      warningMinutes: task.warningMinutes,
      reminderRepeats: task.reminderRepeats,
      reminderIntervalMinutes: task.reminderIntervalMinutes,
      steps: task.steps.length > 0 ? task.steps : [''],
    };
  }
  return {
    title: '',
    category: 'medicine',
    description: '',
    critical: false,
    localStartDate: new Date().toISOString().slice(0, 10),
    localTime: '09:00',
    recurrenceKind: 'daily',
    recurrenceDays: [],
    recurrenceInterval: 2,
    completionWindowMinutes: 120,
    escalationWindowMinutes: 30,
    warningMinutes: 10,
    reminderRepeats: 1,
    reminderIntervalMinutes: 10,
    steps: [''],
  };
}

export function TaskForm({ wardID, task, onDone }: { wardID: string; task?: Task; onDone: () => void }) {
  const [draft, setDraft] = useState<Draft>(() => draftFrom(task));
  const [photo, setPhoto] = useState<File | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const set = <K extends keyof Draft>(key: K, value: Draft[K]) => setDraft((current) => ({ ...current, [key]: value }));

  const toggleWeekday = (day: number) => {
    const has = draft.recurrenceDays.includes(day);
    set(
      'recurrenceDays',
      has ? draft.recurrenceDays.filter((item) => item !== day) : [...draft.recurrenceDays, day].sort(),
    );
  };

  const setStep = (index: number, value: string) => {
    const steps = [...draft.steps];
    steps[index] = value;
    set('steps', steps);
  };

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setBusy(true);
    setError('');
    const payload = {
      ...draft,
      steps: draft.steps.map((step) => step.trim()).filter(Boolean),
    };
    try {
      let taskID = task?.id ?? '';
      if (task) {
        await api(`/tasks/${task.id}`, { method: 'PATCH', body: JSON.stringify(payload) });
      } else {
        const created = await api<{ id: string }>(`/wards/${wardID}/tasks`, {
          method: 'POST',
          body: JSON.stringify(payload),
        });
        taskID = created.id;
      }
      if (photo && taskID) {
        await upload(`/tasks/${taskID}/photo`, photo);
      }
      onDone();
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Не вдалося зберегти завдання.');
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} className="stack">
      <ErrorText>{error}</ErrorText>

      <div className="grid-two">
        <Field label="Коротка назва" hint="До п'яти слів — так її легше прочитати з відстані.">
          <input value={draft.title} onChange={(event) => set('title', event.target.value)} required maxLength={80} />
        </Field>

        <Field label="Категорія">
          <select value={draft.category} onChange={(event) => set('category', event.target.value)}>
            <option value="medicine">Ліки</option>
            <option value="procedure">Процедура</option>
            <option value="visit">Візит</option>
            <option value="food">Їжа</option>
            <option value="other">Інше</option>
          </select>
        </Field>
      </div>

      <Field label="Пояснення для підопічного" hint="Одне-два речення простими словами. Не обов'язково.">
        <textarea
          rows={2}
          value={draft.description}
          onChange={(event) => set('description', event.target.value)}
          maxLength={300}
        />
      </Field>

      <div className="grid-two">
        <Field label="Дата початку">
          <input
            type="date"
            value={draft.localStartDate}
            onChange={(event) => set('localStartDate', event.target.value)}
            required
          />
        </Field>
        <Field label="Час">
          <input type="time" value={draft.localTime} onChange={(event) => set('localTime', event.target.value)} required />
        </Field>
      </div>

      <Field label="Повторення">
        <select
          value={draft.recurrenceKind}
          onChange={(event) => set('recurrenceKind', event.target.value as Draft['recurrenceKind'])}
        >
          <option value="once">Один раз</option>
          <option value="daily">Щодня</option>
          <option value="weekly">У вибрані дні тижня</option>
          <option value="interval">Кожні кілька днів</option>
        </select>
      </Field>

      {draft.recurrenceKind === 'weekly' && (
        <div className="weekday-row" role="group" aria-label="Дні тижня">
          {weekdayNames.map((name, day) => (
            <button
              type="button"
              key={name}
              className={draft.recurrenceDays.includes(day) ? 'weekday active' : 'weekday'}
              onClick={() => toggleWeekday(day)}
              aria-pressed={draft.recurrenceDays.includes(day)}
            >
              {name}
            </button>
          ))}
        </div>
      )}

      {draft.recurrenceKind === 'interval' && (
        <Field label="Кожні скільки днів" hint="Від 1 до 60.">
          <input
            type="number"
            min={1}
            max={60}
            value={draft.recurrenceInterval}
            onChange={(event) => set('recurrenceInterval', Number(event.target.value))}
          />
        </Field>
      )}

      <Checkbox
        label="Критична справа: якщо її не виконати вчасно, помічники отримають ескалацію"
        checked={draft.critical}
        onChange={(value) => set('critical', value)}
      />

      <details className="details-block">
        <summary>Кроки виконання (до десяти)</summary>
        <div className="stack">
          {draft.steps.map((step, index) => (
            <div className="step-row" key={index}>
              <span className="step-number">{index + 1}</span>
              <input
                value={step}
                onChange={(event) => setStep(index, event.target.value)}
                maxLength={120}
                placeholder="Наприклад: налийте склянку води"
              />
              <button
                type="button"
                className="icon-button"
                aria-label={`Прибрати крок ${index + 1}`}
                onClick={() => set('steps', draft.steps.filter((_, position) => position !== index))}
              >
                ×
              </button>
            </div>
          ))}
          {draft.steps.length < 10 && (
            <button type="button" onClick={() => set('steps', [...draft.steps, ''])}>
              Додати крок
            </button>
          )}
        </div>
      </details>

      <details className="details-block">
        <summary>Нагадування та вікна часу</summary>
        <div className="grid-two">
          <Field label="Попередити за, хв" hint="0 — не попереджати заздалегідь.">
            <input
              type="number"
              min={0}
              max={120}
              value={draft.warningMinutes}
              onChange={(event) => set('warningMinutes', Number(event.target.value))}
            />
          </Field>
          <Field label="Повторних нагадувань" hint="Не більше трьох.">
            <input
              type="number"
              min={0}
              max={3}
              value={draft.reminderRepeats}
              onChange={(event) => set('reminderRepeats', Number(event.target.value))}
            />
          </Field>
          <Field label="Інтервал нагадувань, хв">
            <input
              type="number"
              min={1}
              max={60}
              value={draft.reminderIntervalMinutes}
              onChange={(event) => set('reminderIntervalMinutes', Number(event.target.value))}
            />
          </Field>
          <Field label="Вікно виконання, хв" hint="Скільки часу справа лишається активною.">
            <input
              type="number"
              min={5}
              max={1440}
              value={draft.completionWindowMinutes}
              onChange={(event) => set('completionWindowMinutes', Number(event.target.value))}
            />
          </Field>
          <Field label="Вікно ескалації, хв" hint="Через скільки повідомити помічників про критичну справу.">
            <input
              type="number"
              min={5}
              max={720}
              value={draft.escalationWindowMinutes}
              onChange={(event) => set('escalationWindowMinutes', Number(event.target.value))}
            />
          </Field>
        </div>
      </details>

      <Field label="Фото ліків або предмета" hint="Допомагає впізнати потрібну упаковку. До 5 МБ.">
        <input
          type="file"
          accept="image/jpeg,image/png,image/webp"
          onChange={(event) => setPhoto(event.target.files?.[0] ?? null)}
        />
      </Field>
      {task?.photo && !photo && (
        <img className="task-photo-preview" src={`/api/media/${task.photo}`} alt="Поточне фото завдання" />
      )}

      <button className="primary" disabled={busy}>
        {busy ? 'Зберігаємо…' : task ? 'Зберегти зміни' : 'Створити завдання'}
      </button>
    </form>
  );
}
