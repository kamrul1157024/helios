import { useEffect, useState } from 'react';
import { storeKey, signJWT } from '../lib/auth';
import { login, updateDeviceMe } from '../lib/api';

function detectPlatform(): string {
  const ua = navigator.userAgent;
  if (/iPhone/.test(ua)) return 'iOS';
  if (/iPad/.test(ua)) return 'iPadOS';
  if (/Android/.test(ua)) return 'Android';
  if (/Mac OS/.test(ua)) return 'macOS';
  if (/Windows/.test(ua)) return 'Windows';
  if (/Linux/.test(ua)) return 'Linux';
  return 'Unknown';
}

function detectBrowser(): string {
  const ua = navigator.userAgent;
  if (/CriOS/.test(ua)) return 'Chrome (iOS)';
  if (/FxiOS/.test(ua)) return 'Firefox (iOS)';
  if (/EdgiOS/.test(ua) || /Edg\//.test(ua)) return 'Edge';
  if (/Chrome\/(\d+)/.test(ua)) return `Chrome ${ua.match(/Chrome\/(\d+)/)?.[1]}`;
  if (/Safari\//.test(ua) && !/Chrome/.test(ua)) return `Safari ${ua.match(/Version\/(\d+)/)?.[1] || ''}`.trim();
  if (/Firefox\/(\d+)/.test(ua)) return `Firefox ${ua.match(/Firefox\/(\d+)/)?.[1]}`;
  return 'Unknown';
}

type SetupStep = 'input' | 'importing' | 'naming' | 'done' | 'error';

export function Setup({ onComplete }: { onComplete: () => void }) {
  const [input, setInput] = useState('');
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const [step, setStep] = useState<SetupStep>('input');
  const [deviceName, setDeviceName] = useState('');
  const [statusMessages, setStatusMessages] = useState<string[]>([]);

  // Check URL params for auto-import (QR scan flow)
  useEffect(() => {
    const hash = window.location.hash;
    const match = hash.match(/[?&]key=([^&]+).*[?&]kid=([^&]+)/);
    if (match) {
      autoSetup(match[1], match[2]);
    }
  }, []);

  async function autoSetup(key: string, kid: string) {
    setStep('importing');
    setStatusMessages([]);

    try {
      // 1. Store Ed25519 private key in IndexedDB
      setStatusMessages(prev => [...prev, 'Importing key...']);
      await storeKey(key, kid);
      setStatusMessages(prev => [...prev, '✓ Key imported']);

      // 2. Sign JWT and login (sets HttpOnly cookie)
      setStatusMessages(prev => [...prev, 'Authenticating...']);
      const jwt = await signJWT();
      const result = await login(jwt);
      if (!result.success) {
        throw new Error('Login failed — device may be revoked');
      }
      setStatusMessages(prev => [...prev, '✓ Authenticated']);

      // 3. Auto-detect device metadata
      setStatusMessages(prev => [...prev, 'Detecting device...']);
      const platform = detectPlatform();
      const browser = detectBrowser();
      const defaultName = `${platform} — ${browser}`;
      setDeviceName(defaultName);

      // 4. Send initial metadata
      await updateDeviceMe(defaultName, platform, browser);
      setStatusMessages(prev => [...prev, '✓ Device registered']);

      // 5. Show naming step
      setStep('naming');

      // Clean up URL params
      window.location.hash = '#/setup';
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Setup failed');
      setStep('error');
    }
  }

  async function handleManualSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError('');
    setLoading(true);

    try {
      // Parse helios://setup?key=...&kid=... or just key=...&kid=...
      const parsed = parseKeyParams(input.trim());
      if (!parsed) {
        setError('Invalid setup string. Expected: helios://setup?key=...&kid=...');
        setLoading(false);
        return;
      }

      await autoSetup(parsed.key, parsed.kid);
    } catch (err) {
      setError(`Setup failed: ${err instanceof Error ? err.message : 'Unknown error'}`);
      setLoading(false);
    }
  }

  async function handleSaveName() {
    if (deviceName.trim()) {
      const platform = detectPlatform();
      const browser = detectBrowser();
      await updateDeviceMe(deviceName.trim(), platform, browser);
    }
    setStep('done');
    onComplete();
  }

  // Auto-import step
  if (step === 'importing') {
    return (
      <div className="setup-page">
        <div className="setup-card">
          <h1>helios</h1>
          <p className="subtitle">Setting up device...</p>
          <div style={{ marginTop: '1.5rem' }}>
            {statusMessages.map((msg, i) => (
              <p key={i} style={{ marginBottom: '0.5rem', fontFamily: 'monospace', fontSize: '0.875rem' }}>
                {msg}
              </p>
            ))}
          </div>
        </div>
      </div>
    );
  }

  // Device naming step
  if (step === 'naming') {
    return (
      <div className="setup-page">
        <div className="setup-card">
          <h1>helios</h1>
          <p className="subtitle">Connected!</p>

          <div className="input-group">
            <label htmlFor="device-name">Name this device:</label>
            <input
              id="device-name"
              type="text"
              value={deviceName}
              onChange={(e) => setDeviceName(e.target.value)}
              placeholder="My iPhone"
              autoFocus
            />
          </div>

          <button onClick={handleSaveName}>
            Save & Continue
          </button>
        </div>
      </div>
    );
  }

  // Error step
  if (step === 'error') {
    return (
      <div className="setup-page">
        <div className="setup-card">
          <h1>helios</h1>
          <p className="subtitle">Setup Failed</p>

          {error && <div className="error">{error}</div>}

          <div style={{ marginTop: '1rem' }}>
            {statusMessages.map((msg, i) => (
              <p key={i} style={{ marginBottom: '0.5rem', fontFamily: 'monospace', fontSize: '0.875rem' }}>
                {msg}
              </p>
            ))}
          </div>

          <button onClick={() => { setStep('input'); setError(''); setStatusMessages([]); }}>
            Try Again
          </button>
        </div>
      </div>
    );
  }

  // Manual input step (default — for when QR scan isn't used)
  return (
    <div className="setup-page">
      <div className="setup-card">
        <h1>helios</h1>
        <p className="subtitle">Device Setup</p>

        <form onSubmit={handleManualSubmit}>
          <div className="input-group">
            <label htmlFor="setup-input">Paste your setup string from the terminal:</label>
            <input
              id="setup-input"
              type="text"
              value={input}
              onChange={(e) => setInput(e.target.value)}
              placeholder="helios://setup?key=...&kid=..."
              autoFocus
              disabled={loading}
            />
          </div>

          {error && <div className="error">{error}</div>}

          <button type="submit" disabled={loading || !input.trim()}>
            {loading ? 'Setting up...' : 'Connect'}
          </button>
        </form>

        <div className="help-text">
          <p>Run <code>helios auth init</code> in your terminal to get a setup string, or scan the QR code.</p>
        </div>
      </div>
    </div>
  );
}

function parseKeyParams(input: string): { key: string; kid: string } | null {
  try {
    // Try as URL (helios://setup?key=...&kid=... or https://.../#/setup?key=...&kid=...)
    const url = new URL(input);
    const key = url.searchParams.get('key');
    const kid = url.searchParams.get('kid');
    if (key && kid) return { key, kid };
  } catch {
    // Not a URL — try as query string
  }

  // Try to parse as query params
  try {
    const params = new URLSearchParams(input.includes('?') ? input.split('?')[1] : input);
    const key = params.get('key');
    const kid = params.get('kid');
    if (key && kid) return { key, kid };
  } catch {
    // Ignore
  }

  return null;
}
