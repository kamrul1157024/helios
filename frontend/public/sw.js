// helios service worker — handles Web Push notifications
// Uses cookie-based auth (credentials: 'same-origin')

self.addEventListener('install', (event) => {
  self.skipWaiting();
});

self.addEventListener('activate', (event) => {
  event.waitUntil(clients.claim());
});

// Handle incoming push notifications
self.addEventListener('push', (event) => {
  if (!event.data) return;

  let payload;
  try {
    payload = event.data.json();
  } catch {
    payload = { title: 'helios', body: event.data.text() };
  }

  const options = {
    body: payload.body || '',
    icon: '/icons/icon-192.png',
    badge: '/icons/icon-192.png',
    tag: payload.id || 'helios-notification',
    renotify: true,
    requireInteraction: true,
    data: {
      id: payload.id,
      type: payload.type,
      url: payload.url || '/',
    },
    actions: [],
  };

  // Add approve/deny buttons for permission notifications
  if (payload.type === 'permission' && payload.id) {
    options.actions = [
      { action: 'approve', title: 'Approve' },
      { action: 'deny', title: 'Deny' },
    ];
  }

  event.waitUntil(
    self.registration.showNotification(payload.title || 'helios', options)
  );
});

// Handle notification click actions (approve/deny buttons)
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

  // Default click: open dashboard
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

// Handle notification close (dismissed without action)
self.addEventListener('notificationclose', (event) => {
  // No action needed — notification stays pending in helios
});

// Call helios API with cookie auth (same-origin credentials)
async function callAPI(path, method) {
  try {
    const response = await fetch(path, {
      method,
      headers: { 'Content-Type': 'application/json' },
      credentials: 'same-origin',
    });
    if (!response.ok) {
      console.error(`helios SW: API call failed: ${response.status}`);
    }
  } catch (err) {
    console.error('helios SW: API call error:', err);
  }
}
