// Passkeys no perfil e reautenticação recente (ADR-0070). Fala com /api/v1/me: o servidor decide o que vale, e o
// diálogo abre quando a API responde 401 reauth_required. No provider firebase, a prova é um login do Google pelo SDK
// (import sob demanda dos arquivos vendorizados). Os textos vêm dos data-t-* da seção (traduzidos no servidor).
(function () {
  'use strict';

  // ---- WebAuthn: base64url <-> ArrayBuffer, e o JSON do servidor para as opções do navegador ----

  function b64uToBuf(s) {
    var b = s.replaceAll('-', '+').replaceAll('_', '/');
    while (b.length % 4) b += '=';
    var bin = atob(b);
    return Uint8Array.from(bin, function (ch) { return ch.codePointAt(0); }).buffer;
  }

  function bufToB64u(buf) {
    var s = Array.from(new Uint8Array(buf), function (byte) { return String.fromCodePoint(byte); }).join('');
    var b = btoa(s).replaceAll('+', '-').replaceAll('/', '_');
    while (b.endsWith('=')) b = b.slice(0, -1);
    return b;
  }

  function creationOptions(pk) {
    pk.challenge = b64uToBuf(pk.challenge);
    pk.user.id = b64uToBuf(pk.user.id);
    (pk.excludeCredentials || []).forEach(function (c) { c.id = b64uToBuf(c.id); });
    return pk;
  }

  function requestOptions(pk) {
    pk.challenge = b64uToBuf(pk.challenge);
    (pk.allowCredentials || []).forEach(function (c) { c.id = b64uToBuf(c.id); });
    return pk;
  }

  // credentialJSON leva a resposta do navegador (create ou get) para o JSON que a API espera.
  function credentialJSON(c) {
    var r = c.response;
    var out = { id: c.id, rawId: bufToB64u(c.rawId), type: c.type, response: {} };
    out.response.clientDataJSON = bufToB64u(r.clientDataJSON);
    if (r.attestationObject) {
      out.response.attestationObject = bufToB64u(r.attestationObject);
      out.response.transports = r.getTransports ? r.getTransports() : [];
    } else {
      out.response.authenticatorData = bufToB64u(r.authenticatorData);
      out.response.signature = bufToB64u(r.signature);
      if (r.userHandle) out.response.userHandle = bufToB64u(r.userHandle);
    }
    return out;
  }

  // ---- Firebase: a prova é um login do Google pelo SDK (ADR-0070 §3). Só carrega o SDK quando a conta usa a prova. ----

  function loadFirebase() {
    return Promise.all([
      import('/static/vendor/firebase/firebase-app.js'),
      import('/static/vendor/firebase/firebase-auth.js'),
    ]).then(function (m) { return Object.assign({}, m[0], m[1]); });
  }

  // googleIDToken entra com o Google e devolve um ID token novo (auth_time agora). A sessão do SDK sai logo: quem
  // guarda a prova é o Hanko.
  function googleIDToken(auth, fb) {
    var provider = new fb.GoogleAuthProvider();
    provider.setCustomParameters({ prompt: 'select_account' });
    return fb.signInWithPopup(auth, provider).then(function (cred) {
      return cred.user.getIdToken(true).then(function (idToken) {
        return fb.signOut(auth).catch(function () {}).then(function () { return idToken; });
      });
    });
  }

  // ---- API ----

  var API = '/api/v1/me';

  function call(method, path, body) {
    var opts = { method: method, credentials: 'same-origin', headers: {} };
    if (body !== undefined) {
      opts.headers['Content-Type'] = 'application/json';
      opts.body = JSON.stringify(body);
    }
    return fetch(API + path, opts).then(function (res) {
      return res.text().then(function (text) {
        var data = null;
        try { data = text ? JSON.parse(text) : null; } catch (e) { data = null; }
        return { ok: res.ok, status: res.status, data: data };
      });
    });
  }

  function isReauth(r) {
    return r.status === 401 && !!r.data && String(r.data.detail || '').startsWith('reauth_required');
  }

  function has(list, m) { return (list || []).includes(m); }

  // proofChoices diz quais provas o diálogo oferece: só as que a API lista, e a do Google só com a configuração do
  // Firebase na página. Sem nenhuma, o diálogo avisa para entrar de novo.
  function proofChoices(methods, firebaseOn) {
    return {
      password: has(methods, 'password'),
      passkey: has(methods, 'passkey'),
      firebase: has(methods, 'firebase') && firebaseOn,
    };
  }

  function show(el, msg) {
    el.textContent = msg || '';
    el.hidden = !msg;
  }

  // ---- a seção do perfil ----

  function init(section) {
    var d = section.dataset;
    var t = { remove: d.tRemove, unnamed: d.tUnnamed, error: d.tError, cancelled: d.tCancelled, firebase: d.tFirebase,
      nomethod: d.tNomethod, nowebauthn: d.tNowebauthn };
    var list = section.querySelector('#passkey-list');
    var empty = section.querySelector('#passkey-empty');
    var nameInput = section.querySelector('#passkey-name');
    var addBtn = section.querySelector('#passkey-add');
    var errBox = section.querySelector('#passkey-error');
    var dialog = section.querySelector('#reauth-dialog');
    var pwForm = section.querySelector('#reauth-password');
    var pwInput = section.querySelector('#reauth-pass');
    var pkBtn = section.querySelector('#reauth-passkey');
    var fbBtn = section.querySelector('#reauth-firebase');
    var cancelBtn = section.querySelector('#reauth-cancel');
    var reauthErr = section.querySelector('#reauth-error');
    var noMethod = section.querySelector('#reauth-nomethod');
    var windowSpan = section.querySelector('#reauth-window');

    var pending = null;      // resolve da prova em curso (o diálogo aberto)
    var pendingOpts = null;  // opções de prova devolvidas por /reauth/options
    var firebaseReady = null; // SDK carregado e auth criado, uma vez por página

    function getFirebase() {
      if (!firebaseReady) {
        firebaseReady = loadFirebase().then(function (fb) {
          var app = fb.initializeApp({ apiKey: d.fbApiKey, authDomain: d.fbAuthDomain, projectId: d.fbProjectId });
          return { fb: fb, auth: fb.getAuth(app) };
        }).catch(function (e) {
          firebaseReady = null; // a próxima tentativa carrega de novo
          throw e;
        });
      }
      return firebaseReady;
    }

    // failure: a falha da API vira erro mostrado ao usuário; reauth_required (prova cancelada) mostra o texto geral.
    function failure(r) {
      var e = new Error((!isReauth(r) && r.data?.detail) || t.error);
      e.api = true;
      return e;
    }

    // ---- diálogo de reautenticação ----

    function askProof() {
      return call('POST', '/reauth/options').then(function (r) {
        if (!r.ok) {
          show(errBox, r.data?.detail || t.error);
          return Promise.resolve(false);
        }
        pendingOpts = r.data;
        var choice = proofChoices(r.data.methods, !!d.fbApiKey);
        pwForm.hidden = !choice.password;
        pkBtn.hidden = !choice.passkey || !r.data.passkey;
        fbBtn.hidden = !choice.firebase;
        show(noMethod, pwForm.hidden && pkBtn.hidden && fbBtn.hidden ? t.nomethod : '');
        windowSpan.textContent = String(r.data.window_minutes);
        show(reauthErr, '');
        if (dialog.showModal) dialog.showModal(); else dialog.setAttribute('open', '');
        return new Promise(function (resolve) { pending = resolve; });
      });
    }

    function finishProof(ok) {
      if (dialog.close) dialog.close(); else dialog.removeAttribute('open');
      var resolve = pending;
      pending = null;
      if (resolve) resolve(ok);
    }

    cancelBtn.addEventListener('click', function () { finishProof(false); });

    pwForm.addEventListener('submit', function (ev) {
      ev.preventDefault();
      var password = pwInput.value;
      pwInput.value = '';
      call('POST', '/reauth', { method: 'password', password: password }).then(function (r) {
        if (r.ok) finishProof(true); else show(reauthErr, r.data?.detail || t.error);
      }).catch(function () {
        show(reauthErr, t.error);
      });
    });

    fbBtn.addEventListener('click', function () {
      show(reauthErr, '');
      fbBtn.disabled = true;
      getFirebase().then(function (fbs) {
        return googleIDToken(fbs.auth, fbs.fb);
      }).then(function (idToken) {
        return call('POST', '/reauth', { method: 'firebase', id_token: idToken });
      }).then(function (r) {
        if (r.ok) finishProof(true); else show(reauthErr, r.data?.detail || t.firebase);
      }, function () {
        show(reauthErr, t.firebase);
      }).then(function () {
        fbBtn.disabled = false;
      });
    });

    pkBtn.addEventListener('click', function () {
      var pk = pendingOpts?.passkey;
      if (!pk) return;
      show(reauthErr, '');
      navigator.credentials.get({ publicKey: requestOptions(pk.public_key.publicKey) }).then(function (assertion) {
        return call('POST', '/reauth', { method: 'passkey', challenge_id: pk.challenge_id, credential: credentialJSON(assertion) });
      }).then(function (r) {
        if (r.ok) finishProof(true); else show(reauthErr, r.data?.detail || t.error);
      }, function () {
        show(reauthErr, t.cancelled);
      });
    });

    // withReauth roda a ação; se a API pedir prova recente, abre o diálogo e repete a ação uma vez.
    function withReauth(action) {
      return action().then(function (r) {
        if (!isReauth(r)) return r;
        return askProof().then(function (ok) { return ok ? action() : r; });
      });
    }

    // ---- lista e ações ----

    function render(items) {
      list.textContent = '';
      empty.hidden = items.length > 0;
      items.forEach(function (p) {
        var li = document.createElement('li');
        li.className = 'passkey-item';
        var name = document.createElement('b');
        name.textContent = p.name || t.unnamed;
        var when = document.createElement('span');
        when.className = 'muted';
        when.textContent = new Date(p.created_at).toLocaleDateString();
        var del = document.createElement('button');
        del.type = 'button';
        del.className = 'btn';
        del.textContent = t.remove;
        del.addEventListener('click', function () { remove(p.id); });
        li.appendChild(name);
        li.appendChild(when);
        li.appendChild(del);
        list.appendChild(li);
      });
    }

    function load() {
      return call('GET', '/passkeys').then(function (r) {
        if (!r.ok) return show(errBox, r.data?.detail || t.error);
        render(r.data?.items || []);
      });
    }

    function remove(id) {
      show(errBox, '');
      withReauth(function () { return call('DELETE', '/passkeys/' + encodeURIComponent(id)); }).then(function (r) {
        if (!r.ok) return show(errBox, r.data?.detail || t.error);
        return load();
      }).catch(function () {
        show(errBox, t.error);
      });
    }

    function beginRegistration() {
      return call('POST', '/passkeys/registration');
    }

    function finishRegistration(begin, cred) {
      return call('POST', '/passkeys', {
        challenge_id: begin.data.challenge_id,
        name: nameInput.value.trim(),
        credential: credentialJSON(cred)
      });
    }

    // createCredential: o navegador cria a passkey; a conclusão no servidor passa pela reautenticação, se pedir.
    function createCredential(begin) {
      return navigator.credentials.create({ publicKey: creationOptions(begin.data.public_key.publicKey) })
        .catch(function () { throw new Error(t.cancelled); })
        .then(function (cred) {
          return withReauth(function () { return finishRegistration(begin, cred); });
        });
    }

    function add() {
      show(errBox, '');
      withReauth(beginRegistration).then(function (begin) {
        if (!begin.ok) throw failure(begin);
        return createCredential(begin);
      }).then(function (fin) {
        if (!fin.ok) throw failure(fin);
        nameInput.value = '';
        return load();
      }).catch(function (e) {
        show(errBox, e?.api ? e.message : (e?.message || t.error));
      });
    }

    addBtn.addEventListener('click', add);

    if (!window.PublicKeyCredential || !navigator.credentials) {
      addBtn.disabled = true;
      show(errBox, t.nowebauthn);
    }
    load().catch(function () {
      show(errBox, t.error);
    });
  }

  // Módulo para o teste em node (internal/ui/passkey_test.go); no navegador não existe.
  if (typeof module !== 'undefined' && module.exports) {
    module.exports = { b64uToBuf: b64uToBuf, bufToB64u: bufToB64u, creationOptions: creationOptions,
      requestOptions: requestOptions, credentialJSON: credentialJSON, proofChoices: proofChoices,
      googleIDToken: googleIDToken };
  }
  if (typeof document !== 'undefined') {
    var section = document.getElementById('passkey-section');
    if (section) init(section);
  }
})();
