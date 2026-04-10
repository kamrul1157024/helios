import { getAuthHeader } from './auth';

/**
 * Register the service worker and subscribe to Web Push notifications.
 * Call this after setup is complete and the user has a stored key.
 */

export async function registerServiceWorker(): Promise<ServiceWorkerRegistration | null> {
  if (!('serviceWorker' in navigator)) {
    console.warn('Service workers not supported');
    return null;
  }

  try {
    const reg = await navigator.serviceWorker.register('/sw.js');
    return reg;
  } catch (err) {
    console.error('SW registration failed:', err);
    return null;
  }
}

export async function subscribeToPush(): Promise<boolean> {
  if (!('PushManager' in window)) {
    console.warn('Push API not supported');
    return false;
  }

  const permission = await Notification.requestPermission();
  if (permission !== 'granted') {
    console.warn('Notification permission denied');
    return false;
  }

  const reg = await navigator.serviceWorker.ready;

  // Fetch VAPID public key from daemon
  const vapidResp = await fetch('/api/push/vapid-public-key');
  if (!vapidResp.ok) {
    console.error('Failed to fetch VAPID key');
    return false;
  }
  const { public_key } = await vapidResp.json();

  // Convert base64url VAPID key to Uint8Array for subscribe()
  const applicationServerKey = urlBase64ToUint8Array(public_key);

  // Subscribe via Push API — browser contacts FCM/Mozilla and returns an endpoint
  const subscription = await reg.pushManager.subscribe({
    userVisibleOnly: true,
    applicationServerKey: applicationServerKey.buffer as ArrayBuffer,
  });

  const subJSON = subscription.toJSON();

  // Send subscription to daemon so it can push to us later
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
  };

  try {
    const auth = await getAuthHeader();
    headers['Authorization'] = auth;
  } catch {
    // localhost — no auth needed
  }

  const resp = await fetch('/api/push/subscribe', {
    method: 'POST',
    headers,
    body: JSON.stringify({
      endpoint: subJSON.endpoint,
      keys: {
        p256dh: subJSON.keys?.p256dh,
        auth: subJSON.keys?.auth,
      },
    }),
  });

  if (!resp.ok) {
    console.error('Failed to register push subscription with daemon');
    return false;
  }

  return true;
}

export async function isPushSubscribed(): Promise<boolean> {
  if (!('serviceWorker' in navigator) || !('PushManager' in window)) {
    return false;
  }

  try {
    const reg = await navigator.serviceWorker.ready;
    const sub = await reg.pushManager.getSubscription();
    return sub !== null;
  } catch {
    return false;
  }
}

export async function unsubscribeFromPush(): Promise<boolean> {
  try {
    const reg = await navigator.serviceWorker.ready;
    const sub = await reg.pushManager.getSubscription();
    if (!sub) return true;

    const endpoint = sub.endpoint;
    await sub.unsubscribe();

    // Tell daemon to remove subscription
    const headers: Record<string, string> = {
      'Content-Type': 'application/json',
    };

    try {
      const auth = await getAuthHeader();
      headers['Authorization'] = auth;
    } catch {
      // localhost
    }

    await fetch('/api/push/unsubscribe', {
      method: 'POST',
      headers,
      body: JSON.stringify({ endpoint }),
    });

    return true;
  } catch (err) {
    console.error('Unsubscribe failed:', err);
    return false;
  }
}

function urlBase64ToUint8Array(base64String: string): Uint8Array {
  const padding = '='.repeat((4 - (base64String.length % 4)) % 4);
  const base64 = (base64String + padding).replace(/-/g, '+').replace(/_/g, '/');
  const rawData = atob(base64);
  const outputArray = new Uint8Array(rawData.length);
  for (let i = 0; i < rawData.length; i++) {
    outputArray[i] = rawData.charCodeAt(i);
  }
  return outputArray;
}
