import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { createRoot } from 'react-dom/client'

import { NotificationCard } from './components/notification-card.tsx'
import type { Notification } from '../shared/models.ts'

import './styles.css'
import './hud.css'

interface Card {
  hostId: string
  hostName?: string
  notification: Notification
}

const bridge = window.helios

function keyOf(hostId: string, notificationId: string): string {
  return `${hostId}:${notificationId}`
}

function Hud(): JSX.Element {
  const [cards, setCards] = useState<Card[]>([])
  const stack = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const offPresent = bridge.hud.onPresent((card) => {
      setCards((current) => {
        const key = keyOf(card.hostId, card.notification.id)
        const without = current.filter((c) => keyOf(c.hostId, c.notification.id) !== key)
        return [...without, card]
      })
    })
    const offRetract = bridge.hud.onRetract((key) => {
      setCards((current) => current.filter((c) => keyOf(c.hostId, c.notification.id) !== key))
    })
    return () => {
      offPresent()
      offRetract()
    }
  }, [])

  // The window is sized to the stack's own height. The stack is deliberately
  // unconstrained — inside a scroll container it keeps its natural size, so the
  // measurement does not chase the window it is sizing.
  useLayoutEffect(() => {
    const element = stack.current
    if (!element) return
    const report = (): void => {
      bridge.hud.resize(Math.ceil(element.getBoundingClientRect().height))
    }
    report()
    const observer = new ResizeObserver(report)
    observer.observe(element)
    return () => observer.disconnect()
  }, [cards])

  useEffect(() => {
    const onKey = (event: KeyboardEvent): void => {
      if (event.key === 'Escape') bridge.hud.dismiss()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  return (
    <div className="hud-stack" ref={stack}>
      {cards.map((card) => (
        <HudCard key={keyOf(card.hostId, card.notification.id)} card={card} />
      ))}
    </div>
  )
}

function HudCard({ card }: { card: Card }): JSX.Element {
  const { hostId, notification } = card
  const [error, setError] = useState<string | null>(null)

  const act = async (body: Record<string, unknown>): Promise<void> => {
    try {
      await bridge.api.call(hostId, 'notificationAction', [notification.id, body])
      // The daemon's resolved event retracts this normally; doing it here too
      // means the card does not sit there looking unanswered while the event
      // makes its way back.
      bridge.hud.resolved(keyOf(hostId, notification.id))
    } catch (err) {
      const status = (err as { status?: number }).status
      // 410: the terminal or the phone got there first.
      if (status === 410) bridge.hud.resolved(keyOf(hostId, notification.id))
      else setError(err instanceof Error ? err.message : String(err))
    }
  }

  return (
    <div className="hud-card">
      <div className="hud-head">
        <span className="hud-host">{card.hostName ?? hostId}</span>
        <button
          className="hud-open"
          onClick={() =>
            bridge.hud.activate({
              hostId,
              notificationId: notification.id,
              sessionId: notification.source_session,
            })
          }
        >
          Open session
        </button>
      </div>
      <NotificationCard
        notif={notification}
        onAct={act}
        onDismiss={() => bridge.hud.resolved(keyOf(hostId, notification.id))}
      />
      {error && <p className="hud-error">{error}</p>}
    </div>
  )
}

createRoot(document.getElementById('root') as HTMLElement).render(<Hud />)
