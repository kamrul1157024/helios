import { api, statusOf } from '../bridge.ts'
import { useHostNotifications } from '../host-data.ts'
import { store, useStore } from '../store.ts'
import { NotificationCard } from './notification-card.tsx'

/**
 * The pending approvals for one session.
 */
export function ApprovalsPanel({ hostId, sessionId }: { hostId: string; sessionId: string }): JSX.Element {
  const all = useHostNotifications()
  const pending = (all[hostId] ?? []).filter((n) => n.source_session === sessionId)

  if (pending.length === 0) {
    return <p className="empty-note">Nothing waiting for you.</p>
  }

  return (
    <div className="approvals">
      {pending.map((notif) => (
        <NotificationCard
          key={notif.id}
          notif={notif}
          onAct={async (body) => {
            try {
              await api(hostId).notificationAction(notif.id, body)
              void store.invalidateNotificationsFor(hostId)
            } catch (err) {
              // 410 means someone else — the terminal, the phone — got there first.
              if (statusOf(err) === 410) {
                store.notify('Already answered elsewhere')
                void store.invalidateNotificationsFor(hostId)
              } else {
                store.fail(err)
              }
            }
          }}
          onDismiss={() => void api(hostId).dismissNotification(notif.id)}
        />
      ))}
    </div>
  )
}
