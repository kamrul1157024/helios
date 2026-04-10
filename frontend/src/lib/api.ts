const BASE = '';

// Cookie-based auth: browser sends HttpOnly cookie automatically.
// No need to set Authorization headers.
async function authFetch(path: string, options: RequestInit = {}): Promise<Response> {
  const resp = await fetch(`${BASE}${path}`, {
    ...options,
    credentials: 'same-origin', // Send cookies
  });

  if (resp.status === 401) {
    // Redirect to setup
    window.location.hash = '#/setup';
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

export async function login(token: string): Promise<{ success: boolean; kid: string }> {
  const resp = await fetch(`${BASE}/api/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ token }),
    credentials: 'same-origin',
  });
  return resp.json();
}

export async function getDeviceMe(): Promise<{
  kid: string;
  name: string;
  platform: string;
  browser: string;
} | null> {
  try {
    const resp = await authFetch('/api/auth/device/me');
    if (!resp.ok) return null;
    return resp.json();
  } catch {
    return null;
  }
}

export async function updateDeviceMe(name: string, platform: string, browser: string): Promise<void> {
  await authFetch('/api/auth/device/me', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name, platform, browser }),
  });
}

export async function healthCheck(): Promise<{ status: string; sse_clients: number; pending: number }> {
  const resp = await fetch(`${BASE}/api/health`);
  return resp.json();
}
