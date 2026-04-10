const DB_NAME = 'helios-auth';
const STORE_NAME = 'keys';
const KEY_ID = 'device-key';

interface StoredKey {
  kid: string;
  key: CryptoKey;
}

function openDB(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const req = indexedDB.open(DB_NAME, 1);
    req.onupgradeneeded = () => {
      req.result.createObjectStore(STORE_NAME);
    };
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
}

export async function storeKey(privateKeyBase64: string, kid: string): Promise<void> {
  // Decode base64url to raw bytes (32-byte Ed25519 seed)
  const raw = base64urlToBytes(privateKeyBase64);

  // Import as Ed25519 signing key (PKCS8 format needed for Web Crypto)
  // Ed25519 seed is 32 bytes, need to wrap in PKCS8
  const pkcs8 = wrapEd25519SeedInPKCS8(raw);

  const key = await crypto.subtle.importKey(
    'pkcs8',
    pkcs8,
    { name: 'Ed25519' },
    false, // non-extractable
    ['sign']
  );

  const db = await openDB();
  return new Promise((resolve, reject) => {
    const tx = db.transaction(STORE_NAME, 'readwrite');
    tx.objectStore(STORE_NAME).put({ kid, key } as StoredKey, KEY_ID);
    tx.oncomplete = () => resolve();
    tx.onerror = () => reject(tx.error);
  });
}

export async function hasKey(): Promise<boolean> {
  try {
    const stored = await getStoredKey();
    return stored !== null;
  } catch {
    return false;
  }
}

export async function getStoredKey(): Promise<StoredKey | null> {
  const db = await openDB();
  return new Promise((resolve, reject) => {
    const tx = db.transaction(STORE_NAME, 'readonly');
    const req = tx.objectStore(STORE_NAME).get(KEY_ID);
    req.onsuccess = () => resolve(req.result || null);
    req.onerror = () => reject(req.error);
  });
}

export async function signJWT(): Promise<string> {
  const stored = await getStoredKey();
  if (!stored) throw new Error('No key stored');

  const header = {
    alg: 'EdDSA',
    typ: 'JWT',
    kid: stored.kid,
  };

  const now = Math.floor(Date.now() / 1000);
  const payload = {
    iat: now,
    exp: now + 3600, // 1 hour
    sub: 'helios-client',
  };

  const encodedHeader = base64urlEncode(JSON.stringify(header));
  const encodedPayload = base64urlEncode(JSON.stringify(payload));
  const signingInput = `${encodedHeader}.${encodedPayload}`;

  const signature = await crypto.subtle.sign(
    { name: 'Ed25519' },
    stored.key,
    new TextEncoder().encode(signingInput)
  );

  const encodedSignature = bytesToBase64url(new Uint8Array(signature));
  return `${signingInput}.${encodedSignature}`;
}

export async function getAuthHeader(): Promise<string> {
  const token = await signJWT();
  return `Bearer ${token}`;
}

export async function clearKey(): Promise<void> {
  const db = await openDB();
  return new Promise((resolve, reject) => {
    const tx = db.transaction(STORE_NAME, 'readwrite');
    tx.objectStore(STORE_NAME).delete(KEY_ID);
    tx.oncomplete = () => resolve();
    tx.onerror = () => reject(tx.error);
  });
}

// Parse helios://setup?key=...&kid=...&v=1
export function parseSetupPayload(input: string): { key: string; kid: string } | null {
  try {
    const url = new URL(input);
    const key = url.searchParams.get('key');
    const kid = url.searchParams.get('kid');
    if (!key || !kid) return null;
    return { key, kid };
  } catch {
    return null;
  }
}

// Helper: base64url encode/decode
function base64urlEncode(str: string): string {
  return btoa(str).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

function bytesToBase64url(bytes: Uint8Array): string {
  let binary = '';
  for (const b of bytes) binary += String.fromCharCode(b);
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

function base64urlToBytes(str: string): Uint8Array {
  // Add padding
  const padded = str.replace(/-/g, '+').replace(/_/g, '/');
  const decoded = atob(padded);
  const bytes = new Uint8Array(decoded.length);
  for (let i = 0; i < decoded.length; i++) {
    bytes[i] = decoded.charCodeAt(i);
  }
  return bytes;
}

// Wrap a 32-byte Ed25519 seed in PKCS8 DER format
function wrapEd25519SeedInPKCS8(seed: Uint8Array): ArrayBuffer {
  // PKCS8 header for Ed25519:
  // 30 2e (SEQUENCE, 46 bytes)
  //   02 01 00 (INTEGER 0 - version)
  //   30 05 (SEQUENCE, 5 bytes)
  //     06 03 2b 65 70 (OID 1.3.101.112 - Ed25519)
  //   04 22 (OCTET STRING, 34 bytes)
  //     04 20 (OCTET STRING, 32 bytes)
  //       <32 bytes of seed>
  const header = new Uint8Array([
    0x30, 0x2e, 0x02, 0x01, 0x00, 0x30, 0x05, 0x06,
    0x03, 0x2b, 0x65, 0x70, 0x04, 0x22, 0x04, 0x20,
  ]);
  const result = new Uint8Array(header.length + seed.length);
  result.set(header);
  result.set(seed, header.length);
  return result.buffer;
}
