//go:build integration

package ui_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/testdb"
	"github.com/renatobardi/hanko/internal/ui"
)

// authedUI monta a UI em modo authenticated com uma "sessão" simulada: o cabeçalho X-Test-User escolhe o usuário
// (faz o papel do auth.Guard).
type authedUI struct {
	*realUI
	users map[string]domain.User
}

func newAuthedUI(t *testing.T) *authedUI {
	t.Helper()
	pool := testdb.New(t)
	broker := events.NewBroker()
	svc := service.New(pool, noopJobs{}, broker)
	svc.ExternalIdentity = true
	mux := http.NewServeMux()
	ui.Mount(mux, ui.Deps{Svc: svc, Broker: broker, Logger: slog.New(slog.DiscardHandler), Version: "it", Auth: &ui.AuthDeps{Provider: "local"}})
	co, err := svc.CreateCompany(context.Background(), domain.HumanActor, service.CreateCompanyInput{Name: "Acme", Slug: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	a := &authedUI{users: map[string]domain.User{}}
	mk := func(label string, inst domain.UserRole, member domain.MemberRole) {
		id := domain.NewID()
		if _, err := pool.Exec(context.Background(), `INSERT INTO users (id, email, name, provider, password_hash, role) VALUES ($1,$2,$2,'local','x',$3)`,
			id, label+"@x.com", string(inst)); err != nil {
			t.Fatal(err)
		}
		if member != "" {
			if _, err := pool.Exec(context.Background(), `INSERT INTO company_members (company_id, user_id, role) VALUES ($1,$2,$3)`, co.ID, id, string(member)); err != nil {
				t.Fatal(err)
			}
		}
		u, err := svc.GetUserByEmail(context.Background(), label+"@x.com")
		if err != nil {
			t.Fatal(err)
		}
		a.users[label] = u
	}
	mk("owner", domain.RoleMember, domain.MemberOwner)
	mk("admin", domain.RoleMember, domain.MemberAdmin)
	mk("stranger", domain.RoleMember, "")
	mk("install", domain.RoleOwner, "")
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if who := r.Header.Get("X-Test-User"); who != "" {
			r = r.WithContext(auth.WithIdentity(r.Context(), auth.Identity{User: a.users[who], Via: auth.ViaSession}))
		}
		mux.ServeHTTP(w, r)
	})
	a.realUI = &realUI{t: t, svc: svc, co: co, h: h}
	return a
}

func (a *authedUI) as(who, method, path string, body *strings.Reader, ctype string) *httptest.ResponseRecorder {
	a.t.Helper()
	var rd *strings.Reader = strings.NewReader("")
	if body != nil {
		rd = body
	}
	rq := httptest.NewRequest(method, path, rd)
	rq.Header.Set("X-Test-User", who)
	if ctype != "" {
		rq.Header.Set("Content-Type", ctype)
	}
	if method == "POST" {
		rq.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	rec := httptest.NewRecorder()
	a.h.ServeHTTP(rec, rq)
	return rec
}

func (a *authedUI) getAs(who, path string) *httptest.ResponseRecorder {
	return a.as(who, "GET", path, nil, "")
}

func (a *authedUI) formAs(who, path string, v url.Values) *httptest.ResponseRecorder {
	return a.as(who, "POST", path, strings.NewReader(v.Encode()), "application/x-www-form-urlencoded")
}

func TestUICicloDeVidaDaEmpresa(t *testing.T) {
	u := newRealUI(t) // modo trusted: mesmo cartão, sem papéis
	const page = "/c/acme/configuracoes"

	if rec := u.get(page); rec.Code != 200 || !strings.Contains(rec.Body.String(), "Arquivar empresa") || strings.Contains(rec.Body.String(), "archived-banner") {
		t.Fatalf("página: %d", rec.Code)
	}
	// editar
	rec := u.form(page+"/empresa", url.Values{"name": {"Acme Brasil"}, "timezone": {"UTC"}, "description": {"time de produto"}, "prefix": {"AC"}})
	if rec.Code != 303 {
		t.Fatalf("editar: %d %s", rec.Code, rec.Body.String())
	}
	if b := u.get(page).Body.String(); !strings.Contains(b, "Acme Brasil") || !strings.Contains(b, "time de produto") {
		t.Error("edição não apareceu")
	}
	rec = u.form(page+"/empresa", url.Values{"timezone": {"Mars/Olympus"}})
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "timezone") {
		t.Errorf("fuso inválido: %d", rec.Code)
	}

	// arquivar: faixa em toda página, escrita recusada com mensagem clara
	if rec := u.form(page+"/empresa/arquivar", nil); rec.Code != 303 {
		t.Fatalf("arquivar: %d %s", rec.Code, rec.Body.String())
	}
	if b := u.get("/c/acme/tasks").Body.String(); !strings.Contains(b, "archived-banner") || !strings.Contains(b, "somente leitura") {
		t.Error("faixa de arquivada ausente")
	}
	rec = u.form("/c/acme/projetos/novo", url.Values{"name": {"P"}})
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "arquivada") {
		t.Errorf("escrita em arquivada: %d %.200s", rec.Code, rec.Body.String())
	}
	if b := u.get(page).Body.String(); !strings.Contains(b, "Restaurar empresa") || !strings.Contains(b, "Excluir empresa") {
		t.Error("cartão de arquivada sem restaurar/excluir")
	}

	// restaurar
	if rec := u.form(page+"/empresa/restaurar", nil); rec.Code != 303 {
		t.Fatalf("restaurar: %d", rec.Code)
	}
	if b := u.get("/c/acme/tasks").Body.String(); strings.Contains(b, "archived-banner") {
		t.Error("faixa deveria ter sumido")
	}

	// excluir: só arquivada e com o nome
	if rec := u.form(page+"/empresa/excluir", url.Values{"confirm": {"Acme Brasil"}}); rec.Code != 409 {
		t.Errorf("excluir ativa: %d", rec.Code)
	}
	u.form(page+"/empresa/arquivar", nil)
	if rec := u.form(page+"/empresa/excluir", url.Values{"confirm": {"errado"}}); rec.Code != 422 {
		t.Errorf("nome errado: %d", rec.Code)
	}
	if rec := u.form(page+"/empresa/excluir", url.Values{"confirm": {"Acme Brasil"}}); rec.Code != 303 || rec.Header().Get("Location") != "/" {
		t.Errorf("excluir: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if rec := u.get(page); rec.Code != 404 {
		t.Errorf("empresa excluída: %d", rec.Code)
	}
}

func TestUILoginDaCLIPelaTela(t *testing.T) {
	a := newAuthedUI(t)
	ctx := context.Background()
	st, err := a.svc.StartCLIAuth(ctx, "notebook da Ana", domain.KeyScopeRead)
	if err != nil {
		t.Fatal(err)
	}
	path := "/cli-auth/" + st.Code

	// sem sessão: vai para o login e volta
	if rec := a.get(path); rec.Code != 303 || !strings.HasPrefix(rec.Header().Get("Location"), "/entrar?next=") {
		t.Fatalf("sem sessão: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	rec := a.getAs("admin", path)
	b := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(b, "notebook da Ana") || !strings.Contains(b, "Só leitura") || !strings.Contains(b, "admin@x.com") {
		t.Fatalf("confirmação: %d %.300s", rec.Code, b)
	}
	if rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Error("sem Referrer-Policy")
	}
	if rec := a.getAs("admin", "/cli-auth/codigo-que-nao-existe"); rec.Code != 404 {
		t.Errorf("código desconhecido: %d", rec.Code)
	}
	rec = a.formAs("admin", path, url.Values{"decision": {"approve"}})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "foi autorizado") {
		t.Fatalf("autorizar: %d %.300s", rec.Code, rec.Body.String())
	}
	p, err := a.svc.PollCLIAuth(ctx, st.PollToken)
	if err != nil || p.APIKey == "" || p.Key.Scope != domain.KeyScopeRead {
		t.Fatalf("poll: %+v %v", p, err)
	}
	// agora a chave aparece na tela de chaves, com dispositivo e escopo
	if b := a.getAs("admin", "/c/acme/configuracoes").Body.String(); !strings.Contains(b, "notebook da Ana") || !strings.Contains(b, "só leitura") {
		t.Error("tela de chaves sem dispositivo/escopo")
	}
	// de novo: pedido já decidido
	if rec := a.formAs("admin", path, url.Values{"decision": {"deny"}}); rec.Code != 404 {
		t.Errorf("decidir de novo: %d", rec.Code)
	}
}

func TestUIPedidoDeEntradaEUsuarios(t *testing.T) {
	a := newAuthedUI(t)
	ctx := context.Background()
	members := "/c/acme/configuracoes/membros/entrada"

	rec := a.formAs("admin", members+"/link", nil)
	if rec.Code != 201 || !strings.Contains(rec.Body.String(), "/pedir-acesso/") {
		t.Fatalf("gerar link: %d %.200s", rec.Code, rec.Body.String())
	}
	b := rec.Body.String()
	i := strings.Index(b, "/pedir-acesso/")
	tok := b[i+len("/pedir-acesso/"):]
	tok = tok[:strings.IndexAny(tok, "<\" ")]

	// sem sessão -> login; com sessão -> formulário
	if rec := a.get("/pedir-acesso/" + tok); rec.Code != 303 {
		t.Fatalf("sem sessão: %d", rec.Code)
	}
	rec = a.getAs("stranger", "/pedir-acesso/"+tok)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Pedir acesso a Acme") || rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("página: %d %.300s", rec.Code, rec.Body.String())
	}
	if rec := a.getAs("stranger", "/pedir-acesso/token-errado"); rec.Code != 404 {
		t.Errorf("token errado: %d", rec.Code)
	}
	rec = a.formAs("stranger", "/pedir-acesso/"+tok, url.Values{"message": {"oi, sou do time"}})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Pedido enviado") {
		t.Fatalf("enviar: %d %.300s", rec.Code, rec.Body.String())
	}
	if rec := a.formAs("stranger", "/pedir-acesso/"+tok, nil); rec.Code != 409 {
		t.Errorf("pedido repetido: %d", rec.Code)
	}

	// o admin vê o pedido e aprova como viewer
	b = a.getAs("admin", "/c/acme/configuracoes").Body.String()
	if !strings.Contains(b, "stranger@x.com") || !strings.Contains(b, "oi, sou do time") {
		t.Fatalf("fila: %.400s", b)
	}
	rs, err := a.svc.ListJoinRequests(auth.WithIdentity(ctx, auth.Identity{User: a.users["admin"], Via: auth.ViaSession}), a.co.ID, true)
	if err != nil || len(rs) != 1 {
		t.Fatalf("pedidos: %v %v", rs, err)
	}
	rec = a.formAs("admin", members+"/"+rs[0].ID.String()+"/decidir", url.Values{"decision": {"approve"}, "role": {"viewer"}})
	if rec.Code != 303 {
		t.Fatalf("aprovar: %d %.300s", rec.Code, rec.Body.String())
	}
	if rec := a.getAs("stranger", "/c/acme/tasks"); rec.Code != 200 {
		t.Errorf("aprovado deveria entrar: %d", rec.Code)
	}
	if rec := a.formAs("admin", members+"/link/desligar", nil); rec.Code != 303 {
		t.Errorf("desligar: %d", rec.Code)
	}
	if rec := a.getAs("stranger", "/pedir-acesso/"+tok); rec.Code != 404 {
		t.Errorf("link desligado: %d", rec.Code)
	}

	// usuários da instalação: dono/admin da instalação; membro comum, não
	if rec := a.getAs("install", "/admin/usuarios"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "stranger@x.com") {
		t.Errorf("usuários: %d", rec.Code)
	}
	if rec := a.getAs("stranger", "/admin/usuarios"); rec.Code != 403 {
		t.Errorf("usuários como membro: %d", rec.Code)
	}
}

func multipartFile(t *testing.T, fields map[string]string, name string, content []byte) (*strings.Reader, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = w.WriteField(k, v)
	}
	fw, err := w.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write(content)
	_ = w.Close()
	return strings.NewReader(buf.String()), w.FormDataContentType()
}

func TestUIImportarComPrevia(t *testing.T) {
	a := newAuthedUI(t)
	ctx := context.Background()
	if _, err := a.svc.HirePersona(ctx, domain.HumanActor, a.co.ID, service.HirePersonaInput{Name: "Dev", Slug: "dev", Agent: "fake", Model: "m"}); err != nil {
		t.Fatal(err)
	}
	doc, err := a.svc.ExportCompany(ctx, a.co.ID, service.ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(doc)

	if rec := a.getAs("install", "/importar"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "Ver prévia") {
		t.Fatalf("página: %d", rec.Code)
	}
	// arquivo inválido
	body, ct := multipartFile(t, nil, "x.json", []byte("não é json"))
	if rec := a.as("install", "POST", "/importar", body, ct); rec.Code != 422 {
		t.Errorf("json inválido: %d", rec.Code)
	}
	// prévia para o merge na própria empresa: o dev já existe
	body, ct = multipartFile(t, map[string]string{"into": "acme", "on_conflict": "skip"}, "x.json", raw)
	rec := a.as("install", "POST", "/importar", body, ct)
	b := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(b, "Prévia: nada foi gravado ainda.") || !strings.Contains(b, "Confirmar importação") || !strings.Contains(b, `name="r:personas/dev"`) {
		t.Fatalf("prévia: %d %.500s", rec.Code, b)
	}
	i := strings.Index(b, `name="token" value="`)
	tok := b[i+len(`name="token" value="`):]
	tok = tok[:strings.Index(tok, `"`)]
	if ps, _ := a.svc.ListPersonas(ctx, a.co.ID); len(ps) != 1 {
		t.Fatalf("prévia gravou: %d", len(ps))
	}
	// trocar a ação do item para renomear e atualizar a prévia
	rec = a.formAs("install", "/importar/aplicar", url.Values{"token": {tok}, "into": {"acme"}, "on_conflict": {"skip"}, "r:personas/dev": {"rename"}, "intent": {"preview"}})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "dev-2") {
		t.Fatalf("atualizar prévia: %d %.300s", rec.Code, rec.Body.String())
	}
	// token de outro usuário não vale
	if rec := a.formAs("admin", "/importar/aplicar", url.Values{"token": {tok}, "intent": {"confirm"}}); rec.Code != 410 {
		t.Errorf("token alheio: %d", rec.Code)
	}
	// confirmar
	rec = a.formAs("install", "/importar/aplicar", url.Values{"token": {tok}, "into": {"acme"}, "on_conflict": {"skip"}, "r:personas/dev": {"rename"}, "intent": {"confirm"}})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Importação concluída") {
		t.Fatalf("confirmar: %d %.300s", rec.Code, rec.Body.String())
	}
	if ps, _ := a.svc.ListPersonas(ctx, a.co.ID); len(ps) != 2 {
		t.Errorf("personas após merge: %d", len(ps))
	}
	// o token foi consumido
	if rec := a.formAs("install", "/importar/aplicar", url.Values{"token": {tok}, "intent": {"confirm"}}); rec.Code != 410 {
		t.Errorf("token reutilizado: %d", rec.Code)
	}
	// empresa nova pela tela
	body, ct = multipartFile(t, map[string]string{"slug": "copia"}, "x.json", raw)
	rec = a.as("install", "POST", "/importar", body, ct)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "copia") {
		t.Fatalf("prévia nova: %d", rec.Code)
	}
}
