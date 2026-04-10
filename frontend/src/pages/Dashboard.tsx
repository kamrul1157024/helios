import { useEffect, useState, useCallback } from 'react';
import {
  listNotifications,
  approveNotification,
  denyNotification,
  batchAction,
  type Notification,
} from '../lib/api';
import { connectSSE } from '../lib/sse';

export function Dashboard() {
  const [notifications, setNotifications] = useState<Notification[]>([]);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [loading, setLoading] = useState(true);

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
    const disconnect = connectSSE(() => {
      refresh();
    });
    return disconnect;
  }, [refresh]);

  const pending = notifications.filter((n) => n.status === 'pending');
  const resolved = notifications.filter((n) => n.status !== 'pending');

  async function handleApprove(id: string) {
    await approveNotification(id);
    refresh();
  }

  async function handleDeny(id: string) {
    await denyNotification(id);
    refresh();
  }

  async function handleApproveAll() {
    const ids = pending.map((n) => n.id);
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
          {pending.length > 0 && (
            <>
              <button className="btn-approve" onClick={handleApproveAll}>
                Approve All ({pending.length})
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

      {pending.length === 0 && resolved.length === 0 && (
        <div className="empty-state">
          <p>No notifications yet.</p>
          <p className="hint">Start a Claude session with helios hooks installed to see permission requests here.</p>
        </div>
      )}

      {pending.length > 0 && (
        <section>
          <h2>Pending ({pending.length})</h2>
          <div className="notification-list">
            {pending.map((n) => (
              <div key={n.id} className="notification-card pending">
                <div className="card-header">
                  <input
                    type="checkbox"
                    checked={selected.has(n.id)}
                    onChange={() => toggleSelect(n.id)}
                  />
                  <span className="badge badge-permission">{n.type}</span>
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

      {resolved.length > 0 && (
        <section>
          <h2>History</h2>
          <div className="notification-list">
            {resolved.map((n) => (
              <div key={n.id} className={`notification-card resolved ${n.status}`}>
                <div className="card-header">
                  <span className={`badge badge-${n.status}`}>{n.status}</span>
                  <span className="tool-name">{n.tool_name || n.type}</span>
                </div>
                <div className="card-detail">
                  {n.detail || n.tool_input || 'No details'}
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
