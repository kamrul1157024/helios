// helios service worker — handles Web Push notifications

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
    icon: '/icons/icon-192.svg',
    badge: '/icons/icon-192.svg',
    tag: payload.id || 'helios-notification',
    renotify: true,
    requireInteraction: true, // Keep notification visible until user acts
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
      // Focus existing window if open
      for (const client of windowClients) {
        if (client.url.includes('/') && 'focus' in client) {
          return client.focus();
        }
      }
      // Open new window
      return clients.openWindow('/');
    })
  );
});

// Handle notification close (dismissed without action)
self.addEventListener('notificationclose', (event) => {
  // No action needed — notification stays pending in helios
});

// Call helios API with JWT auth
async function callAPI(path, method) {
  try {
    // Get auth token from IndexedDB
    const token = await getAuthToken();
    const headers = { 'Content-Type': 'application/json' };
    if (token) {
      headers['Authorization'] = `Bearer ${token}`;
    }

    const response = await fetch(path, { method, headers });
    if (!response.ok) {
      console.error(`helios SW: API call failed: ${response.status}`);
    }
  } catch (err) {
    console.error('helios SW: API call error:', err);
  }
}

// Read Ed25519 key from IndexedDB and sign a JWT
async function getAuthToken() {
  try {
    const db = await openDB();
    const stored = await getFromDB(db, 'keys', 'device-key');
    if (!stored || !stored.key || !stored.kid) return null;

    // Build JWT
    const header = { alg: 'EdDSA', typ: 'JWT', kid: stored.kid };
    const now = Math.floor(Date.now() / 1000);
    const payload = { iat: now, exp: now + 3600, sub: 'helios-client' };

    const encodedHeader = base64urlEncode(JSON.stringify(header));
    const encodedPayload = base64urlEncode(JSON.stringify(payload));
    const signingInput = `${encodedHeader}.${encodedPayload}`;

    const signature = await crypto.subtle.sign(
      { name: 'Ed25519' },
      stored.key,
      new TextEncoder().encode(signingInput)
    );

    const encodedSignature = bytesToBase64url(new Uint8Array(signature));
    return `${signingInput}.${encodedSignature}`;
  } catch (err) {
    console.error('helios SW: auth error:', err);
    return null;
  }
}

// IndexedDB helpers
function openDB() {
  return new Promise((resolve, reject) => {
    const req = indexedDB.open('helios-auth', 1);
    req.onupgradeneeded = () => req.result.createObjectStore('keys');
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
}

function getFromDB(db, store, key) {
  return new Promise((resolve, reject) => {
    const tx = db.transaction(store, 'readonly');
    const req = tx.objectStore(store).get(key);
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
}

function base64urlEncode(str) {
  return btoa(str).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

function bytesToBase64url(bytes) {
  let binary = '';
  for (const b of bytes) binary += String.fromCharCode(b);
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}
