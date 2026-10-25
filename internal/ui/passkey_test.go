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
