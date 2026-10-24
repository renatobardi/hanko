// Passkeys no perfil e reautenticação recente (ADR-0070). Fala só com /api/v1/me: o servidor decide o que vale, e o
// diálogo abre quando a API responde 401 reauth_required. Os textos vêm dos data-t-* da seção (traduzidos no servidor).
(function () {
  'use strict';

  // ---- WebAuthn: base64url <-> ArrayBuffer, e o JSON do servidor para as opções do navegador ----

  function b64uToBuf(s) {
    var b = s.replace(/-/g, '+').replace(/_/g, '/');
    while (b.length % 4) b += '=';
    var bin = atob(b);
    var out = new Uint8Array(bin.length);
    for (var i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
    return out.buffer;
  }

  function bufToB64u(buf) {
    var bytes = new Uint8Array(buf);
    var s = '';
    for (var i = 0; i < bytes.length; i++) s += String.fromCharCode(bytes[i]);
    return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
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
    return r.status === 401 && !!r.data && String(r.data.detail || '').indexOf('reauth_required') === 0;
  }

  function has(list, m) { return (list || []).indexOf(m) >= 0; }

  // ---- a seção do perfil ----

  function init(section) {
    var d = section.dataset;
    var t = { remove: d.tRemove, unnamed: d.tUnnamed, error: d.tError, cancelled: d.tCancelled, nomethod: d.tNomethod,
      nowebauthn: d.tNowebauthn };
    var list = section.querySelector('#passkey-list');
    var empty = section.querySelector('#passkey-empty');
    var nameInput = section.querySelector('#passkey-name');
    var addBtn = section.querySelector('#passkey-add');
    var errBox = section.querySelector('#passkey-error');
    var dialog = section.querySelector('#reauth-dialog');
    var pwForm = section.querySelector('#reauth-password');
    var pwInput = section.querySelector('#reauth-pass');
    var pkBtn = section.querySelector('#reauth-passkey');
    var cancelBtn = section.querySelector('#reauth-cancel');
    var reauthErr = section.querySelector('#reauth-error');
    var noMethod = section.querySelector('#reauth-nomethod');
    var windowSpan = section.querySelector('#reauth-window');

    var pending = null;      // resolve da prova em curso (o diálogo aberto)
    var pendingOpts = null;  // opções de prova devolvidas por /reauth/options

    function show(el, msg) {
      el.textContent = msg || '';
      el.hidden = !msg;
    }

    // failure: a falha da API vira erro mostrado ao usuário; reauth_required (prova cancelada) mostra o texto geral.
    function failure(r) {
      var e = new Error(!isReauth(r) && r.data && r.data.detail || t.error);
      e.api = true;
      return e;
    }

    // ---- diálogo de reautenticação ----

    function askProof() {
      return call('POST', '/reauth/options').then(function (r) {
        if (!r.ok) {
          show(errBox, (r.data && r.data.detail) || t.error);
          return false;
        }
        pendingOpts = r.data;
        pwForm.hidden = !has(r.data.methods, 'password');
        pkBtn.hidden = !has(r.data.methods, 'passkey') || !r.data.passkey;
        show(noMethod, pwForm.hidden && pkBtn.hidden ? t.nomethod : '');
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
        if (r.ok) finishProof(true); else show(reauthErr, (r.data && r.data.detail) || t.error);
      });
    });

    pkBtn.addEventListener('click', function () {
      var pk = pendingOpts && pendingOpts.passkey;
      if (!pk) return;
      show(reauthErr, '');
      navigator.credentials.get({ publicKey: requestOptions(pk.public_key.publicKey) }).then(function (assertion) {
        return call('POST', '/reauth', { method: 'passkey', challenge_id: pk.challenge_id, credential: credentialJSON(assertion) });
      }).then(function (r) {
        if (r.ok) finishProof(true); else show(reauthErr, (r.data && r.data.detail) || t.error);
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
        if (!r.ok) return show(errBox, (r.data && r.data.detail) || t.error);
        render((r.data && r.data.items) || []);
      });
    }

    function remove(id) {
      show(errBox, '');
      withReauth(function () { return call('DELETE', '/passkeys/' + encodeURIComponent(id)); }).then(function (r) {
        if (!r.ok) return show(errBox, (r.data && r.data.detail) || t.error);
        return load();
      });
    }

    function add() {
      show(errBox, '');
      withReauth(function () { return call('POST', '/passkeys/registration'); }).then(function (begin) {
        if (!begin.ok) throw failure(begin);
        return navigator.credentials.create({ publicKey: creationOptions(begin.data.public_key.publicKey) }).then(function (cred) {
          return withReauth(function () {
            return call('POST', '/passkeys', {
              challenge_id: begin.data.challenge_id,
              name: nameInput.value.trim(),
              credential: credentialJSON(cred)
            });
          });
        }, function () {
          throw new Error(t.cancelled);
        });
      }).then(function (fin) {
        if (!fin.ok) throw failure(fin);
        nameInput.value = '';
        return load();
      }).catch(function (e) {
        show(errBox, e && e.api ? e.message : (e && e.message) || t.error);
      });
    }

    addBtn.addEventListener('click', add);

    if (!window.PublicKeyCredential || !navigator.credentials) {
      addBtn.disabled = true;
      show(errBox, t.nowebauthn);
    }
    load();
  }

  // Módulo para o teste em node (internal/ui/passkey_js_test.go); no navegador não existe.
  if (typeof module !== 'undefined' && module.exports) {
    module.exports = { b64uToBuf: b64uToBuf, bufToB64u: bufToB64u, creationOptions: creationOptions,
      requestOptions: requestOptions, credentialJSON: credentialJSON };
  }
  if (typeof document !== 'undefined') {
    var section = document.getElementById('passkey-section');
    if (section) init(section);
  }
})();
