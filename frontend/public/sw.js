// helios service worker — SSE-based notifications
// Maintains a persistent SSE connection and shows local notifications.
// No FCM/Web Push dependency.

let sseAbort = null;

// Send log to daemon (best-effort, fire-and-forget)
function swLog(level, message) {
  fetch('/api/device/logs', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    credentials: 'same-origin',
    body: JSON.stringify({ logs: [{ level, message, context: 'sw' }] }),
  }).catch(() => {});
}

self.addEventListener('install', () => {
  swLog('info', 'sw install');
  self.skipWaiting();
});

self.addEventListener('activate', (event) => {
  swLog('info', 'sw activate — claiming clients');
  event.waitUntil(clients.claim());
  // Don't auto-start SSE here — wait for explicit message from frontend
});

// Listen for messages from the main page
self.addEventListener('message', (event) => {
  swLog('info', `sw message received: ${event.data}`);
  if (event.data === 'start-sse') {
    startSSE();
  } else if (event.data === 'stop-sse') {
    stopSSE();
  }
});

function stopSSE() {
  if (sseAbort) {
    sseAbort.abort();
    sseAbort = null;
    swLog('info', 'SSE stopped');
  }
}

async function startSSE() {
  // Don't start duplicate connections
  if (sseAbort) {
    swLog('info', 'SSE already running, skipping');
    return;
  }

  swLog('info', 'SSE starting');
  sseAbort = new AbortController();

  while (sseAbort && !sseAbort.signal.aborted) {
    try {
      swLog('info', 'SSE connecting to /api/events');
      const resp = await fetch('/api/events', {
        signal: sseAbort.signal,
        credentials: 'same-origin',
      });

      if (!resp.ok || !resp.body) {
        swLog('warn', `SSE connect failed: HTTP ${resp.status} ${resp.statusText}`);
        await sleep(3000);
        continue;
      }

      swLog('info', 'SSE connected');

      const reader = resp.body.getReader();
      const decoder = new TextDecoder();
      let buffer = '';

      while (true) {
        const { done, value } = await reader.read();
        if (done) {
          swLog('info', 'SSE stream ended');
          break;
        }

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
              swLog('info', `SSE event: ${currentEvent}`);
              handleSSEEvent(currentEvent, data);
            } catch {
              // ignore parse errors
            }
            currentEvent = '';
          }
        }
      }
    } catch (err) {
      if (sseAbort && sseAbort.signal.aborted) return;
      swLog('error', `SSE error: ${err}`);
    }

    // Reconnect after disconnect
    swLog('info', 'SSE reconnecting in 3s');
    await sleep(3000);
  }
}

function handleSSEEvent(type, data) {
  if (type === 'notification') {
    showNotification(data);
  }
}

async function showNotification(data) {
  // Don't show if the app tab is focused
  const windowClients = await clients.matchAll({ type: 'window', includeUncontrolled: true });
  const hasFocused = windowClients.some(c => c.visibilityState === 'visible');
  if (hasFocused) {
    swLog('info', 'notification skipped — tab is focused');
    return;
  }

  const options = {
    body: data.detail || data.tool_name || 'New notification',
    icon: '/icons/icon-192.png',
    badge: '/icons/icon-192.png',
    tag: data.id || 'helios-notification',
    renotify: true,
    requireInteraction: true,
    data: {
      id: data.id,
      type: data.type,
    },
    actions: [],
  };

  // Add approve/deny buttons for permission notifications
  if (data.type === 'permission' && data.id) {
    options.body = `${data.tool_name}: ${data.detail || data.tool_input || 'Permission requested'}`;
    options.actions = [
      { action: 'approve', title: 'Approve' },
      { action: 'deny', title: 'Deny' },
    ];
  }

  const title = data.type === 'permission' ? 'helios — Permission Request' : 'helios';
  swLog('info', `showing notification: ${title}`);
  self.registration.showNotification(title, options);
}

// Handle notification click actions
self.addEventListener('notificationclick', (event) => {
  const notification = event.notification;
  const data = notification.data || {};
  const action = event.action;

  notification.close();

  if (action === 'approve' && data.id) {
    event.waitUntil(callAPI(`/api/notifications/${data.id}/approve`, 'POST'));
    return;
  }

  if (action === 'deny' && data.id) {
    event.waitUntil(callAPI(`/api/notifications/${data.id}/deny`, 'POST'));
    return;
  }

  // Default click: focus or open the app
  event.waitUntil(
    clients.matchAll({ type: 'window', includeUncontrolled: true }).then((windowClients) => {
      for (const client of windowClients) {
        if ('focus' in client) {
          return client.focus();
        }
      }
      return clients.openWindow('/');
    })
  );
});

async function callAPI(path, method) {
  try {
    const response = await fetch(path, {
      method,
      headers: { 'Content-Type': 'application/json' },
      credentials: 'same-origin',
    });
    if (!response.ok) {
      swLog('error', `API call failed: ${path} ${response.status}`);
    }
  } catch (err) {
    swLog('error', `API call error: ${path} ${err}`);
  }
}

function sleep(ms) {
  return new Promise(resolve => setTimeout(resolve, ms));
}
