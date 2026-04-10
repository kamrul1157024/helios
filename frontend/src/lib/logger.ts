/**
 * Device logger — captures errors and sends them to the daemon.
 * Batches log entries and flushes periodically or on page unload.
 */

type LogLevel = 'error' | 'warn' | 'info';

interface LogEntry {
  level: LogLevel;
  message: string;
  context?: string;
}

const buffer: LogEntry[] = [];
let flushTimer: ReturnType<typeof setTimeout> | null = null;

function enqueue(level: LogLevel, message: string, context?: string) {
  buffer.push({ level, message, context });
  if (!flushTimer) {
    flushTimer = setTimeout(flush, 2000);
  }
}

async function flush() {
  flushTimer = null;
  if (buffer.length === 0) return;

  const logs = buffer.splice(0, buffer.length);
  try {
    await fetch('/api/device/logs', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'same-origin',
      body: JSON.stringify({ logs }),
    });
  } catch {
    // Can't log the logging failure — just drop it
  }
}

export const deviceLog = {
  error(message: string, context?: string) {
    enqueue('error', message, context);
  },
  warn(message: string, context?: string) {
    enqueue('warn', message, context);
  },
  info(message: string, context?: string) {
    enqueue('info', message, context);
  },
  flush,
};

// Flush on page unload
if (typeof window !== 'undefined') {
  window.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'hidden') {
      flush();
    }
  });
}
