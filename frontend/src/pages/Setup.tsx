import { useEffect, useState } from 'react';
import { storeKey, signJWT, getDeviceId, getPublicKeyBase64 } from '../lib/auth';
import { pairDevice, login, updateDeviceMe } from '../lib/api';
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';

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

  useEffect(() => {
    const hash = window.location.hash;
    // New format: just key, no kid
    const match = hash.match(/[?&]key=([^&]+)/);
    if (match) {
      autoSetup(match[1]);
    }
  }, []);

  async function autoSetup(key: string) {
    setStep('importing');
    setStatusMessages([]);

    try {
      setStatusMessages(prev => [...prev, 'Importing key...']);
      await storeKey(key);
      setStatusMessages(prev => [...prev, 'Key imported']);

      setStatusMessages(prev => [...prev, 'Registering device...']);
      const kid = await getDeviceId();
      const publicKey = await getPublicKeyBase64();

      const pairResult = await pairDevice(kid, publicKey);
      if (!pairResult.success) {
        if (pairResult.error === 'key_already_claimed') {
          throw new Error(pairResult.message || 'This QR code has already been used by another device. Generate a new QR from the terminal with: helios start');
        }
        throw new Error(pairResult.message || 'Failed to register device');
      }
      setStatusMessages(prev => [...prev, 'Device registered']);

      setStatusMessages(prev => [...prev, 'Authenticating...']);
      const jwt = await signJWT();
      const loginResult = await login(jwt);
      if (!loginResult.success) {
        throw new Error('Login failed — device may be revoked');
      }
      setStatusMessages(prev => [...prev, 'Authenticated']);

      setStatusMessages(prev => [...prev, 'Detecting device...']);
      const platform = detectPlatform();
      const browser = detectBrowser();
      const defaultName = `${platform} — ${browser}`;
      setDeviceName(defaultName);

      await updateDeviceMe(defaultName, platform, browser);
      setStatusMessages(prev => [...prev, 'Setup complete']);

      window.location.hash = '#/dashboard';
      onComplete();
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
      const key = parseKeyParam(input.trim());
      if (!key) {
        setError('Invalid setup string. Paste the URL or key from the terminal QR code.');
        setLoading(false);
        return;
      }
      await autoSetup(key);
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

  if (step === 'importing') {
    return (
      <div className="flex items-center justify-center min-h-screen p-4">
        <Card className="w-full max-w-md">
          <CardHeader>
            <CardTitle className="text-2xl">helios</CardTitle>
            <CardDescription>Setting up device...</CardDescription>
          </CardHeader>
          <CardContent>
            <div className="space-y-2">
              {statusMessages.map((msg, i) => (
                <div key={i} className="flex items-center gap-2 text-sm font-mono">
                  <span className="text-primary">+</span>
                  <span className="text-muted-foreground">{msg}</span>
                </div>
              ))}
              <div className="flex items-center gap-2 text-sm">
                <svg className="animate-spin h-4 w-4 text-primary" viewBox="0 0 24 24" fill="none">
                  <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                  <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
                </svg>
                <span className="text-muted-foreground">Working...</span>
              </div>
            </div>
          </CardContent>
        </Card>
      </div>
    );
  }

  if (step === 'naming') {
    return (
      <div className="flex items-center justify-center min-h-screen p-4">
        <Card className="w-full max-w-md">
          <CardHeader>
            <CardTitle className="text-2xl">helios</CardTitle>
            <CardDescription>Connected! Name this device.</CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="space-y-2">
              <label htmlFor="device-name" className="text-sm text-muted-foreground">
                Device name
              </label>
              <Input
                id="device-name"
                value={deviceName}
                onChange={(e) => setDeviceName(e.target.value)}
                placeholder="My iPhone"
                autoFocus
              />
            </div>
            <Button onClick={handleSaveName} className="w-full">
              Save & Continue
            </Button>
          </CardContent>
        </Card>
      </div>
    );
  }

  if (step === 'error') {
    const isKeyClaimedError = error.includes('already been used') || error.includes('key_already_claimed');

    return (
      <div className="flex items-center justify-center min-h-screen p-4">
        <Card className="w-full max-w-md">
          <CardHeader>
            <CardTitle className="text-2xl">helios</CardTitle>
            <CardDescription>Setup Failed</CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            {error && (
              <div className="rounded-lg bg-destructive/10 border border-destructive/30 p-3 text-sm text-destructive">
                {error}
              </div>
            )}
            {isKeyClaimedError && (
              <div className="rounded-lg bg-muted p-3 text-sm text-muted-foreground">
                <p className="font-medium mb-1">What happened?</p>
                <p>Another device already scanned this QR code. Each QR code can only be used by one device.</p>
                <p className="mt-2">Run <code className="rounded bg-background px-1.5 py-0.5 text-xs">helios start</code> in your terminal to generate a new QR code.</p>
              </div>
            )}
            <div className="space-y-1">
              {statusMessages.map((msg, i) => (
                <p key={i} className="text-sm font-mono text-muted-foreground">
                  {msg}
                </p>
              ))}
            </div>
            <Button
              variant="outline"
              className="w-full"
              onClick={() => { setStep('input'); setError(''); setStatusMessages([]); }}
            >
              Try Again
            </Button>
          </CardContent>
        </Card>
      </div>
    );
  }

  return (
    <div className="flex items-center justify-center min-h-screen p-4">
      <Card className="w-full max-w-md">
        <CardHeader>
          <CardTitle className="text-2xl">helios</CardTitle>
          <CardDescription>Device Setup</CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={handleManualSubmit} className="space-y-4">
            <div className="space-y-2">
              <label htmlFor="setup-input" className="text-sm text-muted-foreground">
                Paste your setup URL from the terminal:
              </label>
              <Input
                id="setup-input"
                value={input}
                onChange={(e) => setInput(e.target.value)}
                placeholder="https://.../#/setup?key=..."
                autoFocus
                disabled={loading}
                className="font-mono text-sm"
              />
            </div>

            {error && (
              <div className="rounded-lg bg-destructive/10 border border-destructive/30 p-3 text-sm text-destructive">
                {error}
              </div>
            )}

            <Button type="submit" disabled={loading || !input.trim()} className="w-full">
              {loading ? 'Setting up...' : 'Connect'}
            </Button>
          </form>

          <div className="mt-6 pt-6 border-t text-sm text-muted-foreground">
            <p>
              Run <code className="rounded bg-muted px-1.5 py-0.5 text-xs">helios start</code> in
              your terminal to get a QR code, or paste the setup URL above.
            </p>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}

// Parse the key from various input formats:
// - Full URL: https://example.com/#/setup?key=abc123
// - helios URL: helios://setup?key=abc123
// - Just the key: abc123
function parseKeyParam(input: string): string | null {
  // Try parsing as URL with hash
  try {
    if (input.includes('#')) {
      const hashPart = input.split('#')[1];
      const params = new URLSearchParams(hashPart.includes('?') ? hashPart.split('?')[1] : '');
      const key = params.get('key');
      if (key) return key;
    }
  } catch {
    // Not a URL with hash
  }

  // Try parsing as URL
  try {
    const url = new URL(input);
    const key = url.searchParams.get('key');
    if (key) return key;
  } catch {
    // Not a URL
  }

  // Try parsing as query string
  try {
    const params = new URLSearchParams(input.includes('?') ? input.split('?')[1] : input);
    const key = params.get('key');
    if (key) return key;
  } catch {
    // Ignore
  }

  // If it looks like a base64url string (the raw key), accept it directly
  if (/^[A-Za-z0-9_-]{20,}$/.test(input)) {
    return input;
  }

  return null;
}
