/**
 * Push notifications via SSE in the service worker.
 * No FCM/Web Push — the SW holds an SSE connection and shows local notifications.
 */

import { deviceLog } from './logger';

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

export async function subscribeToPush(): Promise<{ ok: boolean; error?: string }> {
  if (!('serviceWorker' in navigator)) {
    return fail('Service workers not supported');
  }
  if (!('Notification' in window)) {
    return fail('Notifications not supported');
  }

  try {
    deviceLog.info('[1/3] registering service worker', 'push');
    const reg = await navigator.serviceWorker.register('/sw.js');
    deviceLog.info(`[1/3] SW registered: state=${reg.active?.state ?? reg.installing?.state ?? 'unknown'}`, 'push');

    deviceLog.info(`[2/3] requesting notification permission (current: ${Notification.permission})`, 'push');
    const permission = await Notification.requestPermission();
    deviceLog.info(`[2/3] permission result: ${permission}`, 'push');

    if (permission !== 'granted') {
      return fail(`Notification permission: ${permission}`);
    }

    // Tell the SW to start its SSE connection
    deviceLog.info('[3/3] starting SSE in service worker', 'push');
    const ready = await navigator.serviceWorker.ready;
    ready.active?.postMessage('start-sse');

    deviceLog.info('[3/3] SSE notifications enabled', 'push');
    return { ok: true };
  } catch (err) {
    return fail(`${err}`);
  }
}

function fail(error: string): { ok: false; error: string } {
  deviceLog.error(error, 'push');
  return { ok: false, error };
}

export async function isPushSubscribed(): Promise<boolean> {
  if (!('serviceWorker' in navigator) || !('Notification' in window)) {
    return false;
  }
  if (Notification.permission !== 'granted') {
    return false;
  }

  try {
    const reg = await navigator.serviceWorker.getRegistration('/sw.js');
    return reg !== undefined && reg !== null;
  } catch {
    return false;
  }
}

export async function unsubscribeFromPush(): Promise<boolean> {
  try {
    const reg = await navigator.serviceWorker.ready;
    reg.active?.postMessage('stop-sse');
    return true;
  } catch (err) {
    console.error('Unsubscribe failed:', err);
    return false;
  }
}
