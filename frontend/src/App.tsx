import { useEffect, useState } from 'react';
import { hasKey } from './lib/auth';
import { Setup } from './pages/Setup';
import { Dashboard } from './pages/Dashboard';

export function App() {
  const [route, setRoute] = useState<'loading' | 'setup' | 'dashboard'>('loading');

  useEffect(() => {
    hasKey().then((exists) => {
      if (exists) {
        setRoute('dashboard');
      } else {
        setRoute('setup');
      }
    });

    function onHashChange() {
      if (window.location.hash === '#/') {
        setRoute('setup');
      } else if (window.location.hash === '#/dashboard') {
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
