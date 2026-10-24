package ui

import (
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/ui/uitest"
)

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
