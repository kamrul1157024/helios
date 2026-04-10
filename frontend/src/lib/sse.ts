import { getAuthHeader } from './auth';

export type SSEEventType =
  | 'notification'
  | 'notification_resolved'
  | 'session_created'
  | 'session_done'
  | 'session_error';

export interface SSECallback {
  (eventType: SSEEventType, data: unknown): void;
}

export function connectSSE(callback: SSECallback): () => void {
  let abortController = new AbortController();
  let reconnectTimeout: ReturnType<typeof setTimeout>;

  async function connect() {
    try {
      const authHeader = await getAuthHeader();

      const resp = await fetch('/api/events', {
        headers: { Authorization: authHeader },
        signal: abortController.signal,
      });

      if (!resp.ok || !resp.body) {
        scheduleReconnect();
        return;
      }

      const reader = resp.body.getReader();
      const decoder = new TextDecoder();
      let buffer = '';

      while (true) {
        const { done, value } = await reader.read();
        if (done) break;

        buffer += decoder.decode(value, { stream: true });
        const lines = buffer.split('\n');
        buffer = lines.pop() || '';

        let currentEvent = '';
        for (const line of lines) {
          if (line.startsWith('event: ')) {
            currentEvent = line.slice(7).trim();
          } else if (line.startsWith('data: ') && currentEvent) {
            try {
              const data = JSON.parse(line.slice(6));
              callback(currentEvent as SSEEventType, data);
            } catch {
              // ignore parse errors
            }
            currentEvent = '';
          }
        }
      }
    } catch (err) {
      if (abortController.signal.aborted) return;
    }

    scheduleReconnect();
  }

  function scheduleReconnect() {
    if (abortController.signal.aborted) return;
    reconnectTimeout = setTimeout(connect, 3000);
  }

  connect();

  return () => {
    abortController.abort();
    clearTimeout(reconnectTimeout);
  };
}
