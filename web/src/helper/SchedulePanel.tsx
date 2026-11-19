// Розклад підопічного: виконання на найближчі дні та керування завданнями.

import { useCallback, useEffect, useState } from 'react';
import { ApiError, Occurrence, Task, Ward, api } from '../api';
import { categoryNames, formatDateTime, secondsLeft, weekdayNames } from '../format';
import { Dialog, EmptyState, ErrorText, Loading, NoticeText, StatusPill } from '../components/ui';
import { TaskForm } from './TaskForm';

export function SchedulePanel({ ward }: { ward: Ward }) {
  const [occurrences, setOccurrences] = useState<Occurrence[] | null>(null);
  const [tasks, setTasks] = useState<Task[] | null>(null);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [editing, setEditing] = useState<Task | null>(null);
  const [creating, setCreating] = useState(false);
  const [view, setView] = useState<'виконання' | 'завдання'>('виконання');

  const load = useCallback(async () => {
    setError('');
    try {
      const [list, taskList] = await Promise.all([
        api<Occurrence[]>(`/occurrences?wardId=${ward.id}`),
        api<Task[]>(`/wards/${ward.id}/tasks`),
      ]);
      setOccurrences(list);
      setTasks(taskList);
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Не вдалося отримати розклад.');
      setOccurrences([]);
      setTasks([]);
    }
  }, [ward.id]);

  useEffect(() => {
    setOccurrences(null);
    setTasks(null);
    void load();
  }, [load]);

  const mark = async (occurrence: Occurrence, status: 'done' | 'missed' | 'planned') => {
    setError('');
    try {
      await api(`/occurrences/${occurrence.id}/mark`, { method: 'POST', body: JSON.stringify({ status }) });
      setNotice(status === 'done' ? 'Відмітку збережено. Її можна скасувати протягом півтори хвилини.' : '');
      await load();
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Не вдалося змінити відмітку.');
    }
  };

  const undo = async (occurrence: Occurrence) => {
    setError('');
    try {
      await api(`/occurrences/${occurrence.id}/undo`, { method: 'POST', body: '{}' });
      setNotice('Відмітку скасовано.');
      await load();
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Не вдалося скасувати відмітку.');
    }
  };

  const removeTask = async (task: Task) => {
    if (!window.confirm(`Видалити завдання «${task.title}»? Майбутні виконання зникнуть, історія залишиться.`)) return;
    try {
      await api(`/tasks/${task.id}`, { method: 'DELETE' });
      await load();
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Не вдалося видалити завдання.');
    }
  };

  if (!occurrences || !tasks) return <Loading what="розклад" />;

  return (
    <section className="panel">
      <div className="panel-head">
        <div>
          <h2>Розклад</h2>
          <p>Зміни завдань діють лише для майбутніх виконань. Уже зроблені відмітки залишаються.</p>
        </div>
        {ward.rights.editTasks && (
          <button type="button" className="primary" onClick={() => setCreating(true)}>
            Додати завдання
          </button>
        )}
      </div>

      <ErrorText>{error}</ErrorText>
      <NoticeText>{notice}</NoticeText>

      <div className="switch-row" role="tablist">
        <button
          type="button"
          role="tab"
          aria-selected={view === 'виконання'}
          className={view === 'виконання' ? 'active' : ''}
          onClick={() => setView('виконання')}
        >
          Найближчі виконання
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={view === 'завдання'}
          className={view === 'завдання' ? 'active' : ''}
          onClick={() => setView('завдання')}
        >
          Завдання ({tasks.length})
        </button>
      </div>

      {view === 'виконання' &&
        (occurrences.length === 0 ? (
          <EmptyState
            title="Виконань поки немає"
            hint="Додайте завдання — і застосунок сам складе розклад на найближчі два тижні."
          />
        ) : (
          <ul className="row-list">
            {occurrences.map((item) => (
              <li key={item.id} className="row">
                <time dateTime={item.scheduledAt}>{formatDateTime(item.scheduledAt, ward.timezone)}</time>
                <div className="row-main">
                  <strong>{item.title}</strong>
                  <small>
                    {categoryNames[item.category] ?? item.category}
                    {item.critical ? ' · критична' : ''}
                    {item.markedBy ? ` · відмітив ${item.markedBy}` : ''}
                  </small>
                </div>
                <StatusPill status={item.status} />
                {ward.rights.editTasks && (
                  <div className="row-actions">
                    {item.status === 'planned' && (
                      <>
                        <button type="button" onClick={() => void mark(item, 'done')}>
                          Виконано
                        </button>
                        <button type="button" onClick={() => void mark(item, 'missed')}>
                          Пропущено
                        </button>
                      </>
                    )}
                    {item.status !== 'planned' && secondsLeft(item.undoUntil) > 0 && (
                      <button type="button" onClick={() => void undo(item)}>
                        Скасувати ({secondsLeft(item.undoUntil)} с)
                      </button>
                    )}
                    {item.status !== 'planned' && secondsLeft(item.undoUntil) === 0 && (
                      <button type="button" onClick={() => void mark(item, 'planned')}>
                        Повернути в план
                      </button>
                    )}
                  </div>
                )}
              </li>
            ))}
          </ul>
        ))}

      {view === 'завдання' &&
        (tasks.length === 0 ? (
          <EmptyState
            title="Завдань немає"
            hint="Додайте перше завдання: ліки, їжу, процедуру або візит."
            action={
              ward.rights.editTasks ? (
                <button type="button" className="primary" onClick={() => setCreating(true)}>
                  Додати завдання
                </button>
              ) : undefined
            }
          />
        ) : (
          <ul className="row-list">
            {tasks.map((task) => (
              <li key={task.id} className="row">
                <time>{task.localTime}</time>
                <div className="row-main">
                  <strong>{task.title}</strong>
                  <small>
                    {categoryNames[task.category] ?? task.category} · {describeRecurrence(task)}
                    {task.critical ? ' · критична' : ''}
                    {task.steps.length > 0 ? ` · ${task.steps.length} кроків` : ''}
                  </small>
                </div>
                {ward.rights.editTasks && (
                  <div className="row-actions">
                    <button type="button" onClick={() => setEditing(task)}>
                      Змінити
                    </button>
                    <button type="button" className="danger" onClick={() => void removeTask(task)}>
                      Видалити
                    </button>
                  </div>
                )}
              </li>
            ))}
          </ul>
        ))}

      {creating && (
        <Dialog title="Нове завдання" onClose={() => setCreating(false)} wide>
          <TaskForm
            wardID={ward.id}
            onDone={() => {
              setCreating(false);
              void load();
            }}
          />
        </Dialog>
      )}

      {editing && (
        <Dialog title="Змінити завдання" onClose={() => setEditing(null)} wide>
          <TaskForm
            wardID={ward.id}
            task={editing}
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

/** Опис повторення людською мовою. */
function describeRecurrence(task: Task): string {
  switch (task.recurrenceKind) {
    case 'daily':
      return 'щодня';
    case 'weekly':
      return `щотижня: ${task.recurrenceDays.map((day) => weekdayNames[day]).join(', ')}`;
    case 'interval':
      return `кожні ${task.recurrenceInterval} дн.`;
    default:
      return `один раз ${task.localStartDate}`;
  }
}
