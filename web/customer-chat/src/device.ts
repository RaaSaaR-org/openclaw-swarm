// Device identity — Ed25519 keypair for gateway authentication
// Based on PinchChat's approach: generate keypair, store in IndexedDB, sign challenges

const DB_NAME = 'emai_chat_device';
const STORE_NAME = 'identity';
const KEY = 'device_v1';

export interface DeviceIdentity {
  id: string;              // SHA-256 hex fingerprint of public key
  publicKeyRaw: string;    // base64url-encoded raw 32-byte public key
  keyPair: CryptoKeyPair;
}

function bufToBase64Url(buf: ArrayBuffer): string {
  const bytes = new Uint8Array(buf);
  let binary = '';
  for (let i = 0; i < bytes.length; i++) {
    binary += String.fromCharCode(bytes[i]);
  }
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

function bufToHex(buf: ArrayBuffer): string {
  return Array.from(new Uint8Array(buf)).map(b => b.toString(16).padStart(2, '0')).join('');
}

async function openDB(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const req = indexedDB.open(DB_NAME, 1);
    req.onupgradeneeded = () => {
      req.result.createObjectStore(STORE_NAME);
    };
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
}

async function storeIdentity(jwk: { privateKey: JsonWebKey; publicKey: JsonWebKey; id: string; publicKeyRaw: string }) {
  const db = await openDB();
  return new Promise<void>((resolve, reject) => {
    const tx = db.transaction(STORE_NAME, 'readwrite');
    tx.objectStore(STORE_NAME).put(jwk, KEY);
    tx.oncomplete = () => resolve();
    tx.onerror = () => reject(tx.error);
  });
}

async function loadStoredIdentity(): Promise<{ privateKey: JsonWebKey; publicKey: JsonWebKey; id: string; publicKeyRaw: string } | null> {
  try {
    const db = await openDB();
    return new Promise((resolve) => {
      const tx = db.transaction(STORE_NAME, 'readonly');
      const req = tx.objectStore(STORE_NAME).get(KEY);
      req.onsuccess = () => resolve(req.result || null);
      req.onerror = () => resolve(null);
    });
  } catch {
    return null;
  }
}

export async function getOrCreateDevice(): Promise<DeviceIdentity> {
  // Try loading existing
  const stored = await loadStoredIdentity();
  if (stored) {
    console.log('[Device] Loaded existing device:', stored.id.slice(0, 12) + '...');
    const keyPair: CryptoKeyPair = {
      privateKey: await crypto.subtle.importKey('jwk', stored.privateKey, { name: 'Ed25519' }, false, ['sign']),
      publicKey: await crypto.subtle.importKey('jwk', stored.publicKey, { name: 'Ed25519' }, true, ['verify']),
    };
    return { id: stored.id, publicKeyRaw: stored.publicKeyRaw, keyPair };
  }

  // Generate new
  console.log('[Device] Generating new Ed25519 keypair...');
  const keyPair = await crypto.subtle.generateKey('Ed25519', true, ['sign', 'verify']) as CryptoKeyPair;

  // Extract raw public key (SPKI has 12-byte prefix for Ed25519)
  const spki = await crypto.subtle.exportKey('spki', keyPair.publicKey);
  const rawKey = new Uint8Array(spki).slice(12); // Remove SPKI header
  const publicKeyRaw = bufToBase64Url(rawKey.buffer);

  // Device ID = SHA-256 fingerprint of raw public key
  const hash = await crypto.subtle.digest('SHA-256', rawKey);
  const id = bufToHex(hash);

  // Store for persistence
  const privateJwk = await crypto.subtle.exportKey('jwk', keyPair.privateKey);
  const publicJwk = await crypto.subtle.exportKey('jwk', keyPair.publicKey);
  await storeIdentity({ privateKey: privateJwk, publicKey: publicJwk, id, publicKeyRaw });

  console.log('[Device] Created device:', id.slice(0, 12) + '...');
  return { id, publicKeyRaw, keyPair };
}

export async function signChallenge(
  device: DeviceIdentity,
  nonce: string,
  clientId: string,
  clientMode: string,
  role: string,
  scopes: string[],
  token: string,
): Promise<{ signature: string; signedAt: number }> {
  const signedAt = Date.now();

  // v2 canonical payload (pipe-delimited)
  const payload = `v2|${device.id}|${clientId}|${clientMode}|${role}|${scopes.join(',')}|${signedAt}|${token}|${nonce}`;

  console.log('[Device] Signing challenge, payload prefix:', payload.slice(0, 60) + '...');

  const data = new TextEncoder().encode(payload);
  const sig = await crypto.subtle.sign('Ed25519', device.keyPair.privateKey, data);
  const signature = bufToBase64Url(sig);

  return { signature, signedAt };
}
