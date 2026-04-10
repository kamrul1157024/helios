import { useEffect, useState, useCallback } from 'react';
import {
  listNotifications,
  approveNotification,
  denyNotification,
  dismissNotification,
  batchAction,
  type Notification,
} from '../lib/api';
import { connectSSE } from '../lib/sse';
import { subscribeToPush, isPushSubscribed, unsubscribeFromPush } from '../lib/push';
import { getTheme, toggleTheme } from '../lib/theme';
import { Card, CardContent } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { Switch } from '@/components/ui/switch';
import { Separator } from '@/components/ui/separator';

export function Dashboard() {
  const [notifications, setNotifications] = useState<Notification[]>([]);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [loading, setLoading] = useState(true);
  const [pushEnabled, setPushEnabled] = useState(false);
  const [pushLoading, setPushLoading] = useState(false);
  const [theme, setThemeState] = useState(getTheme);

  const refresh = useCallback(async () => {
    try {
      const all = await listNotifications();
      setNotifications(all);
    } catch {
      // ignore
    }
    setLoading(false);
  }, []);

  useEffect(() => {
    refresh();
    isPushSubscribed().then(setPushEnabled);
    const disconnect = connectSSE(() => {
      refresh();
    });
    return disconnect;
  }, [refresh]);

  async function handleTogglePush() {
    setPushLoading(true);
    try {
      if (pushEnabled) {
        await unsubscribeFromPush();
        setPushEnabled(false);
      } else {
        const ok = await subscribeToPush();
        setPushEnabled(ok);
      }
    } finally {
      setPushLoading(false);
    }
  }

  function handleToggleTheme() {
    const next = toggleTheme();
    setThemeState(next);
  }

  const pendingPermissions = notifications.filter(
    (n) => n.status === 'pending' && n.type === 'permission'
  );
  const activeStatuses = notifications.filter(
    (n) => n.status === 'pending' && n.type !== 'permission'
  );
  const resolved = notifications.filter((n) => n.status !== 'pending');

  async function handleApprove(id: string) {
    await approveNotification(id);
    refresh();
  }

  async function handleDeny(id: string) {
    await denyNotification(id);
    refresh();
  }

  async function handleDismiss(id: string) {
    await dismissNotification(id);
    refresh();
  }

  async function handleApproveAll() {
    const ids = pendingPermissions.map((n) => n.id);
    if (ids.length === 0) return;
    await batchAction(ids, 'approve');
    refresh();
  }

  async function handleApproveSelected() {
    const ids = Array.from(selected);
    if (ids.length === 0) return;
    await batchAction(ids, 'approve');
    setSelected(new Set());
    refresh();
  }

  function toggleSelect(id: string) {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  if (loading) {
    return (
      <div className="flex items-center justify-center min-h-screen">
        <div className="flex items-center gap-3 text-muted-foreground">
          <svg className="animate-spin h-5 w-5" viewBox="0 0 24 24" fill="none">
            <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
            <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
          </svg>
          <span>Loading...</span>
        </div>
      </div>
    );
  }

  return (
    <div className="max-w-2xl mx-auto p-4 pb-8">
      {/* Header */}
      <header className="flex items-center justify-between mb-6">
        <h1 className="text-xl font-bold tracking-tight">helios</h1>
        <div className="flex items-center gap-3">
          {/* Theme toggle */}
          <Button variant="ghost" size="icon" onClick={handleToggleTheme} title="Toggle theme">
            {theme === 'dark' ? (
              <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M12 3v1m0 16v1m9-9h-1M4 12H3m15.364 6.364l-.707-.707M6.343 6.343l-.707-.707m12.728 0l-.707.707M6.343 17.657l-.707.707M16 12a4 4 0 11-8 0 4 4 0 018 0z" />
              </svg>
            ) : (
              <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M20.354 15.354A9 9 0 018.646 3.646 9.003 9.003 0 0012 21a9.003 9.003 0 008.354-5.646z" />
              </svg>
            )}
          </Button>

          {/* Push toggle */}
          <div className="flex items-center gap-2">
            <label htmlFor="push-toggle" className="text-xs text-muted-foreground">
              Push
            </label>
            <Switch
              id="push-toggle"
              checked={pushEnabled}
              onCheckedChange={handleTogglePush}
              disabled={pushLoading}
            />
          </div>

          {/* Bulk actions */}
          {pendingPermissions.length > 0 && (
            <>
              <Separator orientation="vertical" className="h-6" />
              <Button size="sm" onClick={handleApproveAll}>
                Approve All ({pendingPermissions.length})
              </Button>
              {selected.size > 0 && (
                <Button size="sm" variant="outline" onClick={handleApproveSelected}>
                  Approve ({selected.size})
                </Button>
              )}
            </>
          )}
        </div>
      </header>

      {/* Empty state */}
      {pendingPermissions.length === 0 && activeStatuses.length === 0 && resolved.length === 0 && (
        <div className="text-center py-16">
          <div className="inline-flex items-center justify-center w-12 h-12 rounded-full bg-muted mb-4">
            <svg className="h-6 w-6 text-muted-foreground" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M14.857 17.082a23.848 23.848 0 005.454-1.31A8.967 8.967 0 0118 9.75v-.7V9A6 6 0 006 9v.75a8.967 8.967 0 01-2.312 6.022c1.733.64 3.56 1.085 5.455 1.31m5.714 0a24.255 24.255 0 01-5.714 0m5.714 0a3 3 0 11-5.714 0" />
            </svg>
          </div>
          <p className="text-muted-foreground">No notifications yet.</p>
          <p className="text-sm text-muted-foreground/70 mt-1">
            Start a Claude session with helios hooks installed.
          </p>
        </div>
      )}

      {/* Pending Permissions */}
      {pendingPermissions.length > 0 && (
        <section className="mb-6">
          <h2 className="text-sm font-medium text-muted-foreground mb-3">
            Pending Permissions ({pendingPermissions.length})
          </h2>
          <div className="space-y-3">
            {pendingPermissions.map((n) => (
              <Card key={n.id} className="border-l-4 border-l-warning">
                <CardContent className="space-y-3">
                  <div className="flex items-center gap-2">
                    <input
                      type="checkbox"
                      checked={selected.has(n.id)}
                      onChange={() => toggleSelect(n.id)}
                      className="accent-primary"
                    />
                    <Badge variant="outline" className="text-warning border-warning/30 bg-warning/10">
                      permission
                    </Badge>
                    <span className="font-semibold text-sm">{n.tool_name}</span>
                    <span className="ml-auto font-mono text-xs text-muted-foreground" title={n.claude_session_id}>
                      {n.claude_session_id.slice(0, 8)}...
                    </span>
                  </div>

                  <div className="rounded-md bg-muted p-2.5 text-sm font-mono break-all max-h-28 overflow-y-auto">
                    {n.detail || n.tool_input || 'No details'}
                  </div>

                  <div className="flex items-center gap-3 text-xs text-muted-foreground">
                    <span className="font-mono truncate max-w-[240px]" title={n.cwd}>{n.cwd}</span>
                    <span>{formatTime(n.created_at)}</span>
                  </div>

                  <div className="flex gap-2">
                    <Button size="sm" onClick={() => handleApprove(n.id)}>
                      Approve
                    </Button>
                    <Button size="sm" variant="destructive" onClick={() => handleDeny(n.id)}>
                      Deny
                    </Button>
                  </div>
                </CardContent>
              </Card>
            ))}
          </div>
        </section>
      )}

      {/* Active Sessions */}
      {activeStatuses.length > 0 && (
        <section className="mb-6">
          <h2 className="text-sm font-medium text-muted-foreground mb-3">Active Sessions</h2>
          <div className="space-y-3">
            {activeStatuses.map((n) => (
              <Card key={n.id} className={n.type === 'error' ? 'border-l-4 border-l-destructive' : 'border-l-4 border-l-primary'}>
                <CardContent>
                  <div className="flex items-center gap-2 mb-2">
                    <Badge variant={n.type === 'error' ? 'destructive' : 'secondary'}>
                      {statusLabel(n.type)}
                    </Badge>
                    <span className="ml-auto font-mono text-xs text-muted-foreground" title={n.claude_session_id}>
                      {n.claude_session_id.slice(0, 8)}...
                    </span>
                    <Button variant="ghost" size="icon-xs" onClick={() => handleDismiss(n.id)} title="Dismiss">
                      <svg className="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                        <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
                      </svg>
                    </Button>
                  </div>
                  <p className="text-sm text-muted-foreground">{n.detail || statusLabel(n.type)}</p>
                  <div className="flex items-center gap-3 text-xs text-muted-foreground mt-2">
                    <span className="font-mono truncate max-w-[240px]" title={n.cwd}>{n.cwd}</span>
                    <span>{formatTime(n.created_at)}</span>
                  </div>
                </CardContent>
              </Card>
            ))}
          </div>
        </section>
      )}

      {/* History */}
      {resolved.length > 0 && (
        <section>
          <h2 className="text-sm font-medium text-muted-foreground mb-3">History</h2>
          <div className="space-y-2">
            {resolved.map((n) => (
              <Card key={n.id} className="opacity-70">
                <CardContent>
                  <div className="flex items-center gap-2 mb-1">
                    <Badge variant={n.status === 'approved' ? 'default' : n.status === 'denied' ? 'destructive' : 'secondary'}>
                      {n.status}
                    </Badge>
                    <span className="text-sm font-medium">{n.tool_name || statusLabel(n.type)}</span>
                  </div>
                  <p className="text-sm text-muted-foreground truncate">
                    {n.detail || n.tool_input || statusLabel(n.type)}
                  </p>
                  <div className="flex items-center gap-3 text-xs text-muted-foreground mt-1">
                    <span className="font-mono truncate max-w-[240px]" title={n.cwd}>{n.cwd}</span>
                    <span>{formatTime(n.created_at)}</span>
                    {n.resolved_source && (
                      <span>via {n.resolved_source}</span>
                    )}
                  </div>
                </CardContent>
              </Card>
            ))}
          </div>
        </section>
      )}
    </div>
  );
}

function statusLabel(type: string): string {
  switch (type) {
    case 'idle': return 'Waiting for input';
    case 'done': return 'Session completed';
    case 'error': return 'Session error';
    case 'permission': return 'Permission request';
    default: return type;
  }
}

function formatTime(ts: string): string {
  try {
    const d = new Date(ts.includes('T') ? ts : ts.replace(' ', 'T') + 'Z');
    const now = new Date();
    const diff = now.getTime() - d.getTime();

    if (diff < 60000) return 'just now';
    if (diff < 3600000) return `${Math.floor(diff / 60000)}m ago`;
    if (diff < 86400000) return `${Math.floor(diff / 3600000)}h ago`;
    return d.toLocaleDateString();
  } catch {
    return ts;
  }
}
