import { useState } from 'react';
import { storeKey, parseSetupPayload } from '../lib/auth';
import { verifyAuth } from '../lib/api';

export function Setup({ onComplete }: { onComplete: () => void }) {
  const [input, setInput] = useState('');
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError('');
    setLoading(true);

    try {
      const parsed = parseSetupPayload(input.trim());
      if (!parsed) {
        setError('Invalid setup string. Expected format: helios://setup?key=...&kid=...');
        setLoading(false);
        return;
      }

      await storeKey(parsed.key, parsed.kid);

      const valid = await verifyAuth();
      if (!valid) {
        setError('Key stored but verification failed. The daemon may not be running.');
        setLoading(false);
        return;
      }

      onComplete();
    } catch (err) {
      setError(`Setup failed: ${err instanceof Error ? err.message : 'Unknown error'}`);
      setLoading(false);
    }
  }

  return (
    <div className="setup-page">
      <div className="setup-card">
        <h1>helios</h1>
        <p className="subtitle">Device Setup</p>

        <form onSubmit={handleSubmit}>
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
          <p>Run <code>helios auth init --name "My Device"</code> in your terminal to get a setup string.</p>
        </div>
      </div>
    </div>
  );
}
