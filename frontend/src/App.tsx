import { useEffect, useState } from 'react';
import { Setup } from './pages/Setup';
import { Dashboard } from './pages/Dashboard';
import { getDeviceMe } from './lib/api';

export function App() {
  const [route, setRoute] = useState<'loading' | 'setup' | 'dashboard'>('loading');

  useEffect(() => {
    const hash = window.location.hash;
    if (hash.includes('key=')) {
      setRoute('setup');
      return;
    }

    getDeviceMe().then((device) => {
      setRoute(device ? 'dashboard' : 'setup');
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
    return (
      <div className="flex items-center justify-center min-h-screen">
        <div className="flex items-center gap-3 text-muted-foreground">
          <svg className="animate-spin h-5 w-5" viewBox="0 0 24 24" fill="none">
            <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
            <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
          </svg>
          <span>Loading...</span>
        </div>
      </div>
    );
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
