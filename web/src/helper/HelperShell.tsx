// Робоче місце помічника: вибір підопічного, розділи та живі сигнали.

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { ApiError, Me, Signal, Ward, api } from '../api';
import { LiveEvent, playAlert, showDesktopNotification, subscribeToEvents } from '../events';
import { ErrorText, EmptyState, Loading } from '../components/ui';
import { Overview } from './Overview';
import { SignalsPanel } from './SignalsPanel';
import { SchedulePanel } from './SchedulePanel';
import { NeedsPanel } from './NeedsPanel';
import { AccessibilityPanel } from './AccessibilityPanel';
import { DevicesPanel } from './DevicesPanel';
import { HelpersPanel } from './HelpersPanel';
import { SuggestionsPanel } from './SuggestionsPanel';
import { ReportPanel } from './ReportPanel';
import { AuditPanel } from './AuditPanel';
import { ProfilePanel } from './ProfilePanel';
import { NewWardForm } from './NewWardForm';
import { WardProfilePanel } from './WardProfilePanel';
import { Dialog } from '../components/ui';

const tabs = [
  { id: 'огляд', label: 'Огляд' },
  { id: 'сигнали', label: 'Сигнали' },
  { id: 'розклад', label: 'Розклад' },
  { id: 'профіль', label: 'Профіль' },
  { id: 'потреби', label: 'Потреби' },
  { id: 'екран', label: 'Екран' },
  { id: 'пристрої', label: 'Пристрої' },
  { id: 'помічники', label: 'Помічники' },
  { id: 'поради', label: 'Поради' },
  { id: 'звіт', label: 'Звіт' },
  { id: 'журнал', label: 'Журнал' },
] as const;

type TabID = (typeof tabs)[number]['id'];

export function HelperShell({
  me,
  onSignOut,
  initialWardID,
  initialTab,
}: {
  me: Me;
  onSignOut: () => void;
  initialWardID?: string;
  initialTab?: string;
}) {
  const [wards, setWards] = useState<Ward[] | null>(null);
  const [selectedID, setSelectedID] = useState(initialWardID ?? '');
  const [tab, setTab] = useState<TabID>(normalizeTab(initialTab));
  const [error, setError] = useState('');
  const [signals, setSignals] = useState<Signal[]>([]);
  const [liveOnline, setLiveOnline] = useState(false);
  const [showNewWard, setShowNewWard] = useState(false);
  const [showProfile, setShowProfile] = useState(false);
  const seenSignals = useRef(new Set<string>());

  const loadWards = useCallback(async () => {
    try {
      const list = await api<Ward[]>('/wards');
      setWards(list);
      setSelectedID((current) => (current && list.some((w) => w.id === current) ? current : list[0]?.id ?? ''));
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : 'Не вдалося отримати список підопічних.');
    }
  }, []);

  const loadSignals = useCallback(async () => {
    try {
      const list = await api<Signal[]>('/signals');
      setSignals(list);
      for (const item of list) seenSignals.current.add(item.id);
    } catch {
      // Сигнали оновляться за таймером або наступною подією.
    }
  }, []);

  useEffect(() => {
    void loadWards();
    void loadSignals();
  }, [loadWards, loadSignals]);

  // Живі події: сигнал з'являється одразу, зі звуком і системним сповіщенням.
  useEffect(() => {
    const unsubscribe = subscribeToEvents(
      (event: LiveEvent) => {
        void loadSignals();
        void loadWards();
        if (event.urgent) {
          playAlert();
          showDesktopNotification(event.title, 'Відкрийте розділ «Сигнали».');
        }
      },
      (online) => setLiveOnline(online),
    );
    return unsubscribe;
  }, [loadSignals, loadWards]);

  // Запасне оновлення на випадок, якщо потік подій перервався.
  useEffect(() => {
    const timer = window.setInterval(() => {
      void loadSignals();
    }, 60_000);
    return () => window.clearInterval(timer);
  }, [loadSignals]);

  const ward = useMemo(() => wards?.find((item) => item.id === selectedID), [wards, selectedID]);
  const openSignals = signals.length;
  const urgentSignals = signals.filter((item) => item.type !== 'ескалація' || item.status === 'open').length;

  if (!wards) return <Loading what="список підопічних" />;

  return (
    <div className="shell">
      <header className="shell-head">
        <div className="shell-title">
          <p className="eyebrow">ДОГЛЯД · ПОМІЧНИК</p>
          <h1>{ward?.addressName ?? 'Мої підопічні'}</h1>
        </div>

        <div className="shell-actions">
          <span className={liveOnline ? 'live live-on' : 'live live-off'} title={liveOnline ? 'Живі сигнали увімкнено' : 'Немає потоку подій, оновлюємо раз на хвилину'}>
            {liveOnline ? 'Сигнали наживо' : 'Оновлення за таймером'}
          </span>
          <button type="button" className="text" onClick={() => setShowProfile(true)}>
            {me.name}
          </button>
          <button type="button" className="text" onClick={onSignOut}>
            Вийти
          </button>
          <button type="button" className="primary" onClick={() => setShowNewWard(true)}>
            Додати підопічного
          </button>
        </div>
      </header>

      <ErrorText>{error}</ErrorText>

      {!me.emailConfirmed && <ConfirmationReminder />}

      {openSignals > 0 && tab !== 'сигнали' && (
        <button type="button" className="signal-banner" onClick={() => setTab('сигнали')}>
          <strong>{signalWord(openSignals)}</strong>
          <span>{signals.slice(0, 2).map((item) => `${item.wardName}: ${item.title}`).join(' · ')}</span>
          <span className="signal-banner-go">Відкрити →</span>
        </button>
      )}

      {wards.length === 0 ? (
        <EmptyState
          title="Поки що немає профілів"
          hint="Створіть профіль підопічного, додайте розклад, а потім підключіть пристрій кодом або QR-кодом."
          action={
            <button type="button" className="primary" onClick={() => setShowNewWard(true)}>
              Створити профіль
            </button>
          }
        />
      ) : (
        <>
          {wards.length > 1 && (
            <nav className="ward-switch" aria-label="Підопічні">
              {wards.map((item) => (
                <button
                  type="button"
                  key={item.id}
                  className={item.id === selectedID ? 'ward-chip active' : 'ward-chip'}
                  onClick={() => setSelectedID(item.id)}
                >
                  {item.addressName}
                  {item.openSignals.total > 0 && <span className="badge">{item.openSignals.total}</span>}
                </button>
              ))}
            </nav>
          )}

          <nav className="tabs" aria-label="Розділи">
            {tabs.map((item) => (
              <button
                type="button"
                key={item.id}
                className={item.id === tab ? 'active' : ''}
                onClick={() => setTab(item.id)}
                aria-current={item.id === tab ? 'page' : undefined}
              >
                {item.label}
                {item.id === 'сигнали' && urgentSignals > 0 && <span className="badge">{urgentSignals}</span>}
              </button>
            ))}
          </nav>

          <main className="shell-body">
            {ward && (
              <>
                {tab === 'огляд' && <Overview ward={ward} signals={signals} onOpenSignals={() => setTab('сигнали')} />}
                {tab === 'сигнали' && <SignalsPanel signals={signals} onChanged={loadSignals} />}
                {tab === 'розклад' && <SchedulePanel ward={ward} />}
                {tab === 'профіль' && <WardProfilePanel ward={ward} onChanged={loadWards} />}
                {tab === 'потреби' && <NeedsPanel ward={ward} />}
                {tab === 'екран' && <AccessibilityPanel ward={ward} />}
                {tab === 'пристрої' && <DevicesPanel ward={ward} />}
                {tab === 'помічники' && <HelpersPanel ward={ward} onChanged={loadWards} />}
                {tab === 'поради' && <SuggestionsPanel ward={ward} />}
                {tab === 'звіт' && <ReportPanel ward={ward} />}
                {tab === 'журнал' && <AuditPanel ward={ward} />}
              </>
            )}
          </main>
        </>
      )}

      {showNewWard && (
        <Dialog title="Новий підопічний" onClose={() => setShowNewWard(false)}>
          <NewWardForm
            onCreated={(id) => {
              setShowNewWard(false);
              setSelectedID(id);
              void loadWards();
            }}
          />
        </Dialog>
      )}

      {showProfile && (
        <Dialog title="Профіль і сповіщення" onClose={() => setShowProfile(false)} wide>
          <ProfilePanel me={me} />
        </Dialog>
      )}
    </div>
  );
}

function ConfirmationReminder() {
  const [sent, setSent] = useState(false);
  const resend = async () => {
    try {
      await api('/auth/email/resend', { method: 'POST', body: '{}' });
      setSent(true);
    } catch {
      setSent(false);
    }
  };
  return (
    <p className="message message-warning" role="status">
      Адресу ще не підтверджено, тому листи-сповіщення можуть не надходити.{' '}
      {sent ? (
        <strong>Лист надіслано ще раз.</strong>
      ) : (
        <button type="button" className="text" onClick={() => void resend()}>
          Надіслати лист ще раз
        </button>
      )}
    </p>
  );
}

function signalWord(count: number): string {
  if (count === 1) return '1 відкритий сигнал';
  if (count < 5) return `${count} відкриті сигнали`;
  return `${count} відкритих сигналів`;
}

function normalizeTab(value?: string): TabID {
  if (value === 'signals' || value === 'сигнали') return 'сигнали';
  const known = tabs.find((item) => item.id === value);
  return known ? known.id : 'огляд';
}
