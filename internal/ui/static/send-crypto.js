// Cifra e decifra do envio por link (ADR-0069). Mesmos parâmetros que internal/service/sends.go: HKDF-SHA256 com
// info "hanko-send-v1" para a chave AES-256-GCM, AAD "send/<public_id>", nonce de 12 bytes na frente do texto cifrado
// e, com senha, PBKDF2-SHA256 de 600 000 iterações e prova HKDF com info "hanko-send-auth".

const enc = new TextEncoder();
const dec = new TextDecoder();

export const PBKDF2_ITERATIONS = 600000;
const KEY_INFO = 'hanko-send-v1';
const PROOF_INFO = 'hanko-send-auth';

export function b64uEncode(bytes) {
  let bin = '';
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

export function b64uDecode(text) {
  const pad = text.length % 4 === 0 ? '' : '='.repeat(4 - (text.length % 4));
  const bin = atob(text.replace(/-/g, '+').replace(/_/g, '/') + pad);
  return Uint8Array.from(bin, (c) => c.charCodeAt(0));
}

function concat(a, b) {
  const out = new Uint8Array(a.length + b.length);
  out.set(a, 0);
  out.set(b, a.length);
  return out;
}

async function hkdf(ikm, salt, info) {
  const key = await crypto.subtle.importKey('raw', ikm, 'HKDF', false, ['deriveBits']);
  const bits = await crypto.subtle.deriveBits({ name: 'HKDF', hash: 'SHA-256', salt, info: enc.encode(info) }, key, 256);
  return new Uint8Array(bits);
}

// derivePassword deriva a chave da senha (32 bytes) com PBKDF2-SHA256; o sal vem do envio (base64url).
export async function derivePassword(password, saltB64u) {
  const base = await crypto.subtle.importKey('raw', enc.encode(password), 'PBKDF2', false, ['deriveBits']);
  const bits = await crypto.subtle.deriveBits(
    { name: 'PBKDF2', hash: 'SHA-256', salt: b64uDecode(saltB64u), iterations: PBKDF2_ITERATIONS }, base, 256);
  return new Uint8Array(bits);
}

// proofFor é a prova da senha que o servidor confere (nunca a senha nem a chave derivada dela).
export function proofFor(pwdDerived) {
  return hkdf(pwdDerived, new Uint8Array(0), PROOF_INFO);
}

// aesKeyFor deriva a chave AES-GCM do envio: ikm = chave do link (+ senha derivada), sal = o do envio (vazio sem senha).
export async function aesKeyFor(linkKey, pwdDerived, salt) {
  const ikm = pwdDerived ? concat(linkKey, pwdDerived) : linkKey;
  const raw = await hkdf(ikm, salt, KEY_INFO);
  return crypto.subtle.importKey('raw', raw, 'AES-GCM', false, ['encrypt', 'decrypt']);
}

// decryptText decifra o texto de um envio. Chave ou senha erradas falham aqui (GCM confere a tag).
export async function decryptText(key, publicId, ciphertextB64u) {
  const buf = b64uDecode(ciphertextB64u);
  const plain = await crypto.subtle.decrypt(
    { name: 'AES-GCM', iv: buf.slice(0, 12), additionalData: enc.encode('send/' + publicId) }, key, buf.slice(12));
  return dec.decode(plain);
}

// encryptText cifra o texto de um envio novo (criação no navegador).
export async function encryptText(key, publicId, text) {
  const iv = crypto.getRandomValues(new Uint8Array(12));
  const sealed = await crypto.subtle.encrypt({ name: 'AES-GCM', iv, additionalData: enc.encode('send/' + publicId) }, key, enc.encode(text));
  return b64uEncode(concat(iv, new Uint8Array(sealed)));
}
