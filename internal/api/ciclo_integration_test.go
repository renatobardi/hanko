//go:build integration

package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// anon chama a API sem credencial (os endpoints públicos do login da CLI).
func (e *authEnv) anon(t *testing.T, method, path, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, e.url+"/api/v1"+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestAPICicloDeVidaDaEmpresa(t *testing.T) {
	e := setupAuth(t)
	c := "/companies/" + e.co.Slug
	want := func(who, method, path, body string, status int) string {
		t.Helper()
		got, resp := e.call(t, who, method, path, body)
		if got != status {
			t.Errorf("%s %s %s como %s: %d, esperado %d: %s", method, path, body, who, got, status, resp)
		}
		return resp
	}
	// editar: admin+
	want("member", "PATCH", c, `{"name":"Novo"}`, 403)
	body := want("admin", "PATCH", c, `{"name":"Acme Brasil","description":"time de produto","timezone":"UTC"}`, 200)
	if !strings.Contains(body, `"description":"time de produto"`) || !strings.Contains(body, `"name":"Acme Brasil"`) {
		t.Errorf("patch: %s", body)
	}
	want("admin", "PATCH", c, `{"timezone":"Mars/Olympus"}`, 422)
	want("stranger", "PATCH", c, `{"name":"x"}`, 404)

	// prefixo: trava com tasks
	want("admin", "PATCH", c, `{"task_prefix":"AC"}`, 200)
	want("member", "POST", c+"/tasks", `{"title":"x"}`, 201)
	want("admin", "PATCH", c, `{"task_prefix":"ZZ"}`, 409)

	// excluir exige estar arquivada, e é do owner
	want("owner", "DELETE", c, `{"confirm":"Acme Brasil"}`, 409)
	want("admin", "POST", c+"/archive", "", 200)
	want("admin", "POST", c+"/archive", "", 409)
	// somente leitura
	want("member", "POST", c+"/tasks", `{"title":"y"}`, 409)
	want("admin", "POST", c+"/subscriptions", `{"name":"S","agent":"claude","monthly_usd":1}`, 409)
	want("viewer", "GET", c+"/tasks", "", 200)
	if _, b := e.call(t, "install", "GET", "/companies?archived=true", ""); !strings.Contains(b, `"archived_at"`) {
		t.Errorf("lista arquivadas: %s", b)
	}
	if _, b := e.call(t, "install", "GET", "/companies?archived=false", ""); strings.Contains(b, e.co.Slug) {
		t.Errorf("lista sem arquivadas: %s", b)
	}

	// restaurar e arquivar de novo
	want("admin", "POST", c+"/restore", `{}`, 200)
	want("member", "POST", c+"/tasks", `{"title":"z"}`, 201)
	want("admin", "POST", c+"/archive", "", 200)

	// excluir: admin não; owner com o nome errado não; owner com o nome certo sim
	want("admin", "DELETE", c, `{"confirm":"Acme Brasil"}`, 403)
	want("owner", "DELETE", c, `{"confirm":"acme"}`, 422)
	del := want("owner", "DELETE", c, `{"confirm":"Acme Brasil"}`, 200)
	var d struct {
		ID         string `json:"id"`
		ExportPath string `json:"export_path"`
	}
	if err := json.Unmarshal([]byte(del), &d); err != nil || d.ID == "" {
		t.Fatalf("delete: %s %v", del, err)
	}
	want("owner", "GET", c, "", 404)
	// o export guardado: só o dono da instalação (ou quem apagou)
	want("install", "GET", "/company-deletions", "", 200)
	if code, exp := e.call(t, "install", "GET", "/company-deletions/"+d.ID+"/export", ""); code != 200 || !strings.Contains(exp, `"format":"hanko.company.v1"`) {
		t.Errorf("export: %d %.200s", code, exp)
	}
	want("owner", "GET", "/company-deletions/"+d.ID+"/export", "", 200) // quem apagou
	want("member", "GET", "/company-deletions/"+d.ID+"/export", "", 404)
	want("member", "GET", "/company-deletions", "", 403)
}

func TestAPIChaveSoDeLeitura(t *testing.T) {
	e := setupAuth(t)
	ctx := context.Background()
	var uid domain.ID
	if err := e.pool.QueryRow(ctx, `SELECT id FROM users WHERE email='member@x.com'`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	key, _, err := e.svc.CreateAPIKeyScoped(ctx, uid, "leitura", domain.KeyScopeRead, "ci")
	if err != nil {
		t.Fatal(err)
	}
	e.keys["ro"] = key
	c := "/companies/" + e.co.Slug
	if code, b := e.call(t, "ro", "GET", c+"/tasks", ""); code != 200 {
		t.Errorf("leitura: %d %s", code, b)
	}
	if code, _ := e.call(t, "ro", "POST", c+"/tasks", `{"title":"x"}`); code != 401 {
		t.Errorf("escrita com chave de leitura: %d", code)
	}
	code, b := e.call(t, "ro", "GET", "/whoami", "")
	if code != 200 || !strings.Contains(b, `"via":"api_key"`) || !strings.Contains(b, `"scope":"read"`) || !strings.Contains(b, `"device":"ci"`) {
		t.Errorf("whoami: %d %s", code, b)
	}
	if code, b := e.call(t, "owner", "GET", "/whoami", ""); code != 200 || !strings.Contains(b, `"scope":"full"`) || !strings.Contains(b, `"email":"owner@x.com"`) {
		t.Errorf("whoami owner: %d %s", code, b)
	}
}

func TestAPILoginDaCLI(t *testing.T) {
	e := setupAuth(t)
	ctx := context.Background()
	code, b := e.anon(t, "POST", "/cli-auth", `{"device":"notebook","scope":"full"}`)
	if code != 201 {
		t.Fatalf("start: %d %s", code, b)
	}
	var st struct {
		Code, PollToken, VerifyPath string
	}
	var raw map[string]any
	_ = json.Unmarshal([]byte(b), &raw)
	st.Code, _ = raw["code"].(string)
	st.PollToken, _ = raw["poll_token"].(string)
	st.VerifyPath, _ = raw["verify_path"].(string)
	if st.Code == "" || st.PollToken == "" || st.VerifyPath != "/cli-auth/"+st.Code {
		t.Fatalf("start: %v", raw)
	}
	poll := func() (int, map[string]any) {
		c, body := e.anon(t, "POST", "/cli-auth/poll", `{"poll_token":"`+st.PollToken+`"}`)
		var m map[string]any
		_ = json.Unmarshal([]byte(body), &m)
		return c, m
	}
	if c, m := poll(); c != 200 || m["status"] != "pending" {
		t.Fatalf("poll pendente: %d %v", c, m)
	}
	// a pessoa autoriza no navegador (aqui, direto no service: a página tem teste próprio na UI)
	var uid domain.ID
	if err := e.pool.QueryRow(ctx, `SELECT id FROM users WHERE email='member@x.com'`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.DecideCLIAuth(ctx, uid, st.Code, true); err != nil {
		t.Fatal(err)
	}
	c, m := poll()
	key, _ := m["api_key"].(string)
	if c != 200 || m["status"] != "approved" || !strings.HasPrefix(key, "hk_") {
		t.Fatalf("poll aprovado: %d %v", c, m)
	}
	// o cliente fake usa a chave recebida
	e.keys["cli"] = key
	if code, b := e.call(t, "cli", "GET", "/whoami", ""); code != 200 || !strings.Contains(b, `"device":"notebook"`) || !strings.Contains(b, `"email":"member@x.com"`) {
		t.Errorf("whoami com a chave do login: %d %s", code, b)
	}
	if code, _ := e.call(t, "cli", "POST", "/companies/"+e.co.Slug+"/tasks", `{"title":"da CLI"}`); code != 201 {
		t.Errorf("chave full escreve: %d", code)
	}
	if c, m := poll(); c != 200 || m["status"] != "consumed" || m["api_key"] != nil {
		t.Errorf("segunda consulta: %d %v", c, m)
	}
	// os endpoints são públicos mas validam entrada
	if code, _ := e.anon(t, "POST", "/cli-auth", `{"device":""}`); code != 422 {
		t.Errorf("device vazio: %d", code)
	}
	if code, _ := e.anon(t, "POST", "/cli-auth/poll", `{"poll_token":"nao-existe"}`); code != 404 {
		t.Errorf("poll desconhecido: %d", code)
	}
	// revogar a chave (a UI faz o mesmo) derruba o acesso
	keys, _ := e.svc.ListAPIKeys(ctx, uid)
	for _, k := range keys {
		if k.Device == "notebook" {
			if _, err := e.svc.RevokeAPIKey(ctx, uid, k.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if code, _ := e.call(t, "cli", "GET", "/whoami", ""); code != 401 {
		t.Errorf("chave revogada: %d", code)
	}
}

func TestAPIPedidosDeEntradaEUsuarios(t *testing.T) {
	e := setupAuth(t)
	c := "/companies/" + e.co.Slug
	want := func(who, method, path, body string, status int) string {
		t.Helper()
		got, resp := e.call(t, who, method, path, body)
		if got != status {
			t.Errorf("%s %s %s como %s: %d, esperado %d: %s", method, path, body, who, got, status, resp)
		}
		return resp
	}
	want("member", "POST", c+"/join-link", "", 403)
	b := want("admin", "POST", c+"/join-link", "", 201)
	var jl struct{ Token, Path string }
	_ = json.Unmarshal([]byte(b), &jl)
	if jl.Token == "" || jl.Path != "/pedir-acesso/"+jl.Token {
		t.Fatalf("link: %s", b)
	}
	if b := want("admin", "GET", c+"/join-link", "", 200); strings.Contains(b, jl.Token) {
		t.Errorf("o estado do link não pode devolver o token: %s", b)
	}
	// "stranger" pede entrada pelo serviço (a página pública tem teste na UI)
	var sid domain.ID
	if err := e.pool.QueryRow(context.Background(), `SELECT id FROM users WHERE email='stranger@x.com'`).Scan(&sid); err != nil {
		t.Fatal(err)
	}
	req, err := e.svc.RequestJoin(context.Background(), sid, jl.Token, "oi")
	if err != nil {
		t.Fatal(err)
	}
	want("member", "GET", c+"/join-requests", "", 403)
	if b := want("admin", "GET", c+"/join-requests?pending=true", "", 200); !strings.Contains(b, "stranger@x.com") {
		t.Errorf("lista: %s", b)
	}
	want("admin", "POST", c+"/join-requests/"+req.ID.String()+"/decision", `{"approve":true,"role":"admin"}`, 403)
	b = want("admin", "POST", c+"/join-requests/"+req.ID.String()+"/decision", `{"approve":true,"role":"viewer"}`, 200)
	if !strings.Contains(b, `"status":"approved"`) || !strings.Contains(b, `"role":"viewer"`) {
		t.Errorf("decisão: %s", b)
	}
	want("stranger", "GET", c+"/tasks", "", 200) // agora é membro (viewer)
	want("admin", "POST", c+"/join-requests/"+req.ID.String()+"/decision", `{"approve":false}`, 409)
	want("admin", "DELETE", c+"/join-link", "", 204)
	want("admin", "DELETE", c+"/join-link", "", 404)

	// contas da instalação
	want("member", "GET", "/users", "", 403)
	if b := want("install", "GET", "/users", "", 200); !strings.Contains(b, "stranger@x.com") || !strings.Contains(b, `"active_keys"`) {
		t.Errorf("users: %s", b)
	}
	_ = service.CLIAuthTTL
}

func TestAPIImportPreviaEMerge(t *testing.T) {
	e := setupAuth(t)
	ctx := context.Background()
	c := "/companies/" + e.co.Slug
	if _, err := e.svc.HirePersona(ctx, domain.HumanActor, e.co.ID, service.HirePersonaInput{Name: "Dev", Slug: "dev", Agent: "fake", Model: "m"}); err != nil {
		t.Fatal(err)
	}
	_, exp := e.call(t, "owner", "GET", c+"/export", "")
	post := func(who, query, body string) (int, string) {
		return e.call(t, who, "POST", "/companies/import"+query, body)
	}
	// prévia para empresa nova
	code, b := post("install", "?dry_run=true&slug=copia", exp)
	if code != 200 || !strings.Contains(b, `"dry_run":true`) || !strings.Contains(b, `"action":"create"`) {
		t.Fatalf("prévia: %d %s", code, b)
	}
	if code, b := e.call(t, "install", "GET", "/companies/copia", ""); code != 404 {
		t.Errorf("dry-run não cria: %d %s", code, b)
	}
	// merge: a persona já existe -> pular por padrão, renomear por exceção
	code, b = post("admin", "?into="+e.co.Slug+"&dry_run=true", exp)
	if code != 200 || !strings.Contains(b, `"action":"skip"`) || !strings.Contains(b, `"merged":true`) {
		t.Fatalf("prévia merge: %d %s", code, b)
	}
	code, b = post("admin", "?into="+e.co.Slug+"&resolve=personas/dev%3Drename", exp)
	if code != 200 || !strings.Contains(b, `"target":"dev-2"`) {
		t.Fatalf("merge: %d %s", code, b)
	}
	if _, b := e.call(t, "owner", "GET", c+"/personas", ""); !strings.Contains(b, `"dev-2"`) {
		t.Errorf("persona renomeada: %s", b)
	}
	// permissões e entradas inválidas
	if code, _ := post("member", "?into="+e.co.Slug, exp); code != 403 {
		t.Errorf("member não importa em empresa: %d", code)
	}
	if code, _ := post("stranger", "?into="+e.co.Slug, exp); code != 404 {
		t.Errorf("estranho: %d", code)
	}
	if code, _ := post("admin", "?into="+e.co.Slug+"&on_conflict=mesclar", exp); code != 422 {
		t.Errorf("on_conflict inválido: %d", code)
	}
	if code, _ := post("admin", "?into="+e.co.Slug+"&resolve=personas/fantasma%3Dskip", exp); code != 422 {
		t.Errorf("resolve para chave que não existe: %d", code)
	}
	if code, _ := post("admin", "?into="+e.co.Slug+"&resolve=lixo", exp); code != 422 {
		t.Errorf("resolve malformado: %d", code)
	}
}
