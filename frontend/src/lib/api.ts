import { getAuthHeader, hasKey } from './auth';

const BASE = '';

async function authFetch(path: string, options: RequestInit = {}): Promise<Response> {
  const headers = new Headers(options.headers);

  try {
    const auth = await getAuthHeader();
    headers.set('Authorization', auth);
  } catch {
    // No key stored — will get 401 from server if remote
  }

  const resp = await fetch(`${BASE}${path}`, { ...options, headers });

  if (resp.status === 401) {
    const keyExists = await hasKey();
    if (!keyExists) {
      window.location.hash = '#/';
    }
  }

  return resp;
}

export interface Notification {
  id: string;
  claude_session_id: string;
  cwd: string;
  type: string;
  status: string;
  tool_name?: string;
  tool_input?: string;
  detail?: string;
  resolved_at?: string;
  resolved_source?: string;
  created_at: string;
}

export async function listNotifications(status?: string, type?: string): Promise<Notification[]> {
  const params = new URLSearchParams();
  if (status) params.set('status', status);
  if (type) params.set('type', type);

  const resp = await authFetch(`/api/notifications?${params}`);
  const data = await resp.json();
  return data.notifications || [];
}

export async function approveNotification(id: string): Promise<void> {
  await authFetch(`/api/notifications/${id}/approve`, { method: 'POST' });
}

export async function denyNotification(id: string): Promise<void> {
  await authFetch(`/api/notifications/${id}/deny`, { method: 'POST' });
}

export async function dismissNotification(id: string): Promise<void> {
  await authFetch(`/api/notifications/${id}/dismiss`, { method: 'POST' });
}

export async function batchAction(ids: string[], action: 'approve' | 'deny'): Promise<void> {
  await authFetch('/api/notifications/batch', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ notification_ids: ids, action }),
  });
}

export async function verifyAuth(): Promise<boolean> {
  try {
    const resp = await authFetch('/api/auth/verify', { method: 'POST' });
    return resp.ok;
  } catch {
    return false;
  }
}

export async function healthCheck(): Promise<{ status: string; sse_clients: number; pending: number }> {
  const resp = await fetch(`${BASE}/api/health`);
  return resp.json();
}
