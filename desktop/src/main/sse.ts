import { EventEmitter } from 'node:events'

import type { ApiClient } from './api.ts'
import type { SSEEvent } from '../shared/models.ts'

/** Mobile backs off 3s→30s (daemon_api_service.dart:275); match it. */
const BACKOFF_MIN = 3_000
const BACKOFF_MAX = 30_000

/**
 * Server-sent events from one daemon.
 *
 * Built on fetch rather than EventSource because the endpoint is bearer
 * authenticated and EventSource cannot set headers — the same constraint that
 * keeps the WebSocket in the main process.
 *
 * Events: `event` (SSEEvent), `state` ('open' | 'closed').
 */
export class EventStream extends EventEmitter {
  private controller: AbortController | null = null
  private retryTimer: NodeJS.Timeout | null = null
  private backoff = BACKOFF_MIN
  private stopped = false

  constructor(private readonly api: ApiClient) {
    super()
  }

  start(): void {
    this.stopped = false
    void this.run()
  }

  stop(): void {
    this.stopped = true
    if (this.retryTimer) clearTimeout(this.retryTimer)
    this.retryTimer = null
    this.controller?.abort()
    this.controller = null
  }

  private async run(): Promise<void> {
    if (this.stopped) return

    const controller = new AbortController()
    this.controller = controller

    try {
      const response = await fetch(`${this.api.baseUrl}/api/events`, {
        headers: { Authorization: this.api.authHeader(), Accept: 'text/event-stream' },
        signal: controller.signal,
      })

      if (response.status === 401) {
        // The token outlived its device or the clock moved; a fresh one is one
        // signature away, so this is a retry rather than a failure.
        this.api.invalidate()
        throw new Error('unauthorized')
      }
      if (!response.ok || !response.body) {
        throw new Error(`events endpoint returned ${response.status}`)
      }

      this.backoff = BACKOFF_MIN
      this.emit('state', 'open')
      await this.consume(response.body)
      throw new Error('event stream ended')
    } catch (err) {
      if (this.stopped) return
      this.emit('state', 'closed')
      this.emit('error', err instanceof Error ? err : new Error(String(err)))
      this.scheduleRetry()
    }
  }

  private async consume(body: ReadableStream<Uint8Array>): Promise<void> {
    const reader = body.getReader()
    const decoder = new TextDecoder()
    let buffer = ''

    for (;;) {
      const { done, value } = await reader.read()
      if (done) return
      buffer += decoder.decode(value, { stream: true })

      // Events are separated by a blank line; a chunk may hold several, or half
      // of one, so only complete records are dispatched.
      let boundary = buffer.indexOf('\n\n')
      while (boundary !== -1) {
        this.dispatch(buffer.slice(0, boundary))
        buffer = buffer.slice(boundary + 2)
        boundary = buffer.indexOf('\n\n')
      }
    }
  }

  private dispatch(record: string): void {
    let eventName = 'message'
    const dataLines: string[] = []

    for (const rawLine of record.split('\n')) {
      const line = rawLine.endsWith('\r') ? rawLine.slice(0, -1) : rawLine
      if (line.startsWith(':') || line === '') continue
      const colon = line.indexOf(':')
      const field = colon === -1 ? line : line.slice(0, colon)
      const value = colon === -1 ? '' : line.slice(colon + 1).replace(/^ /, '')
      if (field === 'event') eventName = value
      else if (field === 'data') dataLines.push(value)
    }

    if (dataLines.length === 0) return
    const payload = dataLines.join('\n')

    let data: Record<string, unknown> = {}
    try {
      const parsed = JSON.parse(payload)
      if (parsed && typeof parsed === 'object') data = parsed as Record<string, unknown>
    } catch {
      data = { raw: payload }
    }

    // The daemon puts the type in the SSE event name and the payload in data
    // (internal/server/sse.go:85). A `type` inside the payload is the
    // notification's own kind, not the event's, so the name wins.
    const type = eventName !== 'message' ? eventName : typeof data.type === 'string' ? data.type : 'message'
    this.emit('event', { type, data } satisfies SSEEvent)
  }

  private scheduleRetry(): void {
    if (this.retryTimer || this.stopped) return
    const delay = this.backoff
    this.backoff = Math.min(this.backoff * 2, BACKOFF_MAX)
    this.retryTimer = setTimeout(() => {
      this.retryTimer = null
      void this.run()
    }, delay)
  }
}
