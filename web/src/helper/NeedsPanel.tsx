// Профіль функціональних потреб. Діагнози не збираються.

import { useEffect, useState } from 'react';
import { AdaptationChange, ApiError, Ward, api } from '../api';
import { ErrorText, Loading, NoticeText } from '../components/ui';

type Answers = Record<string, string>;

const questions: { key: string; text: string; hint: string }[] = [
  { key: 'visionText', text: 'Важко читати звичайний текст', hint: 'Збільшимо шрифт на екрані підопічного.' },
  { key: 'visionColors', text: 'Погано розрізняє кольори', hint: 'Увімкнемо палітру, безпечну для кольорів.' },
  {
    key: 'hearing',
    text: 'Може не почути звуковий сигнал',
    hint: 'Екран підопічного і так не покладається на звук: усе видно написами.',
  },
  { key: 'motor', text: 'Важко влучати в невеликі кнопки', hint: 'Зробимо кнопки більшими.' },
  { key: 'dyslexia', text: 'Повільно читає або плутає літери', hint: 'Увімкнемо зручний шрифт і озвучення.' },
  { key: 'memory', text: 'Забуває зроблене або поточний день', hint: 'Покажемо дату та підтвердження приймання.' },
  { key: 'prefersAudio', text: 'Легше слухати, ніж читати', hint: 'Увімкнемо озвучення тексту.' },
];

const options = [
  { value: 'yes', label: 'Так' },
  { value: 'partly', label: 'Частково' },
  { value: 'no', label: 'Ні' },
  { value: 'skip', label: 'Пропустити' },
];

export function NeedsPanel({ ward }: { ward: Ward }) {
  const [answers, setAnswers] = useState<Answers | null>(null);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [changes, setChanges] = useState<AdaptationChange[] | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let cancelled = false;
    setAnswers(null);
    void api<Answers>(`/wards/${ward.id}/needs`)
      .then((data) => !cancelled && setAnswers(data ?? {}))
      .catch((cause: unknown) => {
        if (cancelled) return;
        setError(cause instanceof ApiError ? cause.message : 'Не вдалося отримати профіль потреб.');
        setAnswers({});
      });
    return () => {
      cancelled = true;
    };
  }, [ward.id]);

  const save = async () => {
    if (!answers) return;
    setBusy(true);
    setError('');
    setNotice('');
    setChanges(null);
    try {
      const applied = await api<{ changes: AdaptationChange[] }>(`/wards/${ward.id}/needs`, {
        method: 'PUT',
        body: JSON.stringify(answers),
      });
      setChanges(applied.changes);
      setNotice(
        applied.changes.length > 0
          ? 'Профіль збережено. Нижче видно, що саме змінилося на екрані підопічного.'
          : 'Профіль збережено. Відповіді не вимагають змінювати екран — усе лишилося як було.',
      );
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Не вдалося зберегти профіль.');
    } finally {
      setBusy(false);
    }
  };

  if (!answers) return <Loading what="профіль потреб" />;
  const editable = ward.rights.editSettings;

  return (
    <section className="panel">
      <div className="panel-head">
        <div>
          <h2>Профіль функціональних потреб</h2>
          <p>
            Це не медичний діагноз. Відповіді лише підбирають зручні налаштування екрана, і будь-яке з них
            можна змінити вручну.
          </p>
        </div>
      </div>

      <ErrorText>{error}</ErrorText>
      <NoticeText>{notice}</NoticeText>

      {changes && changes.length > 0 && (
        <section className="changes-card">
          <h3>Що застосунок змінив і чому</h3>
          <ul className="changes-list">
            {changes.map((change) => (
              <li key={change.setting}>
                <strong>
                  {change.setting}: {change.value}
                </strong>
                <small>{change.reason}</small>
              </li>
            ))}
          </ul>
          <p className="field-hint">Будь-що з цього можна змінити вручну в розділі «Екран».</p>
        </section>
      )}

      <ul className="needs-list">
        {questions.map((question) => (
          <li key={question.key}>
            <div className="needs-text">
              <strong>{question.text}</strong>
              <small>{question.hint}</small>
            </div>
            <div className="needs-options" role="group" aria-label={question.text}>
              {options.map((option) => (
                <button
                  type="button"
                  key={option.value}
                  disabled={!editable}
                  aria-pressed={(answers[question.key] || 'skip') === option.value}
                  className={(answers[question.key] || 'skip') === option.value ? 'chip active' : 'chip'}
                  onClick={() => setAnswers({ ...answers, [question.key]: option.value })}
                >
                  {option.label}
                </button>
              ))}
            </div>
          </li>
        ))}
      </ul>

      {editable ? (
        <button type="button" className="primary" onClick={() => void save()} disabled={busy}>
          {busy ? 'Зберігаємо…' : 'Зберегти та застосувати'}
        </button>
      ) : (
        <p className="field-hint">Змінювати профіль може помічник із правом на налаштування.</p>
      )}
    </section>
  );
}
