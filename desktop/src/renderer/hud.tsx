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

  // Taken off screen here rather than waiting for the main process to echo the
  // retract back: the card should go the moment it is dealt with.
  const remove = (key: string): void => {
    setCards((current) => current.filter((c) => keyOf(c.hostId, c.notification.id) !== key))
    bridge.hud.resolved(key)
  }

  return (
    <div className="hud-stack" ref={stack}>
      {cards.map((card) => (
        <HudCard key={keyOf(card.hostId, card.notification.id)} card={card} onDone={remove} />
      ))}
    </div>
  )
}

function HudCard({ card, onDone }: { card: Card; onDone: (key: string) => void }): JSX.Element {
  const { hostId, notification } = card
  const key = keyOf(hostId, notification.id)
  const [error, setError] = useState<string | null>(null)

  const act = async (body: Record<string, unknown>): Promise<void> => {
    try {
      await bridge.api.call(hostId, 'notificationAction', [notification.id, body])
      onDone(key)
    } catch (err) {
      const status = (err as { status?: number }).status
      // 410: the terminal or the phone got there first, so it is dealt with.
      if (status === 410) onDone(key)
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
        {/* Hiding a card is not answering it: the request stays pending, on the
            tray and on the phone, and comes back if the app reconciles. */}
        <button className="hud-close" title="Hide — the request stays pending" onClick={() => onDone(key)}>
          ×
        </button>
      </div>
      <NotificationCard notif={notification} onAct={act} onDismiss={() => onDone(key)} />
      {error && <p className="hud-error">{error}</p>}
    </div>
  )
}

createRoot(document.getElementById('root') as HTMLElement).render(<Hud />)
