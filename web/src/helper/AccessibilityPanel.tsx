// Налаштування екрана підопічного з живим попереднім переглядом.

import { useEffect, useState } from 'react';
import { AccessibilitySettings, ApiError, Ward, api } from '../api';
import { Checkbox, ErrorText, Field, Loading, NoticeText } from '../components/ui';

export function AccessibilityPanel({ ward }: { ward: Ward }) {
  const [settings, setSettings] = useState<AccessibilitySettings | null>(null);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let cancelled = false;
    setSettings(null);
    void api<AccessibilitySettings>(`/wards/${ward.id}/accessibility`)
      .then((data) => !cancelled && setSettings(data))
      .catch((cause: unknown) => {
        if (cancelled) return;
        setError(cause instanceof ApiError ? cause.message : 'Не вдалося отримати налаштування.');
      });
    return () => {
      cancelled = true;
    };
  }, [ward.id]);

  if (!settings) return <Loading what="налаштування екрана" />;
  const editable = ward.rights.editSettings;
  const set = <K extends keyof AccessibilitySettings>(key: K, value: AccessibilitySettings[K]) =>
    setSettings((current) => (current ? { ...current, [key]: value } : current));

  const save = async () => {
    setBusy(true);
    setError('');
    setNotice('');
    try {
      await api(`/wards/${ward.id}/accessibility`, { method: 'PUT', body: JSON.stringify(settings) });
      setNotice('Збережено. Пристрій підопічного застосує зміни протягом півхвилини.');
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Не вдалося зберегти налаштування.');
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="panel">
      <div className="panel-head">
        <div>
          <h2>Екран підопічного</h2>
          <p>Ліворуч — налаштування, праворуч — як виглядатиме екран.</p>
        </div>
      </div>

      <ErrorText>{error}</ErrorText>
      <NoticeText>{notice}</NoticeText>

      <div className="accessibility-layout">
        <div className="stack">
          <Field label="Масштаб тексту">
            <select
              value={settings.scale}
              disabled={!editable}
              onChange={(event) => set('scale', Number(event.target.value))}
            >
              <option value={100}>Звичайний — 100%</option>
              <option value={125}>Трохи більший — 125%</option>
              <option value={150}>Великий — 150%</option>
              <option value={200}>Дуже великий — 200%</option>
            </select>
          </Field>

          <Field label="Палітра">
            <select
              value={settings.palette}
              disabled={!editable}
              onChange={(event) => set('palette', event.target.value as AccessibilitySettings['palette'])}
            >
              <option value="standard">Звичайна</option>
              <option value="high-contrast">Висока контрастність</option>
              <option value="color-safe">Безпечна для розрізнення кольорів</option>
            </select>
          </Field>

          <Checkbox
            label="Великі кнопки"
            checked={settings.largeControls}
            disabled={!editable}
            onChange={(value) => set('largeControls', value)}
          />
          <Checkbox
            label="Шрифт, зручний для дислексії"
            checked={settings.dyslexicFont}
            disabled={!editable}
            onChange={(value) => set('dyslexicFont', value)}
          />
          <Checkbox
            label="Режим підказок: дата, день тижня та підтвердження приймання"
            checked={settings.dementiaMode}
            disabled={!editable}
            onChange={(value) => set('dementiaMode', value)}
          />
          <Checkbox
            label="Екранна лупа"
            checked={settings.magnifier}
            disabled={!editable}
            onChange={(value) => set('magnifier', value)}
          />
          {settings.magnifier && (
            <Field label="Збільшення лупи">
              <select
                value={settings.magnifierScale}
                disabled={!editable}
                onChange={(event) => set('magnifierScale', Number(event.target.value))}
              >
                <option value={2}>у 2 рази</option>
                <option value={3}>у 3 рази</option>
                <option value={4}>у 4 рази</option>
              </select>
            </Field>
          )}

          <Checkbox
            label="Озвучення тексту"
            checked={settings.speechEnabled}
            disabled={!editable}
            onChange={(value) => set('speechEnabled', value)}
          />
          {settings.speechEnabled && (
            <Field label={`Швидкість озвучення: ${settings.speechRate.toFixed(1)}`}>
              <input
                type="range"
                min={0.5}
                max={1.5}
                step={0.1}
                value={settings.speechRate}
                disabled={!editable}
                onChange={(event) => set('speechRate', Number(event.target.value))}
              />
            </Field>
          )}

          <details className="details-block">
            <summary>Тонке налаштування тексту</summary>
            <div className="grid-two">
              <Field label="Міжлітерний інтервал">
                <select
                  value={settings.letterSpacing}
                  disabled={!editable}
                  onChange={(event) => set('letterSpacing', event.target.value)}
                >
                  <option value="0">звичайний</option>
                  <option value="0.05em">трохи ширший</option>
                  <option value="0.12em">широкий</option>
                </select>
              </Field>
              <Field label="Міжслівний інтервал">
                <select
                  value={settings.wordSpacing}
                  disabled={!editable}
                  onChange={(event) => set('wordSpacing', event.target.value)}
                >
                  <option value="0">звичайний</option>
                  <option value="0.16em">ширший</option>
                  <option value="0.3em">широкий</option>
                </select>
              </Field>
              <Field label="Висота рядка">
                <select
                  value={settings.lineHeight}
                  disabled={!editable}
                  onChange={(event) => set('lineHeight', event.target.value)}
                >
                  <option value="1.4">щільна</option>
                  <option value="1.5">звичайна</option>
                  <option value="1.8">простора</option>
                </select>
              </Field>
            </div>
          </details>

          {editable && (
            <button type="button" className="primary" onClick={() => void save()} disabled={busy}>
              {busy ? 'Зберігаємо…' : 'Зберегти налаштування'}
            </button>
          )}
        </div>

        <Preview settings={settings} wardName={ward.addressName} />
      </div>
    </section>
  );
}

/** Показує, як виглядатиме екран підопічного з поточними налаштуваннями. */
function Preview({ settings, wardName }: { settings: AccessibilitySettings; wardName: string }) {
  return (
    <aside className="preview" aria-label="Попередній перегляд екрана підопічного">
      <p className="preview-caption">Так побачить екран {wardName}</p>
      <div
        className={`preview-screen palette-${settings.palette} ${settings.dyslexicFont ? 'font-dyslexic' : ''}`}
        style={{
          fontSize: `${settings.scale}%`,
          letterSpacing: settings.letterSpacing,
          wordSpacing: settings.wordSpacing,
          lineHeight: settings.lineHeight,
        }}
      >
        {settings.dementiaMode && <p className="preview-date">субота, 27 вересня · 08:00</p>}
        <p className="preview-label">Зараз</p>
        <h3>Ранкові ліки</h3>
        <p className="preview-hint">Одна таблетка зі склянкою води.</p>
        <button type="button" className={settings.largeControls ? 'preview-main large' : 'preview-main'}>
          Прийняв ліки
        </button>
        {settings.speechEnabled && <p className="preview-hint">🔊 Текст можна прослухати</p>}
        <button type="button" className="preview-help">
          Потрібна допомога
        </button>
      </div>
    </aside>
  );
}
