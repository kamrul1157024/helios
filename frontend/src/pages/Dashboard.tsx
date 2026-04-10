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

export function Dashboard() {
  const [notifications, setNotifications] = useState<Notification[]>([]);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [loading, setLoading] = useState(true);
  const [pushEnabled, setPushEnabled] = useState(false);
  const [pushLoading, setPushLoading] = useState(false);

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

  // Split by type: permission requests vs informational
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
    return <div className="dashboard loading">Loading...</div>;
  }

  return (
    <div className="dashboard">
      <header>
        <h1>helios</h1>
        <div className="header-actions">
          <button
            className={pushEnabled ? 'btn-push active' : 'btn-push'}
            onClick={handleTogglePush}
            disabled={pushLoading}
            title={pushEnabled ? 'Push notifications enabled' : 'Enable push notifications'}
          >
            {pushLoading ? '...' : pushEnabled ? 'Push ON' : 'Push OFF'}
          </button>
          {pendingPermissions.length > 0 && (
            <>
              <button className="btn-approve" onClick={handleApproveAll}>
                Approve All ({pendingPermissions.length})
              </button>
              {selected.size > 0 && (
                <button className="btn-approve" onClick={handleApproveSelected}>
                  Approve Selected ({selected.size})
                </button>
              )}
            </>
          )}
        </div>
      </header>

      {pendingPermissions.length === 0 && activeStatuses.length === 0 && resolved.length === 0 && (
        <div className="empty-state">
          <p>No notifications yet.</p>
          <p className="hint">Start a Claude session with helios hooks installed to see permission requests here.</p>
        </div>
      )}

      {pendingPermissions.length > 0 && (
        <section>
          <h2>Pending Permissions ({pendingPermissions.length})</h2>
          <div className="notification-list">
            {pendingPermissions.map((n) => (
              <div key={n.id} className="notification-card pending">
                <div className="card-header">
                  <input
                    type="checkbox"
                    checked={selected.has(n.id)}
                    onChange={() => toggleSelect(n.id)}
                  />
                  <span className="badge badge-permission">permission</span>
                  <span className="tool-name">{n.tool_name}</span>
                  <span className="session-id" title={n.claude_session_id}>
                    {n.claude_session_id.slice(0, 8)}...
                  </span>
                </div>
                <div className="card-detail">
                  {n.detail || n.tool_input || 'No details'}
                </div>
                <div className="card-meta">
                  <span className="cwd" title={n.cwd}>{n.cwd}</span>
                  <span className="time">{formatTime(n.created_at)}</span>
                </div>
                <div className="card-actions">
                  <button className="btn-approve" onClick={() => handleApprove(n.id)}>
                    Approve
                  </button>
                  <button className="btn-deny" onClick={() => handleDeny(n.id)}>
                    Deny
                  </button>
                </div>
              </div>
            ))}
          </div>
        </section>
      )}

      {activeStatuses.length > 0 && (
        <section>
          <h2>Active Sessions</h2>
          <div className="notification-list">
            {activeStatuses.map((n) => (
              <div key={n.id} className={`notification-card status-${n.type}`}>
                <div className="card-header">
                  <span className={`badge badge-${n.type}`}>{statusLabel(n.type)}</span>
                  <span className="session-id" title={n.claude_session_id}>
                    {n.claude_session_id.slice(0, 8)}...
                  </span>
                  <button className="btn-dismiss" onClick={() => handleDismiss(n.id)} title="Dismiss">
                    &times;
                  </button>
                </div>
                <div className="card-detail">{n.detail || statusLabel(n.type)}</div>
                <div className="card-meta">
                  <span className="cwd" title={n.cwd}>{n.cwd}</span>
                  <span className="time">{formatTime(n.created_at)}</span>
                </div>
              </div>
            ))}
          </div>
        </section>
      )}

      {resolved.length > 0 && (
        <section>
          <h2>History</h2>
          <div className="notification-list">
            {resolved.map((n) => (
              <div key={n.id} className={`notification-card resolved ${n.status}`}>
                <div className="card-header">
                  <span className={`badge badge-${n.status}`}>{n.status}</span>
                  <span className="tool-name">{n.tool_name || statusLabel(n.type)}</span>
                </div>
                <div className="card-detail">
                  {n.detail || n.tool_input || statusLabel(n.type)}
                </div>
                <div className="card-meta">
                  <span className="cwd" title={n.cwd}>{n.cwd}</span>
                  <span className="time">{formatTime(n.created_at)}</span>
                  {n.resolved_source && (
                    <span className="source">via {n.resolved_source}</span>
                  )}
                </div>
              </div>
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
