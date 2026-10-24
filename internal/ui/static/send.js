// Página de recebimento de um envio (ADR-0069 §6): lê o id do caminho e a chave do fragmento, pede a senha se houver,
// abre o envio pela API pública e decifra no navegador. A chave não é enviada ao servidor.
import { aesKeyFor, b64uDecode, b64uEncode, decryptText, derivePassword, proofFor } from '/static/send-crypto.js';

const LINK_KEY_BYTES = 32;
const publicId = location.pathname.split('/').filter(Boolean).pop() || '';
const status = document.getElementById('status');
const form = document.getElementById('password-form');
const result = document.getElementById('result');
const content = document.getElementById('content');

function say(text) {
  status.textContent = text;
}

// Tira a chave da barra de endereço e do histórico assim que ela é lida.
function readLinkKey() {
  const fragment = location.hash.replace(/^#/, '');
  history.replaceState(null, '', location.pathname);
  if (!fragment) return null;
  try {
    const key = b64uDecode(fragment);
    return key.length === LINK_KEY_BYTES ? key : null;
  } catch {
    return null;
  }
}

async function getInfo() {
  const res = await fetch('/api/v1/s/' + publicId, { headers: { accept: 'application/json' } });
  if (!res.ok) return null;
  return res.json();
}

async function openSend(info, linkKey, password) {
  say('Abrindo…');
  try {
    let pwdDerived = null;
    const body = {};
    if (info.has_password) {
      pwdDerived = await derivePassword(password, info.kdf_salt);
      body.password_proof = b64uEncode(await proofFor(pwdDerived));
    }
    const res = await fetch('/api/v1/s/' + publicId + '/open', {
      method: 'POST',
      headers: { 'content-type': 'application/json', accept: 'application/json' },
      body: JSON.stringify(body),
    });
    if (res.status === 403) return say('Senha incorreta.');
    if (res.status === 409) return say('Envio bloqueado por senha errada repetida. Tente mais tarde.');
    if (!res.ok) return say('Este envio não está mais disponível. Ele venceu, atingiu o limite de acessos ou foi apagado.');
    const opened = await res.json();
    const salt = info.kdf_salt ? b64uDecode(info.kdf_salt) : new Uint8Array(0);
    const key = await aesKeyFor(linkKey, pwdDerived, salt);
    content.textContent = await decryptText(key, publicId, opened.ciphertext);
    form.hidden = true;
    result.hidden = false;
    say('Conteúdo aberto. Este acesso já foi contado.');
  } catch {
    say('Não foi possível decifrar o envio. Confira o link e a senha.');
  }
}

async function main() {
  const linkKey = readLinkKey();
  if (!linkKey) return say('Este link está incompleto ou alterado: falta a chave depois do #.');
  const info = await getInfo();
  if (!info) return say('Este envio não existe, venceu ou foi apagado.');
  if (!info.has_password) return openSend(info, linkKey, null);
  say('Digite a senha para abrir este envio.');
  form.hidden = false;
  form.addEventListener('submit', (event) => {
    event.preventDefault();
    const password = document.getElementById('password').value;
    openSend(info, linkKey, password);
  });
}

main();
