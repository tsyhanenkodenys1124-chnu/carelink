// Спільні елементи інтерфейсу помічника.

import { ReactNode, useEffect, useRef } from 'react';

/** Повідомлення про помилку з роллю alert, щоб його прочитала програма озвучення. */
export function ErrorText({ children }: { children: ReactNode }) {
  if (!children) return null;
  return (
    <p className="message message-error" role="alert">
      {children}
    </p>
  );
}

/** Підтвердження успішної дії. */
export function NoticeText({ children }: { children: ReactNode }) {
  if (!children) return null;
  return (
    <p className="message message-notice" role="status">
      {children}
    </p>
  );
}

/** Порожній стан розділу з поясненням, що робити далі. */
export function EmptyState({ title, hint, action }: { title: string; hint: string; action?: ReactNode }) {
  return (
    <section className="empty-state">
      <h3>{title}</h3>
      <p>{hint}</p>
      {action}
    </section>
  );
}

/** Модальне вікно з поверненням фокуса та закриттям за Escape. */
export function Dialog({
  title,
  onClose,
  children,
  wide,
}: {
  title: string;
  onClose: () => void;
  children: ReactNode;
  wide?: boolean;
}) {
  const panel = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    panel.current?.focus();
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onClose();
    };
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('keydown', onKey);
      previous?.focus();
    };
  }, [onClose]);

  return (
    <div className="backdrop" onMouseDown={(event) => event.target === event.currentTarget && onClose()}>
      <div
        className={wide ? 'dialog dialog-wide' : 'dialog'}
        role="dialog"
        aria-modal="true"
        aria-label={title}
        tabIndex={-1}
        ref={panel}
      >
        <header className="dialog-head">
          <h2>{title}</h2>
          <button type="button" className="icon-button" onClick={onClose} aria-label="Закрити вікно">
            ×
          </button>
        </header>
        <div className="dialog-body">{children}</div>
      </div>
    </div>
  );
}

/** Поле форми з підписом. */
export function Field({
  label,
  hint,
  children,
}: {
  label: string;
  hint?: string;
  children: ReactNode;
}) {
  return (
    <label className="field">
      <span className="field-label">{label}</span>
      {children}
      {hint && <span className="field-hint">{hint}</span>}
    </label>
  );
}

/** Прапорець із підписом. */
export function Checkbox({
  label,
  checked,
  onChange,
  disabled,
}: {
  label: string;
  checked: boolean;
  onChange: (value: boolean) => void;
  disabled?: boolean;
}) {
  return (
    <label className="checkbox">
      <input
        type="checkbox"
        checked={checked}
        disabled={disabled}
        onChange={(event) => onChange(event.target.checked)}
      />
      <span>{label}</span>
    </label>
  );
}

/** Кольорова позначка статусу виконання. */
export function StatusPill({ status }: { status: string }) {
  const labels: Record<string, string> = {
    planned: 'Заплановано',
    done: 'Виконано',
    missed: 'Пропущено',
  };
  return <span className={`pill pill-${status}`}>{labels[status] ?? status}</span>;
}

/** Показник завантаження розділу. */
export function Loading({ what = 'дані' }: { what?: string }) {
  return (
    <p className="loading" role="status">
      Завантажуємо {what}…
    </p>
  );
}
