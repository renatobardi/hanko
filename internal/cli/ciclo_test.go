package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/renatobardi/hanko/internal/api"
)

// loginServer é o servidor fake do login pelo navegador: pendente nas duas primeiras consultas, depois aprovado.
func loginServer(t *testing.T, status string) (url string, f *fakeAPI) {
	t.Helper()
	var polls atomic.Int32
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"POST /api/v1/cli-auth": jsonResp(201, api.CLIAuthStarted{Code: "CODE123", PollToken: "poll-secret", VerifyPath: "/cli-auth/CODE123", IntervalSec: 1}),
		"POST /api/v1/cli-auth/poll": func(w http.ResponseWriter, r *http.Request) {
			if polls.Add(1) < 3 {
				jsonResp(200, api.CLIAuthPolled{Status: "pending"})(w, r)
				return
			}
			if status == "approved" {
				jsonResp(200, api.CLIAuthPolled{Status: "approved", APIKey: "hk_secret-key", User: &api.Whoami{Email: "ana@x.com"}})(w, r)
				return
			}
			jsonResp(200, api.CLIAuthPolled{Status: status})(w, r)
		},
		"GET /api/v1/whoami": jsonResp(200, api.Whoami{Authenticated: true, Email: "ana@x.com", Role: "member", Via: "api_key",
			Key: &api.APIKeyInfo{Name: "notebook", Prefix: "hk_secre", Scope: "read", Device: "notebook"}}),
	})
	return ts.URL, f
}

func TestLoginWhoamiLogout(t *testing.T) {
	url, f := loginServer(t, "approved")
	dir := t.TempDir()
	env := map[string]string{"HANKO_CONFIG_DIR": dir, "HANKO_LOGIN_POLL": "1ms"}

	code, out, errb := exec(t, Runtime{}, env, "", "--url", url, "login", "--no-browser", "--device", "notebook", "--scope", "read")
	if code != 0 {
		t.Fatalf("login: %d %s %s", code, out, errb)
	}
	if !strings.Contains(out, url+"/cli-auth/CODE123") || !strings.Contains(out, "Login feito como ana@x.com") {
		t.Fatalf("saída: %q", out)
	}
	// o pedido é público: nenhuma credencial foi enviada
	if h := f.hits[0]; h.Auth != "" || !strings.Contains(h.Body, `"device":"notebook"`) || !strings.Contains(h.Body, `"scope":"read"`) {
		t.Errorf("criação: %+v", h)
	}
	// o contexto foi gravado com permissão restrita
	p := filepath.Join(dir, "contexts.json")
	st, err := os.Stat(p)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("arquivo: %v %v", st, err)
	}
	var cs cliContexts
	b, _ := os.ReadFile(p)
	if err := json.Unmarshal(b, &cs); err != nil || cs.Current != "default" || cs.Contexts["default"].Token != "hk_secret-key" || cs.Contexts["default"].URL != url {
		t.Fatalf("contextos: %s %v", b, err)
	}

	// whoami usa o contexto salvo (sem --url nem --token)
	code, out, errb = exec(t, Runtime{}, env, "", "whoami")
	if code != 0 {
		t.Fatalf("whoami: %d %s %s", code, out, errb)
	}
	if h := f.last(); h.Auth != "Bearer hk_secret-key" {
		t.Errorf("whoami sem a chave do contexto: %+v", h)
	}
	for _, want := range []string{"ana@x.com", "notebook", "read"} {
		if !strings.Contains(out, want) {
			t.Errorf("whoami sem %q: %s", want, out)
		}
	}
	// --token vence o contexto
	exec(t, Runtime{}, env, "", "--url", url, "--token", "hk_outra", "whoami")
	if h := f.last(); h.Auth != "Bearer hk_outra" {
		t.Errorf("--token deveria vencer: %+v", h)
	}

	code, out, _ = exec(t, Runtime{}, env, "", "logout")
	if code != 0 || !strings.Contains(out, "removido") {
		t.Fatalf("logout: %d %s", code, out)
	}
	if _, ok := (&env0{}).ctxOf(t, dir); ok {
		t.Error("contexto deveria ter sumido")
	}
}

// env0 só agrupa o helper de leitura do arquivo de contextos.
type env0 struct{}

func (env0) ctxOf(t *testing.T, dir string) (cliContext, bool) {
	t.Helper()
	var cs cliContexts
	b, err := os.ReadFile(filepath.Join(dir, "contexts.json"))
	if err != nil {
		return cliContext{}, false
	}
	_ = json.Unmarshal(b, &cs)
	c, ok := cs.Contexts[cs.Current]
	return c, ok
}

func TestLoginNegadoEExpirado(t *testing.T) {
	for status, want := range map[string]string{"denied": "negado", "consumed": "já foi usado"} {
		url, _ := loginServer(t, status)
		dir := t.TempDir()
		code, _, errb := exec(t, Runtime{}, map[string]string{"HANKO_CONFIG_DIR": dir, "HANKO_LOGIN_POLL": "1ms"}, "", "--url", url, "login", "--no-browser")
		if code == 0 || !strings.Contains(errb, want) {
			t.Errorf("%s: %d %q", status, code, errb)
		}
		if _, err := os.Stat(filepath.Join(dir, "contexts.json")); err == nil {
			t.Errorf("%s: não deveria gravar contexto", status)
		}
	}
	// pedido vencido (404 no poll)
	ts, _ := newFakeAPI(t, map[string]http.HandlerFunc{
		"POST /api/v1/cli-auth":      jsonResp(201, api.CLIAuthStarted{Code: "C", PollToken: "p", VerifyPath: "/cli-auth/C", IntervalSec: 1}),
		"POST /api/v1/cli-auth/poll": jsonResp(404, map[string]any{"title": "Não encontrado", "status": 404}),
	})
	code, _, errb := exec(t, Runtime{}, map[string]string{"HANKO_CONFIG_DIR": t.TempDir(), "HANKO_LOGIN_POLL": "1ms"}, "", "--url", ts.URL, "login", "--no-browser")
	if code == 0 || !strings.Contains(errb, "expirou") {
		t.Errorf("expirado: %d %q", code, errb)
	}
	if code, _, _ := exec(t, Runtime{}, nil, "", "login", "--scope", "root"); code != exitUsage {
		t.Errorf("escopo inválido: %d", code)
	}
}

func TestCompanyLifecycleCmds(t *testing.T) {
	co := api.Company{ID: "id-1", Slug: "acme", Name: "Acme", TaskPrefix: "HNK", Timezone: "UTC"}
	arch := co
	now := arch.CreatedAt
	arch.ArchivedAt = &now
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"PATCH /api/v1/companies/acme":            jsonResp(200, co),
		"POST /api/v1/companies/acme/archive":     jsonResp(200, arch),
		"POST /api/v1/companies/acme/restore":     jsonResp(200, co),
		"DELETE /api/v1/companies/acme":           jsonResp(200, api.CompanyDeletion{ID: "d1", Slug: "acme", Name: "Acme", ExportBytes: 10, SHA256: "abcdef0123456789"}),
		"GET /api/v1/company-deletions/d1/export": jsonResp(200, map[string]any{"format": "hanko.company.v1"}),
	})
	env := map[string]string{"HANKO_URL": ts.URL, "HANKO_COMPANY": "acme"}
	if code, _, errb := exec(t, Runtime{}, env, "", "company", "edit"); code != exitUsage {
		t.Errorf("edit sem flags: %d %s", code, errb)
	}
	if code, out, errb := exec(t, Runtime{}, env, "", "company", "edit", "--name", "Acme 2", "--prefix", "AC"); code != 0 {
		t.Fatalf("edit: %d %s %s", code, out, errb)
	}
	if h := f.last(); h.Body != `{"name":"Acme 2","task_prefix":"AC"}` {
		t.Errorf("corpo do edit: %s", h.Body)
	}
	if code, out, _ := exec(t, Runtime{}, env, "", "company", "archive"); code != 0 || !strings.Contains(out, "arquivada") {
		t.Errorf("archive: %d %s", code, out)
	}
	if code, _, _ := exec(t, Runtime{}, env, "", "company", "restore", "--resume"); code != 0 {
		t.Errorf("restore: %d", code)
	}
	if h := f.last(); h.Body != `{"resume":true}` {
		t.Errorf("corpo do restore: %s", h.Body)
	}
	if code, _, _ := exec(t, Runtime{}, env, "", "company", "delete"); code != exitUsage {
		t.Errorf("delete sem --confirm: %d", code)
	}
	out := filepath.Join(t.TempDir(), "backup.json")
	if code, o, errb := exec(t, Runtime{}, env, "", "company", "delete", "--confirm", "Acme", "-o", out); code != 0 || !strings.Contains(o, "excluída") {
		t.Fatalf("delete: %d %s %s", code, o, errb)
	}
	if b, err := os.ReadFile(out); err != nil || !strings.Contains(string(b), "hanko.company.v1") {
		t.Errorf("export baixado: %q %v", b, err)
	}
}

func TestImportPreviaNaCLI(t *testing.T) {
	res := api.ImportedCompany{Company: api.Company{Name: "Acme", Slug: "acme"}, Counts: map[string]int{"personas": 1}, DryRun: true, Merged: true,
		Items:    []api.ImportItem{{Kind: "personas", Key: "dev", Name: "Dev", Action: "rename", Target: "dev-2"}},
		Warnings: []api.ImportWarning{{Code: "personas_paused", Message: "%d persona(s) entram pausadas até alguém liberar", Count: 1}}}
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{"POST /api/v1/companies/import": jsonResp(200, res)})
	env := map[string]string{"HANKO_URL": ts.URL}
	if code, _, _ := exec(t, Runtime{}, env, `{"format":"hanko.company.v1"}`, "import", "-", "--on-conflict", "skip"); code != exitUsage {
		t.Errorf("on-conflict sem --into: %d", code)
	}
	code, out, errb := exec(t, Runtime{}, env, `{"format":"hanko.company.v1"}`, "import", "-", "--dry-run", "--into", "acme", "--on-conflict", "rename", "--resolve", "personas/dev=replace")
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, errb)
	}
	if h := f.last(); h.Query != "dry_run=true&into=acme&on_conflict=rename&resolve=personas%2Fdev%3Dreplace" {
		t.Errorf("query: %s", h.Query)
	}
	for _, want := range []string{"Prévia (nada foi gravado)", "renomear", "→ dev-2", "aviso: 1 persona(s) entram pausadas"} {
		if !strings.Contains(out, want) {
			t.Errorf("saída sem %q:\n%s", want, out)
		}
	}
}

func TestMemberJoinCmds(t *testing.T) {
	id := "0198f2a0-0000-7000-8000-0000000000aa"
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{
		"POST /api/v1/companies/acme/join-link":                         jsonResp(201, api.JoinLink{Enabled: true, Path: "/pedir-acesso/tok"}),
		"GET /api/v1/companies/acme/join-requests":                      jsonResp(200, list(api.JoinRequest{ID: id, Email: "c@x.com", Status: "pending"})),
		"POST /api/v1/companies/acme/join-requests/" + id + "/decision": jsonResp(200, api.JoinRequest{ID: id, Email: "c@x.com", Status: "approved", Role: "viewer"}),
	})
	env := map[string]string{"HANKO_URL": ts.URL, "HANKO_COMPANY": "acme"}
	code, out, _ := exec(t, Runtime{}, env, "", "member", "join-link", "new")
	if code != 0 || strings.TrimSpace(out) != ts.URL+"/pedir-acesso/tok" {
		t.Errorf("join-link new: %d %q", code, out)
	}
	if code, out, _ := exec(t, Runtime{}, env, "", "member", "requests"); code != 0 || !strings.Contains(out, "c@x.com") {
		t.Errorf("requests: %d %s", code, out)
	}
	if code, out, errb := exec(t, Runtime{}, env, "", "member", "approve", id[:8], "--role", "viewer"); code != 0 || !strings.Contains(out, "viewer") {
		t.Errorf("approve: %d %s %s", code, out, errb)
	}
	if h := f.last(); h.Body != `{"approve":true,"role":"viewer"}` {
		t.Errorf("corpo: %s", h.Body)
	}
}
