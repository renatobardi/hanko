// Tela de entrada do provider firebase. Usa o SDK web vendorizado (static/vendor/firebase),
// troca o ID token por uma sessão do Hanko em POST /auth/firebase e descarta a sessão do SDK.
import { initializeApp } from "./vendor/firebase/firebase-app.js";
import {
  getAuth, signInWithEmailAndPassword, signInWithPopup, GoogleAuthProvider, createUserWithEmailAndPassword,
  sendEmailVerification, sendPasswordResetEmail, signOut, updateProfile,
} from "./vendor/firebase/firebase-auth.js";

const form = document.getElementById("fb-form");
const msg = document.getElementById("fb-msg");
const submit = document.getElementById("fb-submit");
const email = document.getElementById("f-email");
const pass = document.getElementById("f-pass");
const toggle = document.getElementById("fb-toggle");
const toggleText = document.getElementById("fb-toggle-text");
const title = document.getElementById("fb-title");
const sub = document.getElementById("fb-sub");
let creating = false;

const app = initializeApp({
  apiKey: form.dataset.apiKey,
  authDomain: form.dataset.authDomain,
  projectId: form.dataset.projectId,
});
const auth = getAuth(app);
auth.languageCode = "pt-BR";

const messages = {
  "auth/invalid-credential": "E-mail ou senha incorretos.",
  "auth/invalid-email": "E-mail inválido.",
  "auth/user-not-found": "E-mail ou senha incorretos.",
  "auth/wrong-password": "E-mail ou senha incorretos.",
  "auth/user-disabled": "Esta conta está desativada.",
  "auth/too-many-requests": "Muitas tentativas. Aguarde alguns minutos e tente de novo.",
  "auth/email-already-in-use": "Este e-mail já tem conta. Entre com a senha ou use \"Esqueci a senha\".",
  "auth/weak-password": "Senha fraca: use ao menos 6 caracteres.",
  "auth/popup-closed-by-user": "Janela do Google fechada antes de concluir.",
  "auth/cancelled-popup-request": "",
  "auth/popup-blocked": "O navegador bloqueou a janela do Google. Libere pop-ups e tente de novo.",
  "auth/network-request-failed": "Falha de rede. Verifique a conexão.",
  "auth/unauthorized-domain": "Este endereço não está nos domínios autorizados do projeto Firebase.",
};

function show(text, kind = "danger", extra) {
  msg.replaceChildren();
  if (!text) return;
  const box = document.createElement("div");
  box.className = "alert " + (kind === "info" ? "" : kind);
  box.setAttribute("role", kind === "danger" ? "alert" : "status");
  const t = document.createElement("div");
  t.textContent = text;
  box.append(t);
  if (extra) t.append(" ", extra);
  msg.append(box);
}

function busy(on) {
  for (const el of form.querySelectorAll("button, input")) el.disabled = on;
}

async function exchange(user) {
  const idToken = await user.getIdToken(true);
  const res = await fetch("/auth/firebase", {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ id_token: idToken, next: form.dataset.next || "/", invite: form.dataset.invite || "" }),
  });
  let data = {};
  try { data = await res.json(); } catch (_) { /* corpo vazio */ }
  if (res.ok && data.redirect) {
    await signOut(auth).catch(() => {});
    window.location.assign(data.redirect);
    return;
  }
  if (data.code === "email_not_verified") {
    const again = document.createElement("button");
    again.type = "button";
    again.className = "link";
    again.textContent = "Reenviar e-mail de verificação";
    again.addEventListener("click", async () => {
      try { await sendEmailVerification(user); show("E-mail de verificação reenviado.", "info"); }
      catch (e) { show(messages[e.code] || "Não foi possível reenviar agora."); }
    });
    show(data.error || "E-mail não verificado.", "warn", again);
    return;
  }
  await signOut(auth).catch(() => {});
  show(data.error || "Não foi possível entrar.");
}

async function run(fn) {
  show("");
  busy(true);
  try {
    await fn();
  } catch (e) {
    const text = e.code in messages ? messages[e.code] : "Não foi possível entrar. Tente de novo.";
    show(text);
  } finally {
    busy(false);
  }
}

form.addEventListener("submit", (ev) => {
  ev.preventDefault();
  run(async () => {
    if (creating) {
      const cred = await createUserWithEmailAndPassword(auth, email.value.trim(), pass.value);
      await updateProfile(cred.user, { displayName: email.value.split("@")[0] }).catch(() => {});
      await sendEmailVerification(cred.user);
      await signOut(auth).catch(() => {});
      show("Conta criada. Enviamos um link de verificação para o seu e-mail; confirme e depois entre.", "info");
      setMode(false);
      return;
    }
    const cred = await signInWithEmailAndPassword(auth, email.value.trim(), pass.value);
    await exchange(cred.user);
  });
});

document.getElementById("fb-google").addEventListener("click", () => {
  run(async () => {
    const provider = new GoogleAuthProvider();
    provider.setCustomParameters({ prompt: "select_account" });
    const cred = await signInWithPopup(auth, provider);
    await exchange(cred.user);
  });
});

document.getElementById("fb-reset").addEventListener("click", () => {
  const addr = email.value.trim();
  if (!addr) { show("Informe o e-mail e clique de novo em \"Esqueci a senha\"."); email.focus(); return; }
  run(async () => {
    await sendPasswordResetEmail(auth, addr).catch((e) => {
      if (e.code !== "auth/user-not-found") throw e; // não revela se o e-mail existe
    });
    show("Se o e-mail estiver cadastrado, enviamos um link para redefinir a senha.", "info");
  });
});

function setMode(create) {
  creating = create;
  title.textContent = create ? "Criar conta" : "Entrar";
  sub.textContent = create ? "Use um e-mail e uma senha de, no mínimo, 6 caracteres." : "Use suas credenciais para acessar o Hanko.";
  submit.textContent = create ? "Criar conta" : "Entrar";
  pass.autocomplete = create ? "new-password" : "current-password";
  toggleText.textContent = create ? "Já tem conta?" : "Não tem conta?";
  toggle.textContent = create ? "Entrar" : "Criar conta";
}
toggle.addEventListener("click", () => { show(""); setMode(!creating); });
