import crypto from 'node:crypto'

/**
 * Device identity: an Ed25519 keypair whose public half the daemon stores and
 * whose private half signs a short-lived JWT for every request. Same scheme as
 * the mobile client (mobile/lib/services/api_client.dart), reimplemented here.
 */

/** DER prefix for a PKCS#8-wrapped Ed25519 private key, followed by the 32-byte seed. */
const PKCS8_ED25519_PREFIX = Buffer.from('302e020100300506032b657004220420', 'hex')

export interface DeviceKey {
  /** Device id, sent as the JWT `kid` and used as the daemon's device handle. */
  kid: string
  /** 32-byte Ed25519 seed, base64url. Secret — belongs in safeStorage only. */
  seed: string
}

export function generateDeviceKey(): DeviceKey {
  return { kid: crypto.randomUUID(), seed: b64url(crypto.randomBytes(32)) }
}

function privateKeyFromSeed(seed: string): crypto.KeyObject {
  const raw = Buffer.from(seed, 'base64url')
  if (raw.length !== 32) throw new Error(`device seed must be 32 bytes, got ${raw.length}`)
  return crypto.createPrivateKey({
    key: Buffer.concat([PKCS8_ED25519_PREFIX, raw]),
    format: 'der',
    type: 'pkcs8',
  })
}

/** The raw 32-byte public key, base64url — the encoding PublicKeyFromBase64 expects. */
export function publicKeyBase64(seed: string): string {
  const spki = crypto.createPublicKey(privateKeyFromSeed(seed)).export({ format: 'der', type: 'spki' })
  // SPKI is a 12-byte header followed by the raw key; the daemon wants the key.
  return b64url(spki.subarray(spki.length - 32))
}

/**
 * Signs a self-issued EdDSA JWT. The daemon looks the device up by `kid` and
 * verifies with the public key it was paired with, so nothing is granted here
 * that the pairing did not already grant.
 */
export function signJWT(key: DeviceKey, lifetimeSeconds = 3600): string {
  const header = { alg: 'EdDSA', typ: 'JWT', kid: key.kid }
  const now = Math.floor(Date.now() / 1000)
  const payload = { iat: now, exp: now + lifetimeSeconds, sub: 'helios-client' }

  const signingInput = `${b64urlJSON(header)}.${b64urlJSON(payload)}`
  // null algorithm: Ed25519 signs the message directly rather than a digest.
  const signature = crypto.sign(null, Buffer.from(signingInput), privateKeyFromSeed(key.seed))
  return `${signingInput}.${b64url(signature)}`
}

function b64urlJSON(value: unknown): string {
  return b64url(Buffer.from(JSON.stringify(value)))
}

function b64url(buf: Buffer): string {
  return buf.toString('base64url')
}
