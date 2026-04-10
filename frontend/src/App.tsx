import { useEffect, useState } from 'react';
import { Setup } from './pages/Setup';
import { Dashboard } from './pages/Dashboard';
import { getDeviceMe } from './lib/api';

export function App() {
  const [route, setRoute] = useState<'loading' | 'setup' | 'dashboard'>('loading');

  useEffect(() => {
    // Check if URL has setup params (QR scan flow)
    const hash = window.location.hash;
    if (hash.includes('key=') && hash.includes('kid=')) {
      setRoute('setup');
      return;
    }

    // Check if we have a valid cookie by calling the API
    getDeviceMe().then((device) => {
      if (device) {
        setRoute('dashboard');
      } else {
        setRoute('setup');
      }
    });

    function onHashChange() {
      const h = window.location.hash;
      if (h.startsWith('#/setup')) {
        setRoute('setup');
      } else if (h === '#/dashboard') {
        setRoute('dashboard');
      }
    }

    window.addEventListener('hashchange', onHashChange);
    return () => window.removeEventListener('hashchange', onHashChange);
  }, []);

  if (route === 'loading') {
    return <div className="loading">Loading...</div>;
  }

  if (route === 'setup') {
    return (
      <Setup
        onComplete={() => {
          window.location.hash = '#/dashboard';
          setRoute('dashboard');
        }}
      />
    );
  }

  return <Dashboard />;
}
