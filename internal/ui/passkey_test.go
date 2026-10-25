package ui

import (
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/ui/uitest"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// newFirebaseProfileEnv monta o perfil com provider firebase. newPersonalEnv não liga o provider (s.auth fica nil).
func newFirebaseProfileEnv(t *testing.T, f *uitest.Fake) *env {
	t.Helper()
	s := &server{be: f, broker: events.NewBroker(), log: testLogger(t), version: "test", now: uitest.Now, sseTick: 50 * time.Millisecond}
	s.auth = newAuthState(AuthDeps{Provider: "firebase", Firebase: views.FirebaseWeb{APIKey: "key", AuthDomain: "p.firebaseapp.com", ProjectID: "p"}}, newFakeAuth(), s.now)
	mux := http.NewServeMux()
	s.routes(mux)
	return &env{f: f, broker: s.broker, h: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r.WithContext(auth.WithIdentity(r.Context(), auth.Identity{User: ana, Via: auth.ViaSession})))
	})}
}

// Com provider firebase, o perfil leva a configuração do SDK e o diálogo oferece a prova pelo Google (ADR-0070 §3). Com
// provider local, a configuração fica vazia e a senha continua no diálogo.
func TestProfilePageFirebaseOfereceProvaPeloGoogle(t *testing.T) {
	f := uitest.Sample()
	f.W3.Account = true
	f.W3.Profile = domain.UserProfile{Email: "ana@x.com", Name: "Ana"}
	fb := newFirebaseProfileEnv(t, f)
	rec := fb.get(t, "/c/hanko/perfil")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, `id="reauth-firebase"`, `data-fb-api-key="key"`, `data-fb-auth-domain="p.firebaseapp.com"`, `data-fb-project-id="p"`)

	local := newPersonalEnv(t, f)
	rec = local.get(t, "/c/hanko/perfil")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, `id="reauth-password"`)
	if strings.Contains(rec.Body.String(), `data-fb-api-key="key"`) {
		t.Fatal("conta local recebeu a configuração do Firebase")
	}

	js := fb.get(t, "/static/passkey.js")
	wantStatus(t, js, http.StatusOK)
	wantContains(t, js, "/static/vendor/firebase/firebase-auth.js", "'firebase'")
}

// A seção de passkeys no perfil e o diálogo de reautenticação (ADR-0070): a página traz a seção, e o JS que fala com
// /api/v1/me é servido junto.
func TestProfilePageMostraPasskeysEReauth(t *testing.T) {
	f := uitest.Sample()
	f.W3.Account = true
	f.W3.Profile = domain.UserProfile{Email: "ana@x.com", Name: "Ana"}
	e := newPersonalEnv(t, f)
	rec := e.get(t, "/c/hanko/perfil")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, `id="passkey-section"`, `id="reauth-dialog"`, `id="reauth-password"`, `/static/passkey.js`)

	js := e.get(t, "/static/passkey.js")
	wantStatus(t, js, http.StatusOK)
	wantContains(t, js, "/api/v1/me", "reauth_required")
}

// O JS converte o JSON do WebAuthn (base64url) para o navegador e o resultado de volta. Roda no node quando ele existe.
func TestPasskeyJSConverteBase64urlEOpcoes(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node não instalado")
	}
	path, err := filepath.Abs(filepath.Join("static", "passkey.js"))
	if err != nil {
		t.Fatal(err)
	}
	const script = `
const h = require(process.argv[1]);
const bytes = new Uint8Array([251, 255, 1, 2]);
const enc = h.bufToB64u(bytes.buffer);
if (/[+/=]/.test(enc)) throw new Error('não é base64url: ' + enc);
if (Array.from(new Uint8Array(h.b64uToBuf(enc))).join(',') !== Array.from(bytes).join(',')) throw new Error('ida e volta');
const pk = h.creationOptions({ challenge: enc, user: { id: enc, name: 'x' }, excludeCredentials: [{ type: 'public-key', id: enc }] });
if (!(pk.challenge instanceof ArrayBuffer) || !(pk.user.id instanceof ArrayBuffer) || !(pk.excludeCredentials[0].id instanceof ArrayBuffer)) throw new Error('opções de cadastro sem buffer');
const rq = h.requestOptions({ challenge: enc, allowCredentials: [{ type: 'public-key', id: enc }] });
if (!(rq.challenge instanceof ArrayBuffer) || !(rq.allowCredentials[0].id instanceof ArrayBuffer)) throw new Error('opções de prova sem buffer');
const cj = h.credentialJSON({ id: 'abc', rawId: new Uint8Array([9]).buffer, type: 'public-key',
  response: { clientDataJSON: new Uint8Array([1]).buffer, attestationObject: new Uint8Array([2]).buffer, getTransports: () => ['internal'] } });
if (cj.rawId !== 'CQ' || cj.response.transports[0] !== 'internal' || !cj.response.attestationObject) throw new Error('credentialJSON: ' + JSON.stringify(cj));
console.log('ok');
`
	out, err := exec.Command(node, "-e", script, path).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "ok" {
		t.Fatalf("saída do node = %q", out)
	}
}

// O JS escolhe as provas do diálogo e faz o login do Google pelo SDK (com um SDK falso): o ID token vem do login agora,
// a sessão do SDK sai depois, e um login recusado não vira prova. Roda no node quando ele existe.
func TestPasskeyJSEscolheProvasEFirebase(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node não instalado")
	}
	path, err := filepath.Abs(filepath.Join("static", "passkey.js"))
	if err != nil {
		t.Fatal(err)
	}
	const script = `
const h = require(process.argv[1]);
const eq = (a, b, what) => { if (JSON.stringify(a) !== JSON.stringify(b)) throw new Error(what + ': ' + JSON.stringify(a)); };
eq(h.proofChoices(['firebase'], true), { password: false, passkey: false, firebase: true }, 'firebase com configuração');
eq(h.proofChoices(['firebase'], false), { password: false, passkey: false, firebase: false }, 'firebase sem configuração');
eq(h.proofChoices(['password'], true), { password: true, passkey: false, firebase: false }, 'conta local');
eq(h.proofChoices(['passkey', 'password'], false), { password: true, passkey: true, firebase: false }, 'passkey e senha');
eq(h.proofChoices([], true), { password: false, passkey: false, firebase: false }, 'sem prova');
const calls = [];
const fb = {
  GoogleAuthProvider: function () { this.setCustomParameters = function (p) { calls.push('prompt=' + p.prompt); }; },
  signInWithPopup: function (a) { calls.push('popup:' + a); return Promise.resolve({ user: { getIdToken: function (force) { calls.push('token:' + force); return Promise.resolve('tok-novo'); } } }); },
  signOut: function (a) { calls.push('signOut:' + a); return Promise.resolve(); },
};
h.googleIDToken('auth', fb).then(function (tok) {
  eq(tok, 'tok-novo', 'ID token');
  eq(calls, ['prompt=select_account', 'popup:auth', 'token:true', 'signOut:auth'], 'ordem do login');
  const recusado = { GoogleAuthProvider: fb.GoogleAuthProvider, signInWithPopup: function () { return Promise.reject(new Error('popup-closed')); },
    signOut: function () { throw new Error('a sessão do SDK não devia sair'); } };
  return h.googleIDToken('auth', recusado).then(function () { throw new Error('login recusado virou prova'); },
    function (e) { eq(e.message, 'popup-closed', 'erro do login'); });
}).then(function () {
  console.log('ok');
}).catch(function (e) {
  console.error(e.message);
  process.exit(1);
});
`
	out, err := exec.Command(node, "-e", script, path).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "ok" {
		t.Fatalf("saída do node = %q", out)
	}
}

// Nas telas do cofre (.hkv e .env) o passkey.js sobe só o diálogo de prova (#92): o botão abre o diálogo, a prova sai por
// /api/v1/me/reauth (senha ou passkey), a tela avisa que a identidade foi confirmada, e prova cancelada ou falha não
// avisa nada. O DOM é falso: o teste confere o que o script pede à API e o que mostra. Roda no node quando ele existe.
func TestPasskeyJSProvaNasTelasDoCofre(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node não instalado")
	}
	path, err := filepath.Abs(filepath.Join("static", "passkey.js"))
	if err != nil {
		t.Fatal(err)
	}
	const script = `
const path = process.argv[1];
const eq = (a, b, what) => { if (JSON.stringify(a) !== JSON.stringify(b)) throw new Error(what + ': ' + JSON.stringify(a) + ' != ' + JSON.stringify(b)); };
const tick = () => new Promise(function (r) { setTimeout(r, 0); });

function el() {
  const e = { hidden: false, disabled: false, textContent: '', value: '', handlers: {}, open: false };
  e.addEventListener = function (ev, fn) { e.handlers[ev] = fn; };
  e.fire = function (ev) { return e.handlers[ev]({ preventDefault: function () {} }); };
  e.showModal = function () { e.open = true; };
  e.close = function () { e.open = false; };
  return e;
}

// run monta a página do cofre com o script, responde à API com 'respostas' (por método e caminho) e roda 'fluxo'.
async function run(fbApiKey, respostas, fluxo) {
  const ids = ['reauth-dialog', 'reauth-password', 'reauth-pass', 'reauth-passkey', 'reauth-firebase', 'reauth-cancel',
    'reauth-error', 'reauth-nomethod', 'reauth-window', 'reauth-prove', 'reauth-proved'];
  const els = {};
  ids.forEach(function (id) { els['#' + id] = el(); });
  els['#reauth-proved'].hidden = true;
  const section = { dataset: { tProved: 'Identidade confirmada.', tError: 'erro', tFirebase: 'erro-google', tCancelled: 'cancelada',
    tNomethod: 'sem método', fbApiKey: fbApiKey }, querySelector: function (sel) { return els[sel] || null; } };
  const chamadas = [];
  global.window = {};
  global.document = { getElementById: function (id) { return id === 'reauth-host' ? section : null; } };
  global.fetch = function (url, opts) {
    chamadas.push({ method: opts.method, url: url, body: opts.body ? JSON.parse(opts.body) : null });
    const r = respostas[opts.method + ' ' + url] || { status: 200, body: {} };
    return Promise.resolve({ ok: r.status < 400, status: r.status, text: function () { return Promise.resolve(JSON.stringify(r.body)); } });
  };
  Object.defineProperty(global, 'navigator', { configurable: true, writable: true, value: { credentials: { get: function () {
    const b = new Uint8Array([1, 2]).buffer;
    return Promise.resolve({ id: 'cred', rawId: b, type: 'public-key', response: { clientDataJSON: b, authenticatorData: b, signature: b } });
  } } } });
  delete require.cache[require.resolve(path)];
  require(path);
  await fluxo(els, chamadas);
}

const opcoes = function (metodos, passkey) {
  return { 'POST /api/v1/me/reauth/options': { status: 200, body: { methods: metodos, window_minutes: 5, passkey: passkey } } };
};

(async function () {
  // senha: o botão abre o diálogo só com a senha; o envio manda a senha à API e a tela avisa
  await run('', Object.assign(opcoes(['password']), { 'POST /api/v1/me/reauth': { status: 200, body: { method: 'password' } } }), async function (els, calls) {
    await els['#reauth-prove'].fire('click'); await tick();
    eq(els['#reauth-dialog'].open, true, 'diálogo aberto');
    eq([els['#reauth-password'].hidden, els['#reauth-passkey'].hidden, els['#reauth-firebase'].hidden], [false, true, true], 'só a senha');
    els['#reauth-pass'].value = 's3nha-da-conta';
    await els['#reauth-password'].fire('submit'); await tick();
    eq(calls[1], { method: 'POST', url: '/api/v1/me/reauth', body: { method: 'password', password: 's3nha-da-conta' } }, 'prova por senha');
    eq(els['#reauth-pass'].value, '', 'senha limpa do campo');
    eq([els['#reauth-dialog'].open, els['#reauth-proved'].hidden, els['#reauth-proved'].textContent], [false, false, 'Identidade confirmada.'], 'aviso');
  });

  // passkey: o botão da passkey assina o desafio e a API recebe o desafio e a credencial
  const pk = { challenge_id: 'ch-1', public_key: { publicKey: { challenge: 'AQI', allowCredentials: [] } } };
  await run('', Object.assign(opcoes(['passkey'], pk), { 'POST /api/v1/me/reauth': { status: 200, body: { method: 'passkey' } } }), async function (els, calls) {
    await els['#reauth-prove'].fire('click'); await tick();
    eq([els['#reauth-password'].hidden, els['#reauth-passkey'].hidden], [true, false], 'só a passkey');
    await els['#reauth-passkey'].fire('click'); await tick();
    eq([calls[1].url, calls[1].body.method, calls[1].body.challenge_id, calls[1].body.credential.id], ['/api/v1/me/reauth', 'passkey', 'ch-1', 'cred'], 'prova por passkey');
    eq([els['#reauth-proved'].hidden, els['#reauth-proved'].textContent], [false, 'Identidade confirmada.'], 'aviso');
  });

  // Google: com o botão à mostra, o SDK que não carrega não vira prova (a API não é chamada) e a tela mostra o erro
  await run('chave', opcoes(['firebase']), async function (els, calls) {
    await els['#reauth-prove'].fire('click'); await tick();
    eq(els['#reauth-firebase'].hidden, false, 'botão do Google');
    await els['#reauth-firebase'].fire('click'); await tick(); await tick();
    eq(calls.map(function (c) { return c.url; }), ['/api/v1/me/reauth/options'], 'sem prova sem o SDK');
    eq([els['#reauth-error'].textContent, els['#reauth-proved'].hidden], ['erro-google', true], 'erro do Google');
  });

  // prova recusada pela API e prova cancelada: nada de aviso de identidade confirmada
  await run('', Object.assign(opcoes(['password']), { 'POST /api/v1/me/reauth': { status: 401, body: { detail: 'senha errada' } } }), async function (els) {
    await els['#reauth-prove'].fire('click'); await tick();
    els['#reauth-pass'].value = 'x';
    await els['#reauth-password'].fire('submit'); await tick();
    eq([els['#reauth-error'].textContent, els['#reauth-dialog'].open, els['#reauth-proved'].hidden], ['senha errada', true, true], 'senha recusada');
    await els['#reauth-cancel'].fire('click'); await tick();
    eq([els['#reauth-dialog'].open, els['#reauth-proved'].hidden], [false, true], 'cancelada');
  });
  console.log('ok');
})().catch(function (e) { console.error(e.stack || e); process.exit(1); });
`
	out, err := exec.Command(node, "-e", script, path).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "ok" {
		t.Fatalf("saída do node = %q", out)
	}
}
