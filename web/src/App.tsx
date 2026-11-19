// Кореневий компонент: обирає режим за адресою та станом входу.

import { useCallback, useEffect, useState } from 'react';
import { ApiError, Me, api, deviceToken, forgetDevice } from './api';
import { AuthScreen } from './auth/AuthScreen';
import { AcceptInvitationScreen, ConfirmEmailScreen, ResetPasswordScreen } from './auth/TokenScreens';
import { HelperShell } from './helper/HelperShell';
import { WardScreen } from './ward/WardScreen';
import { Loading } from './components/ui';

type Route = {
  path: string;
  token: string;
  code: string;
  ward: string;
  tab: string;
};

function readRoute(): Route {
  const url = new URL(window.location.href);
  return {
    path: url.pathname,
    token: url.searchParams.get('token') ?? '',
    code: url.searchParams.get('code') ?? '',
    ward: url.searchParams.get('ward') ?? '',
    tab: url.searchParams.get('tab') ?? '',
  };
}

/** clearQuery прибирає одноразові параметри з адреси, щоб їх не було видно після дії. */
function clearQuery(): void {
  window.history.replaceState({}, '', window.location.pathname === '/' ? '/' : '/');
}

export default function App() {
  const [route, setRoute] = useState<Route>(readRoute);
  const [me, setMe] = useState<Me | null>(null);
  const [checked, setChecked] = useState(false);
  const [isDevice, setIsDevice] = useState(Boolean(deviceToken()));

  const loadMe = useCallback(async () => {
    try {
      setMe(await api<Me>('/me'));
    } catch (cause) {
      if (!(cause instanceof ApiError) || cause.status !== 401) {
        // Мережеву помилку показує вже сам екран входу.
      }
      setMe(null);
    } finally {
      setChecked(true);
    }
  }, []);

  useEffect(() => {
    if (isDevice) {
      setChecked(true);
      return;
    }
    void loadMe();
  }, [isDevice, loadMe]);

  const signOut = async () => {
    try {
      await api('/auth/logout', { method: 'POST', body: '{}' });
    } catch {
      // Навіть якщо запит не пройшов, локально виходимо.
    }
    setMe(null);
  };

  // Пристрій підопічного має пріоритет: на ньому інтерфейс помічника не відкривається.
  if (isDevice) {
    return (
      <WardScreen
        onDisconnect={() => {
          forgetDevice();
          setIsDevice(false);
          setRoute(readRoute());
        }}
      />
    );
  }

  if (!checked) return <Loading what="застосунок" />;

  if (route.path === '/reset' && route.token) {
    return (
      <ResetPasswordScreen
        token={route.token}
        onDone={() => {
          clearQuery();
          setRoute({ ...readRoute(), path: '/' });
        }}
      />
    );
  }

  if (route.path === '/confirm' && route.token) {
    return (
      <ConfirmEmailScreen
        token={route.token}
        onDone={() => {
          clearQuery();
          setRoute({ ...readRoute(), path: '/' });
          void loadMe();
        }}
      />
    );
  }

  if (route.path === '/invite' && route.token) {
    return (
      <AcceptInvitationScreen
        token={route.token}
        signedIn={Boolean(me)}
        onAccepted={() => {
          clearQuery();
          setRoute({ ...readRoute(), path: '/' });
          void loadMe();
        }}
        onNeedSignIn={() => setRoute({ ...route, path: '/' })}
      />
    );
  }

  if (!me) {
    return (
      <AuthScreen
        initialMode={route.path === '/pair' || route.code ? 'pair' : undefined}
        initialCode={route.code}
        onSignedIn={() => {
          clearQuery();
          void loadMe();
        }}
        onDevicePaired={() => {
          clearQuery();
          setIsDevice(true);
        }}
      />
    );
  }

  return <HelperShell me={me} onSignOut={() => void signOut()} initialWardID={route.ward} initialTab={route.tab} />;
}
