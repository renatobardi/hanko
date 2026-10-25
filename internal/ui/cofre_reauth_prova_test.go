package ui

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/ui/uitest"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// Prova de identidade nas telas do cofre (#92, ADR-0070 §3): export e import do .hkv e import de .env aceitam senha,
// Google e passkey. A senha vai no formulário; Google e passkey são provadas pelo diálogo do passkey.js em /api/v1/me,
// que grava a prova na sessão, e o formulário seguinte chega sem senha da conta.

// comSessaoProvadaPor é a sessão que o Guard monta depois de /me/reauth: a prova por method já está nela.
func comSessaoProvadaPor(e *env, method string) {
	inner := e.h
	e.h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		at := uitest.Now()
		sess := auth.SessionInfo{ReauthAt: &at, ReauthMethod: method}
		inner.ServeHTTP(w, r.WithContext(auth.WithIdentity(r.Context(), auth.Identity{User: ana, Via: auth.ViaSession, Session: sess})))
	})
}

// newCofreFirebaseEnv é o cofre com provider firebase e sessão: a página recebe a configuração do SDK do Google.
func newCofreFirebaseEnv(t *testing.T) (*env, *cofreFake) {
	t.Helper()
	f := uitest.Sample()
	f.MyRoleIs = domain.MemberAdmin
	cf := &cofreFake{Fake: f}
	s := &server{be: cf, broker: events.NewBroker(), version: "test", now: uitest.Now, sseTick: 50 * time.Millisecond, log: testLogger(t)}
	s.auth = newAuthState(AuthDeps{Provider: "firebase", Firebase: views.FirebaseWeb{APIKey: "key", AuthDomain: "p.firebaseapp.com", ProjectID: "p"}}, newFakeAuth(), s.now)
	mux := http.NewServeMux()
	s.routes(mux)
	e := &env{f: f, broker: s.broker, h: mux}
	comSessao(e)
	return e, cf
}

// hostDaProva são as marcas da prova por passkey ou Google na página: o botão, o diálogo e o JS compartilhados com o perfil.
var hostDaProva = []string{`id="reauth-host"`, `id="reauth-prove"`, `id="reauth-dialog"`, `id="reauth-passkey"`, `id="reauth-firebase"`, `/static/passkey.js`}

// Quando o servidor pede prova, as três telas oferecem passkey e Google além da senha, e a conta firebase leva a
// configuração do SDK. Sem o pedido, a página não carrega o diálogo.
func TestTelasDoCofreOferecemPasskeyEGoogleQuandoPedemProva(t *testing.T) {
	e, cf := newCofreFirebaseEnv(t)
	cf.needProof = true

	semPedido := e.get(t, cofreArquivoPage)
	wantStatus(t, semPedido, http.StatusOK)
	wantNotContains(t, semPedido, `id="reauth-host"`, `id="reauth-dialog"`)

	rec := e.post(t, cofreExportPath, url.Values{"senha_arquivo": {senhaArquivo}}, false)
	wantStatus(t, rec, http.StatusUnauthorized)
	wantContains(t, rec, append(hostDaProva, `name="senha"`, `data-fb-api-key="key"`, `data-fb-project-id="p"`, "passkey ou Google")...)

	rec = postMultipart(t, e, cofreImportPath, map[string]string{"senha_arquivo": senhaArquivo}, []byte(hkvExportado))
	wantStatus(t, rec, http.StatusUnauthorized)
	wantContains(t, rec, append(hostDaProva, `name="senha"`, `data-fb-api-key="key"`)...)

	rec = e.post(t, cofreEnvPage, envForm("gravar"), false)
	wantStatus(t, rec, http.StatusUnauthorized)
	wantContains(t, rec, append(hostDaProva, `name="senha"`, `data-fb-api-key="key"`, "DB_HOST")...)
}

// Conta local: o diálogo vem com a senha e sem a configuração do Firebase.
func TestTelasDoCofreContaLocalNaoLevaConfigDoFirebase(t *testing.T) {
	e, cf := newCofreEnv(t, domain.MemberAdmin)
	comSessao(e)
	cf.needProof = true
	rec := e.post(t, cofreEnvPage, envForm("gravar"), false)
	wantStatus(t, rec, http.StatusUnauthorized)
	wantContains(t, rec, `id="reauth-host"`, `id="reauth-password"`)
	if strings.Contains(rec.Body.String(), "data-fb-api-key=\"key\"") {
		t.Fatal("conta local recebeu a configuração do Firebase")
	}
}

// Uma prova por método (senha, Google, passkey) já gravada na sessão libera export, import do .hkv e import de .env sem
// senha da conta no formulário; o service recebe a prova com o método certo.
func TestProvaPorMetodoLiberaAsTresTelasDoCofre(t *testing.T) {
	for _, method := range []string{"password", "firebase", "passkey"} {
		t.Run(method, func(t *testing.T) {
			e, cf := newCofreEnv(t, domain.MemberAdmin)
			comSessaoProvadaPor(e, method)
			cf.needProof = true

			rec := e.post(t, cofreExportPath, url.Values{"senha_arquivo": {senhaArquivo}}, false)
			wantStatus(t, rec, http.StatusOK)
			wantContains(t, rec, hkvExportado)

			rec = postMultipart(t, e, cofreImportPath, map[string]string{"senha_arquivo": senhaArquivo}, []byte(hkvExportado))
			wantStatus(t, rec, http.StatusSeeOther)

			rec = e.post(t, cofreEnvPage, envForm("gravar"), false)
			wantStatus(t, rec, http.StatusSeeOther)

			if len(cf.reauthPasswords) != 0 {
				t.Fatalf("o formulário mandou senha da conta: %v", cf.reauthPasswords)
			}
			if cf.proved != 3 || len(cf.provedBy) != 3 {
				t.Fatalf("ações com prova = %d (%v), quer 3", cf.proved, cf.provedBy)
			}
			for _, m := range cf.provedBy {
				if m != method {
					t.Fatalf("método da prova = %q, quer %q", m, method)
				}
			}
		})
	}
}
