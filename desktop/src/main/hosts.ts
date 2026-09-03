import { EventEmitter } from 'node:events'
import { app, safeStorage } from 'electron'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

import { ApiClient } from './api.ts'
import { EventStream } from './sse.ts'
import { generateDeviceKey, publicKeyBase64, type DeviceKey } from './keys.ts'
import type { HostConnectionState, HostRecord, HostStatus } from '../shared/models.ts'

/** The daemon's no-auth admin API, which only ever listens on loopback. */
const INTERNAL_URL = 'http://127.0.0.1:7654'
const DEFAULT_LOCAL_URL = 'http://127.0.0.1:7655'

interface StoredHost extends HostRecord {
  /** Encrypted device seed, base64. Plaintext only if safeStorage is unavailable. */
  secret: string
  encrypted: boolean
}

export interface HostHandle {
  record: HostRecord
  api: ApiClient
  events: EventStream
  key: DeviceKey
  state: HostConnectionState
  error?: string
  /** What the daemon last said it was running. Absent until it answers, and on
   *  a daemon old enough not to report one at all. */
  version?: string
}

/**
 * Every daemon the app talks to, and the credentials for each.
 *
 * Mirrors HostManager in the Flutter client. Lives in the main process because
 * it owns the device keys, which the renderer must never see.
 *
 * Events: `hosts` (HostRecord[]), `status` ({id, state, error}), `event`
 * ({hostId, event}).
 */
export class HostRegistry extends EventEmitter {
  private hosts = new Map<string, HostHandle>()
  private readonly file: string

  constructor(userDataDir = app.getPath('userData')) {
    super()
    this.file = path.join(userDataDir, 'hosts.json')
  }

  load(): void {
    for (const stored of this.readFile()) {
      const seed = this.decrypt(stored)
      if (!seed) continue
      this.instantiate({ ...stripSecret(stored) }, { kid: stored.device_id, seed })
    }
    this.emit('hosts', this.list())
  }

  list(): HostRecord[] {
    return [...this.hosts.values()].map((h) => h.record)
  }

  statuses(): HostStatus[] {
    return [...this.hosts.values()].map((h) => ({
      id: h.record.id,
      state: h.state,
      error: h.error,
      version: h.version,
    }))
  }

  get(id: string): HostHandle | undefined {
    return this.hosts.get(id)
  }

  /** Throws rather than returning undefined: every IPC handler needs a host. */
  require(id: string): HostHandle {
    const host = this.hosts.get(id)
    if (!host) throw new Error(`unknown host ${id}`)
    return host
  }

  private instantiate(record: HostRecord, key: DeviceKey): HostHandle {
    const api = new ApiClient(record.url, key)
    const events = new EventStream(api)
    const handle: HostHandle = { record, api, events, key, state: 'connecting' }

    events.on('event', (event) => this.emit('event', { hostId: record.id, event }))
    events.on('state', (state: string) => {
      this.setState(record.id, state === 'open' ? 'online' : 'offline')
      // Asked on the way up rather than on a timer: the version changes when
      // the daemon restarts, and a restart is what closed and reopened this
      // stream. Unauthenticated, so it answers even for a host mid-pairing.
      if (state === 'open') void this.readVersion(record.id)
    })
    // An SSE failure is normal when a laptop sleeps; it reconnects itself and
    // the state change is the only thing worth surfacing.
    events.on('error', () => {})
    events.start()

    this.hosts.set(record.id, handle)
    return handle
  }

  private setState(id: string, state: HostConnectionState, error?: string): void {
    const host = this.hosts.get(id)
    if (!host || (host.state === state && host.error === error)) return
    host.state = state
    host.error = error
    this.emit('status', { id, state, error, version: host.version })
  }

  /** The daemon's own version, for the Hosts pane to compare against the
   *  newest release. A failure leaves the last answer alone: it means the
   *  health call did not land, not that the daemon forgot its version. */
  private async readVersion(id: string): Promise<void> {
    const host = this.hosts.get(id)
    if (!host) return
    const { ok, version } = await host.api.health()
    if (!ok || host.version === version) return
    host.version = version
    this.emit('status', { id, state: host.state, error: host.error, version })
  }

  // ─── Pairing ───────────────────────────────────────────────────────────

  /**
   * Pairs against the daemon on this machine with no user interaction.
   *
   * Reaching 127.0.0.1:7654 is already the CLI's trust boundary — that endpoint
   * mints pairing tokens for `helios setup` today — so a local app can mint one
   * for itself. The device still shows up in `GET /api/auth/devices` like any
   * other and can be revoked there.
   */
  async pairLocal(name = os.hostname()): Promise<HostRecord> {
    const created = await postJSON<{ token: string }>(`${INTERNAL_URL}/internal/device/create`, {})
    if (!created.token) throw new Error('daemon did not issue a pairing token')

    const key = generateDeviceKey()
    const paired = await postJSON<{ success?: boolean; error?: string }>(
      `${DEFAULT_LOCAL_URL}/api/auth/pair`,
      { token: created.token, kid: key.kid, public_key: publicKeyBase64(key.seed) },
    )
    if (paired.error) throw new Error(`pairing rejected: ${paired.error}`)

    // Activation is what turns a pending device into one the public API will
    // accept. It is only reachable from loopback, which is the whole point.
    await postJSON(`${INTERNAL_URL}/internal/device/activate`, { kid: key.kid })

    return this.adopt({ name, url: DEFAULT_LOCAL_URL, local: true }, key)
  }

  /**
   * Pairs with a remote daemon from a `helios://pair?url=…&token=…` URL — the
   * same one the setup QR encodes, since a desktop has no camera.
   */
  async pairURL(pairingUrl: string, name?: string): Promise<HostRecord> {
    const { url, token } = parsePairingURL(pairingUrl)

    const key = generateDeviceKey()
    const paired = await postJSON<{ success?: boolean; error?: string; message?: string }>(
      `${url}/api/auth/pair`,
      { token, kid: key.kid, public_key: publicKeyBase64(key.seed) },
    )
    if (paired.error) throw new Error(paired.message ?? `pairing rejected: ${paired.error}`)

    return this.adopt({ name: name ?? hostnameOf(url), url, local: isLoopback(url) && runDirReadable() }, key)
  }

  private adopt(spec: { name: string; url: string; local: boolean }, key: DeviceKey): HostRecord {
    const record: HostRecord = {
      id: key.kid,
      name: spec.name,
      url: spec.url.replace(/\/$/, ''),
      device_id: key.kid,
      local: spec.local,
    }
    this.instantiate(record, key)
    this.persist()
    this.emit('hosts', this.list())
    return record
  }

  remove(id: string): void {
    const host = this.hosts.get(id)
    if (!host) return
    host.events.stop()
    this.hosts.delete(id)
    this.persist()
    this.emit('hosts', this.list())
  }

  rename(id: string, name: string): void {
    const host = this.require(id)
    host.record.name = name
    this.persist()
    this.emit('hosts', this.list())
  }

  // ─── Persistence ───────────────────────────────────────────────────────

  private readFile(): StoredHost[] {
    try {
      const raw = fs.readFileSync(this.file, 'utf8')
      const parsed = JSON.parse(raw)
      return Array.isArray(parsed) ? (parsed as StoredHost[]) : []
    } catch {
      return []
    }
  }

  private persist(): void {
    const stored: StoredHost[] = [...this.hosts.values()].map((h) => ({
      ...h.record,
      ...this.encrypt(h.key.seed),
    }))
    fs.mkdirSync(path.dirname(this.file), { recursive: true })
    // 0600 regardless of encryption: on a box with no Secret Service the seed
    // is in here in the clear, and file permissions are then the only guard.
    fs.writeFileSync(this.file, JSON.stringify(stored, null, 2), { mode: 0o600 })
  }

  private encrypt(seed: string): { secret: string; encrypted: boolean } {
    if (safeStorage.isEncryptionAvailable()) {
      return { secret: safeStorage.encryptString(seed).toString('base64'), encrypted: true }
    }
    // Linux with no keyring daemon. Degrading to a 0600 file is worse than the
    // Keychain and better than refusing to run, which is the actual choice.
    return { secret: seed, encrypted: false }
  }

  private decrypt(stored: StoredHost): string | null {
    if (!stored.encrypted) return stored.secret
    try {
      return safeStorage.decryptString(Buffer.from(stored.secret, 'base64'))
    } catch {
      // A key encrypted under a different login, or a corrupt keychain entry.
      // The host is unusable until it is paired again.
      return null
    }
  }
}

// ─── helpers ─────────────────────────────────────────────────────────────

export function parsePairingURL(input: string): { url: string; token: string } {
  const trimmed = input.trim()
  // Accept the scheme form and a bare query, since people paste both.
  const query = trimmed.includes('?') ? trimmed.slice(trimmed.indexOf('?') + 1) : trimmed
  const params = new URLSearchParams(query)
  const url = params.get('url')
  const token = params.get('token')
  if (!url || !token) throw new Error('pairing link needs both url and token')
  return { url: url.replace(/\/$/, ''), token }
}

function isLoopback(url: string): boolean {
  try {
    const host = new URL(url).hostname
    return host === '127.0.0.1' || host === 'localhost' || host === '::1' || host === '[::1]'
  } catch {
    return false
  }
}

/**
 * A host counts as local only if its socket directory is actually reachable —
 * a tailnet address for this same machine is remote as far as transport goes.
 */
function runDirReadable(): boolean {
  try {
    fs.accessSync(path.join(os.homedir(), '.helios', 'run'), fs.constants.R_OK)
    return true
  } catch {
    return false
  }
}

function hostnameOf(url: string): string {
  try {
    return new URL(url).hostname
  } catch {
    return url
  }
}

function stripSecret(stored: StoredHost): HostRecord {
  const { secret: _secret, encrypted: _encrypted, ...record } = stored
  return record
}

async function postJSON<T>(url: string, body: unknown): Promise<T> {
  const res = await fetch(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
    signal: AbortSignal.timeout(10_000),
  })
  const text = await res.text()
  const parsed = text ? (JSON.parse(text) as T) : ({} as T)
  if (!res.ok) {
    const detail = parsed as { message?: string; error?: string }
    throw new Error(detail.message ?? detail.error ?? `${url} returned ${res.status}`)
  }
  return parsed
}
