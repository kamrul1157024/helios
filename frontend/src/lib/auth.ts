const DB_NAME = 'helios-auth';
const STORE_NAME = 'keys';
const KEY_ID = 'device-key';
const DEVICE_ID_KEY = 'device-id';

interface StoredKey {
  kid: string;
  key: CryptoKey;
}

function openDB(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const req = indexedDB.open(DB_NAME, 2);
    req.onupgradeneeded = () => {
      const db = req.result;
      if (!db.objectStoreNames.contains(STORE_NAME)) {
        db.createObjectStore(STORE_NAME);
      }
    };
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
}

// Get or create the device's permanent unique ID (UUID stored in IndexedDB)
export async function getDeviceId(): Promise<string> {
  const db = await openDB();

  // Try to read existing device ID
  const existing = await new Promise<string | null>((resolve, reject) => {
    const tx = db.transaction(STORE_NAME, 'readonly');
    const req = tx.objectStore(STORE_NAME).get(DEVICE_ID_KEY);
    req.onsuccess = () => resolve(req.result || null);
    req.onerror = () => reject(req.error);
  });

  if (existing) return existing;

  // Generate new UUID
  const deviceId = crypto.randomUUID();

  await new Promise<void>((resolve, reject) => {
    const tx = db.transaction(STORE_NAME, 'readwrite');
    tx.objectStore(STORE_NAME).put(deviceId, DEVICE_ID_KEY);
    tx.oncomplete = () => resolve();
    tx.onerror = () => reject(tx.error);
  });

  return deviceId;
}

// Import the private key from QR and store it with this device's kid
export async function storeKey(privateKeyBase64: string): Promise<void> {
  const kid = await getDeviceId();
  const raw = base64urlToBytes(privateKeyBase64);
  const pkcs8 = wrapEd25519SeedInPKCS8(raw);

  const key = await crypto.subtle.importKey(
    'pkcs8',
    pkcs8,
    { name: 'Ed25519' },
    true, // extractable so we can export public key
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

// Export the public key as base64url for sending to backend.
// Uses JWK export which includes the "x" (public key) parameter.
export async function getPublicKeyBase64(): Promise<string> {
  const stored = await getStoredKey();
  if (!stored) throw new Error('No key stored');

  const jwk = await crypto.subtle.exportKey('jwk', stored.key);
  if (!jwk.x) throw new Error('No public key in JWK export');
  // JWK "x" is already base64url-encoded 32-byte Ed25519 public key
  return jwk.x;
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
    exp: now + 3600,
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

export async function clearKey(): Promise<void> {
  const db = await openDB();
  return new Promise((resolve, reject) => {
    const tx = db.transaction(STORE_NAME, 'readwrite');
    tx.objectStore(STORE_NAME).delete(KEY_ID);
    tx.oncomplete = () => resolve();
    tx.onerror = () => reject(tx.error);
  });
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
  const padded = str.replace(/-/g, '+').replace(/_/g, '/');
  const decoded = atob(padded);
  const bytes = new Uint8Array(decoded.length);
  for (let i = 0; i < decoded.length; i++) {
    bytes[i] = decoded.charCodeAt(i);
  }
  return bytes;
}

function wrapEd25519SeedInPKCS8(seed: Uint8Array): ArrayBuffer {
  const header = new Uint8Array([
    0x30, 0x2e, 0x02, 0x01, 0x00, 0x30, 0x05, 0x06,
    0x03, 0x2b, 0x65, 0x70, 0x04, 0x22, 0x04, 0x20,
  ]);
  const result = new Uint8Array(header.length + seed.length);
  result.set(header);
  result.set(seed, header.length);
  return result.buffer;
}
