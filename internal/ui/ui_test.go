package ui

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/uitest"
)

var _ Backend = (*uitest.Fake)(nil)

type env struct {
	f      *uitest.Fake
	broker *events.Broker
	h      http.Handler
}

func newEnv(t *testing.T, f *uitest.Fake) *env {
	t.Helper()
	b := events.NewBroker()
	s := &server{be: f, broker: b, log: nil, version: "test", now: uitest.Now, sseTick: 50 * time.Millisecond}
	s.log = testLogger(t)
	mux := http.NewServeMux()
	s.routes(mux)
	return &env{f: f, broker: b, h: mux}
}

type req struct {
	method, path string
	form         url.Values
	hdr          map[string]string
}

func (e *env) do(t *testing.T, r req) *httptest.ResponseRecorder {
	t.Helper()
	var body io.Reader
	if r.form != nil {
		body = strings.NewReader(r.form.Encode())
	}
	rq := httptest.NewRequest(r.method, r.path, body)
	if r.form != nil {
		rq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range r.hdr {
		rq.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, rq)
	return rec
}

func (e *env) get(t *testing.T, path string, hdr ...string) *httptest.ResponseRecorder {
	h := map[string]string{}
	for i := 0; i+1 < len(hdr); i += 2 {
		h[hdr[i]] = hdr[i+1]
	}
	return e.do(t, req{method: "GET", path: path, hdr: h})
}

// post sends a same-origin form post (plain) or an htmx one.
func (e *env) post(t *testing.T, path string, form url.Values, htmx bool) *httptest.ResponseRecorder {
	h := map[string]string{"Sec-Fetch-Site": "same-origin"}
	if htmx {
		h["HX-Request"] = "true"
	}
	return e.do(t, req{method: "POST", path: path, form: form, hdr: h})
}

func wantStatus(t *testing.T, rec *httptest.ResponseRecorder, code int) {
	t.Helper()
	if rec.Code != code {
		t.Fatalf("status = %d, want %d\nbody: %.400s", rec.Code, code, rec.Body.String())
	}
}

func wantContains(t *testing.T, rec *httptest.ResponseRecorder, subs ...string) {
	t.Helper()
	body := rec.Body.String()
	for _, s := range subs {
		if !strings.Contains(body, s) {
			t.Errorf("body missing %q\nbody: %.600s", s, body)
		}
	}
}

func wantNotContains(t *testing.T, rec *httptest.ResponseRecorder, subs ...string) {
	t.Helper()
	body := rec.Body.String()
	for _, s := range subs {
		if strings.Contains(body, s) {
			t.Errorf("body must not contain %q", s)
		}
	}
}

func TestRoot(t *testing.T) {
	t.Run("sem empresa vai para o onboarding", func(t *testing.T) {
		e := newEnv(t, uitest.None())
		rec := e.get(t, "/")
		wantStatus(t, rec, http.StatusSeeOther)
		if loc := rec.Header().Get("Location"); loc != "/bem-vindo" {
			t.Errorf("Location = %q", loc)
		}
		rec = e.get(t, "/bem-vindo")
		wantStatus(t, rec, http.StatusOK)
		wantContains(t, rec, "Criar empresa", `action="/empresas"`)
	})
	t.Run("com empresa vai para o painel", func(t *testing.T) {
		e := newEnv(t, uitest.Sample())
		rec := e.get(t, "/")
		wantStatus(t, rec, http.StatusSeeOther)
		if loc := rec.Header().Get("Location"); loc != "/c/hanko/" {
			t.Errorf("Location = %q", loc)
		}
	})
	t.Run("raiz não captura caminhos desconhecidos", func(t *testing.T) {
		e := newEnv(t, uitest.Sample())
		wantStatus(t, e.get(t, "/nada"), http.StatusNotFound)
	})
}

// O nome amigável da execução aparece ao lado do id curto: lista, detalhe, task e Caixa de entrada.
func TestRunNameVisivel(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	r0, failed := e.f.Runs[0], e.f.Runs[4]
	for path, name := range map[string]string{
		"/c/hanko/execucoes":                   r0.Name,
		"/c/hanko/execucoes/" + r0.ID.String(): r0.Name,
		"/c/hanko/tasks/HNK-221":               r0.Name,
		"/c/hanko/caixa":                       failed.Name,
	} {
		if name == "" {
			t.Fatal("amostra sem nome de execução")
		}
		wantContains(t, e.get(t, path), name)
	}
	wantContains(t, e.get(t, "/c/hanko/execucoes/"+r0.ID.String()), shortID(r0.ID))
}

func TestPagesRender(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	run := e.f.Runs[0].ID.String()
	cases := []struct {
		path string
		want []string
	}{
		{"/c/hanko/", []string{"Painel", "Personas ativas", "Tasks por status", "Primeiros passos", "Contratar persona"}},
		{"/c/hanko/tasks", []string{"HNK-221", "Docslide: AI-first SDLC no banco", "Em andamento", "Bloqueadas", "Sem dono"}},
		{"/c/hanko/tasks?aba=backlog", []string{"HNK-224"}},
		{"/c/hanko/tasks?aba=concluidas", []string{"HNK-214"}},
		{"/c/hanko/tasks?aba=todas", []string{"HNK-214", "HNK-224", "HNK-221"}},
		{"/c/hanko/tasks/nova", []string{"Nova task", `name="title"`, "Prioridade"}},
		{"/c/hanko/tasks/HNK-221", []string{"Docslide: AI-first SDLC no banco", "<strong>5 slides</strong>", "Pode delegar", "Comentar", `id="task-side"`, "#7c1842"}},
		{"/c/hanko/personas", []string{"Ada", "Kenji", "Aguarda gate", "claude-opus"}},
		{"/c/hanko/personas/nova", []string{"Contratar persona", `name="agent"`, `value="codex"`}},
		{"/c/hanko/personas/ada", []string{"Chief of staff", "Instruções", "HNK-221"}},
		{"/c/hanko/execucoes", []string{"Execuções", "Hana · HNK-219", "ao vivo"}},
		{"/c/hanko/execucoes?aba=falhas", []string{"Sato · HNK-222", "Falhou"}},
		{"/c/hanko/execucoes/" + run, []string{`hx-ext="sse"`, `sse-connect="/c/hanko/execucoes/` + run + `/sse?after=12"`, `sse-swap="run-event"`, `sse-swap="run-status"`, "Interromper", "context.load"}},
		{"/c/hanko/atividade", []string{"Atividade", "Hoje", "HNK-221"}},
		{"/c/hanko/caixa", []string{"Caixa de entrada", "Aprovar o plano de migração do NAS 3D", "Tasks bloqueadas", "Execuções que falharam"}},
		{"/c/hanko/decisoes", []string{"Decisões", "Publicar o digest semanal", "Aprovado"}},
		{"/c/hanko/gates", []string{"Gates", "Pendentes", "Contratar Yuki (Dados)"}},
		{"/c/hanko/organograma", []string{"Organograma", `class="org-svg"`, "Ada", "Kenji"}},
		{"/c/hanko/em-breve/projetos", []string{"Projetos", "M3"}},
		{"/c/hanko/custos", []string{"Custos e orçamentos", "Gasto por dia", "Orçamento da empresa", "Orçamento por persona", "<svg"}},
		{"/c/hanko/fluxos", []string{"Fluxos", "Digest semanal de fontes", "Rodar agora", "Toda sexta às 07:00"}},
		{"/c/hanko/fluxos/novo", []string{"Novo fluxo", `name="cron"`, "Modelos de agenda"}},
		{"/c/hanko/fluxos/digest-semanal", []string{"Digest semanal de fontes", "Histórico de disparos", "HNK-214", "Salvar alterações"}},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			rec := e.get(t, c.path)
			wantStatus(t, rec, http.StatusOK)
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
				t.Errorf("Content-Type = %q", ct)
			}
			wantContains(t, rec, c.want...)
			wantContains(t, rec, "/static/hanko.css", "/static/vendor/htmx.min.js")
		})
	}
}

func TestEmptyCompanyPages(t *testing.T) {
	e := newEnv(t, uitest.Empty())
	for _, p := range []string{"/c/hanko/", "/c/hanko/tasks", "/c/hanko/personas", "/c/hanko/execucoes", "/c/hanko/atividade"} {
		rec := e.get(t, p)
		wantStatus(t, rec, http.StatusOK)
	}
	rec := e.get(t, "/c/hanko/")
	wantContains(t, rec, "Você ainda não tem personas.", "Nenhuma atividade ainda.", "Sem orçamento definido.")
}

func TestShellNavigation(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	rec := e.get(t, "/c/hanko/tasks")
	wantContains(t, rec,
		"Buscar", "Caixa de entrada", "Decisões", "Trabalho", "Projetos", "Fluxos", "Metas", "Artefatos", "Workspaces",
		"Organização", "Organograma", "Skills", "Integrações", "Gates", "Supervisão", "Custos",
		`href="/c/hanko/gates"`, `href="/c/hanko/tasks" aria-current`, `href="/c/hanko/personas"`, `href="/c/hanko/execucoes"`, `href="/c/hanko/configuracoes"`)
	// Configurações fica no menu lateral, marcada quando aberta.
	rec = e.get(t, "/c/hanko/configuracoes")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, `href="/c/hanko/configuracoes" aria-current`)
}

func TestNotFoundAndErrors(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	for _, p := range []string{"/c/nao-existe/", "/c/hanko/tasks/HNK-9999", "/c/hanko/personas/zzz", "/c/hanko/execucoes/not-a-uuid", "/c/hanko/em-breve/tasks", "/c/hanko/em-breve/inexistente"} {
		rec := e.get(t, p)
		wantStatus(t, rec, http.StatusNotFound)
		wantContains(t, rec, "Não encontrado")
	}
	cases := []struct {
		err  error
		code int
		text string
	}{
		{domain.Invalid("title", "obrigatório"), 422, "title: obrigatório"},
		{domain.ErrConflict, 409, "Conflito"},
		{domain.ErrActiveRun, 409, "execução ativa"},
		{domain.ErrNotAllowed, 403, "não permitida"},
		{service.ErrNotImplemented, 501, "Ainda não implementado"},
		{errors.New("boom secreto"), 500, "Algo deu errado"},
	}
	for _, c := range cases {
		e := newEnv(t, uitest.Sample())
		e.f.Err = map[string]error{"Dashboard": c.err}
		rec := e.get(t, "/c/hanko/")
		wantStatus(t, rec, c.code)
		wantContains(t, rec, c.text)
		wantNotContains(t, rec, "boom secreto")
	}
	t.Run("erro em fragmento htmx vira alerta retargetado", func(t *testing.T) {
		e := newEnv(t, uitest.Sample())
		e.f.Err = map[string]error{"AddComment": domain.ErrConflict}
		rec := e.post(t, "/c/hanko/tasks/HNK-221/comentarios", url.Values{"body": {"oi"}}, true)
		wantStatus(t, rec, http.StatusConflict)
		if rec.Header().Get("HX-Retarget") != "#flash" || rec.Header().Get("HX-Reswap") != "innerHTML" {
			t.Errorf("headers: %v", rec.Header())
		}
		wantContains(t, rec, `class="alert danger"`)
		wantNotContains(t, rec, "<html")
	})
}

func TestCSRF(t *testing.T) {
	form := url.Values{"body": {"oi"}}
	cases := []struct {
		name string
		hdr  map[string]string
		ok   bool
	}{
		{"sem cabeçalhos", nil, false},
		{"HX-Request", map[string]string{"HX-Request": "true"}, true},
		{"Sec-Fetch-Site same-origin", map[string]string{"Sec-Fetch-Site": "same-origin"}, true},
		{"Sec-Fetch-Site cross-site", map[string]string{"Sec-Fetch-Site": "cross-site"}, false},
		{"Sec-Fetch-Site same-site", map[string]string{"Sec-Fetch-Site": "same-site"}, false},
		{"Origin igual", map[string]string{"Origin": "http://example.com"}, true},
		{"Origin diferente", map[string]string{"Origin": "http://evil.example"}, false},
		{"Origin lixo", map[string]string{"Origin": "::::"}, false},
		{"cross-site mesmo com Origin igual", map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "http://example.com"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, uitest.Sample())
			rec := e.do(t, req{method: "POST", path: "/c/hanko/tasks/HNK-221/comentarios", form: form, hdr: c.hdr})
			if c.ok && rec.Code == http.StatusForbidden {
				t.Fatalf("esperava aceitar, veio 403")
			}
			if !c.ok {
				wantStatus(t, rec, http.StatusForbidden)
				if e.f.Called("AddComment") {
					t.Error("a escrita aconteceu apesar do bloqueio")
				}
			}
		})
	}
	t.Run("todos os POSTs são protegidos", func(t *testing.T) {
		e := newEnv(t, uitest.Sample())
		run := e.f.Runs[0].ID.String()
		for _, p := range []string{"/empresas", "/c/hanko/tasks/nova", "/c/hanko/tasks/HNK-221", "/c/hanko/tasks/HNK-221/comentarios", "/c/hanko/tasks/HNK-221/acordar", "/c/hanko/personas/nova", "/c/hanko/execucoes/" + run + "/cancelar",
			"/c/hanko/gates/" + uitest.ID("gate:plan").String() + "/decidir", "/c/hanko/personas/kenji/organizacao",
			"/c/hanko/caixa/tasks/" + uitest.ID("task:HNK-209").String() + "/desbloquear", "/c/hanko/caixa/tasks/" + uitest.ID("task:HNK-209").String() + "/acordar",
			"/c/hanko/caixa/personas/mika/reativar"} {
			rec := e.do(t, req{method: "POST", path: p, form: url.Values{"x": {"1"}}})
			if rec.Code != http.StatusForbidden {
				t.Errorf("POST %s = %d, want 403", p, rec.Code)
			}
		}
		if len(e.f.Calls) != 0 {
			t.Errorf("chamadas indevidas: %v", e.f.Calls)
		}
	})
}

func TestCreateCompany(t *testing.T) {
	e := newEnv(t, uitest.None())
	rec := e.post(t, "/empresas", url.Values{"name": {"Acme Corp"}, "prefix": {"acm"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/c/acme-corp/" {
		t.Errorf("Location = %q", loc)
	}
	if got := e.f.Companies[0].TaskPrefix; got != "ACM" {
		t.Errorf("prefix = %q", got)
	}
	rec = e.post(t, "/empresas", url.Values{"name": {"  "}}, false)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	wantContains(t, rec, "Dê um nome à empresa.", `<form`)
	rec = e.post(t, "/empresas", url.Values{"name": {"Acme Corp"}}, false)
	wantStatus(t, rec, http.StatusConflict)
	wantContains(t, rec, "Conflito", `value="Acme Corp"`)
	rec = e.post(t, "/empresas", url.Values{"name": {"Outra"}}, true)
	wantStatus(t, rec, http.StatusNoContent)
	if rec.Header().Get("HX-Redirect") != "/c/outra/" {
		t.Errorf("HX-Redirect = %q", rec.Header().Get("HX-Redirect"))
	}
}

func TestTasksListFilters(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	rec := e.get(t, "/c/hanko/tasks")
	wantNotContains(t, rec, "HNK-224", "HNK-214") // backlog e concluída fora de "Ativas"

	rec = e.get(t, "/c/hanko/tasks?q=nas")
	wantContains(t, rec, "HNK-218")
	wantNotContains(t, rec, "HNK-221")

	rec = e.get(t, "/c/hanko/tasks?q=zzzz")
	wantContains(t, rec, "Nada encontrado.")

	t.Run("fragmento htmx não traz o layout", func(t *testing.T) {
		rec := e.get(t, "/c/hanko/tasks?q=docslide", "HX-Request", "true")
		wantStatus(t, rec, http.StatusOK)
		wantContains(t, rec, `id="tasks-list"`, "HNK-221")
		wantNotContains(t, rec, "<html", "Caixa de entrada")
	})
	t.Run("boost devolve página inteira", func(t *testing.T) {
		rec := e.get(t, "/c/hanko/tasks", "HX-Request", "true", "HX-Boosted", "true")
		wantContains(t, rec, "<html")
	})
}

func TestTaskDetailXSS(t *testing.T) {
	f := uitest.Sample()
	tv := &f.Tasks[0]
	tv.Title = `<img src=x onerror=alert(1)>`
	tv.Description = "texto <script>alert('xss')</script> e [clique](javascript:alert(1))\n\n<iframe src=//evil></iframe>"
	f.Comments = append(f.Comments, domain.Comment{ID: domain.NewID(), TaskID: tv.ID, Author: domain.HumanActor, Body: `<script>alert("c")</script> **ok**`, CreatedAt: uitest.Now()})
	e := newEnv(t, f)
	rec := e.get(t, "/c/hanko/tasks/HNK-221")
	wantStatus(t, rec, http.StatusOK)
	// A ilha do editor ancorado recebe o texto bruto da descrição em data-doc (escapado no atributo, inerte);
	// o resto da página não pode ter nada executável.
	body := rec.Body.String()
	docAttr := regexp.MustCompile(`data-doc="[^"]*"`)
	attrs := docAttr.FindAllString(body, -1)
	if len(attrs) != 1 || strings.Contains(attrs[0], "<") || !strings.Contains(attrs[0], "&lt;script&gt;") {
		t.Errorf("data-doc deve existir uma vez e vir escapado: %q", attrs)
	}
	for _, bad := range []string{"<script>alert", "<iframe", "javascript:", "<img src=x", "onerror=alert(1)>"} {
		if strings.Contains(docAttr.ReplaceAllString(body, ""), bad) {
			t.Errorf("body must not contain %q", bad)
		}
	}
	wantContains(t, rec, "&lt;script", "<strong>ok</strong>")
	// o único <script> permitido é o dos arquivos estáticos
	for _, part := range strings.Split(body, "<script")[1:] {
		if !strings.HasPrefix(part, ` src="/static/`) {
			t.Errorf("<script inesperado: %.80s", part)
		}
	}
}

func TestComment(t *testing.T) {
	t.Run("htmx devolve a thread", func(t *testing.T) {
		e := newEnv(t, uitest.Sample())
		rec := e.post(t, "/c/hanko/tasks/HNK-221/comentarios", url.Values{"body": {"Novo **comentário** do dono"}}, true)
		wantStatus(t, rec, http.StatusOK)
		wantContains(t, rec, `id="thread"`, "<strong>comentário</strong>", "Pode delegar")
		wantNotContains(t, rec, "<html")
		if !e.f.Called("AddComment") {
			t.Error("AddComment não chamado")
		}
		last := e.f.Comments[len(e.f.Comments)-1]
		if last.Author.Type != domain.ActorHuman {
			t.Errorf("autor = %v, want human", last.Author)
		}
	})
	t.Run("form simples redireciona (PRG)", func(t *testing.T) {
		e := newEnv(t, uitest.Sample())
		rec := e.post(t, "/c/hanko/tasks/HNK-221/comentarios", url.Values{"body": {"oi"}}, false)
		wantStatus(t, rec, http.StatusSeeOther)
		if loc := rec.Header().Get("Location"); loc != "/c/hanko/tasks/HNK-221#thread" {
			t.Errorf("Location = %q", loc)
		}
	})
	t.Run("vazio é 422 e não escreve", func(t *testing.T) {
		e := newEnv(t, uitest.Sample())
		rec := e.post(t, "/c/hanko/tasks/HNK-221/comentarios", url.Values{"body": {"   "}}, true)
		wantStatus(t, rec, http.StatusUnprocessableEntity)
		if e.f.Called("AddComment") {
			t.Error("escreveu comentário vazio")
		}
	})
	t.Run("task inexistente", func(t *testing.T) {
		e := newEnv(t, uitest.Sample())
		wantStatus(t, e.post(t, "/c/hanko/tasks/HNK-9/comentarios", url.Values{"body": {"x"}}, false), http.StatusNotFound)
	})
}

func TestUpdateTask(t *testing.T) {
	t.Run("status e atribuição via htmx", func(t *testing.T) {
		e := newEnv(t, uitest.Sample())
		kenji := uitest.ID("persona:kenji").String()
		rec := e.post(t, "/c/hanko/tasks/HNK-217", url.Values{"status": {"in_review"}, "priority": {"high"}, "persona": {kenji}}, true)
		wantStatus(t, rec, http.StatusOK)
		wantContains(t, rec, `id="task-side"`, `id="task-chips"`, `hx-swap-oob="true"`, "Em revisão", "Kenji")
		wantNotContains(t, rec, "<html")
		got, _ := e.f.GetTask(context.Background(), domain.ID{}, "HNK-217")
		if got.Status != domain.TaskInReview || got.Priority != domain.PriorityHigh || got.PersonaName != "Kenji" {
			t.Errorf("task = %+v", got.Task)
		}
	})
	t.Run("sem dono", func(t *testing.T) {
		e := newEnv(t, uitest.Sample())
		rec := e.post(t, "/c/hanko/tasks/HNK-217", url.Values{"status": {"in_progress"}, "persona": {""}}, true)
		wantStatus(t, rec, http.StatusOK)
		got, _ := e.f.GetTask(context.Background(), domain.ID{}, "HNK-217")
		if got.PersonaID != nil {
			t.Error("persona não foi removida")
		}
	})
	t.Run("nada mudou não escreve", func(t *testing.T) {
		e := newEnv(t, uitest.Sample())
		rec := e.post(t, "/c/hanko/tasks/HNK-217", url.Values{"status": {"in_progress"}, "priority": {"medium"}}, true)
		wantStatus(t, rec, http.StatusOK)
		if e.f.Called("UpdateTask") {
			t.Error("UpdateTask chamado sem mudança")
		}
	})
	t.Run("valor inválido é 422", func(t *testing.T) {
		e := newEnv(t, uitest.Sample())
		for _, f := range []url.Values{{"status": {"voando"}}, {"priority": {"urgentissima"}}, {"persona": {"nao-uuid"}}} {
			wantStatus(t, e.post(t, "/c/hanko/tasks/HNK-217", f, true), http.StatusUnprocessableEntity)
		}
		if e.f.Called("UpdateTask") {
			t.Error("escreveu valor inválido")
		}
	})
	t.Run("form simples redireciona", func(t *testing.T) {
		e := newEnv(t, uitest.Sample())
		rec := e.post(t, "/c/hanko/tasks/HNK-217", url.Values{"status": {"blocked"}}, false)
		wantStatus(t, rec, http.StatusSeeOther)
		if loc := rec.Header().Get("Location"); loc != "/c/hanko/tasks/HNK-217" {
			t.Errorf("Location = %q", loc)
		}
	})
}

func TestWake(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	rec := e.post(t, "/c/hanko/tasks/HNK-217/acordar", nil, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/c/hanko/tasks/HNK-217?ok=acordada" {
		t.Errorf("Location = %q", loc)
	}
	rec = e.post(t, "/c/hanko/tasks/HNK-217/acordar", nil, true)
	wantStatus(t, rec, http.StatusNoContent)
	if rec.Header().Get("HX-Redirect") != "/c/hanko/tasks/HNK-217?ok=acordada" {
		t.Errorf("HX-Redirect = %q", rec.Header().Get("HX-Redirect"))
	}
	rec = e.get(t, "/c/hanko/tasks/HNK-217?ok=acordada")
	wantContains(t, rec, "Persona acordada")
	rec = e.post(t, "/c/hanko/tasks/HNK-211/acordar", nil, true) // sem dono
	wantStatus(t, rec, http.StatusUnprocessableEntity)
}

func TestCreateTask(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		e := newEnv(t, uitest.Sample())
		ada := uitest.ID("persona:ada").String()
		rec := e.post(t, "/c/hanko/tasks/nova", url.Values{"title": {" Nova ideia "}, "description": {"corpo"}, "persona": {ada}, "priority": {"high"}, "status": {"todo"}, "project": {uitest.ID("project:kubo").String()}}, false)
		wantStatus(t, rec, http.StatusSeeOther)
		if loc := rec.Header().Get("Location"); loc != "/c/hanko/tasks/HNK-225?ok=criada" {
			t.Errorf("Location = %q", loc)
		}
		got := e.f.Tasks[len(e.f.Tasks)-1]
		if got.Title != "Nova ideia" || got.Priority != domain.PriorityHigh || got.PersonaID == nil || got.ProjectID == nil || got.CreatedBy.Type != domain.ActorHuman {
			t.Errorf("task = %+v", got.Task)
		}
	})
	t.Run("sem título re-renderiza o form", func(t *testing.T) {
		e := newEnv(t, uitest.Sample())
		rec := e.post(t, "/c/hanko/tasks/nova", url.Values{"title": {""}, "description": {"mantém isto"}}, false)
		wantStatus(t, rec, http.StatusUnprocessableEntity)
		wantContains(t, rec, "Dê um título", "mantém isto", `<form`)
		if e.f.Called("CreateTask") {
			t.Error("criou task sem título")
		}
	})
	t.Run("erro do service volta ao form", func(t *testing.T) {
		e := newEnv(t, uitest.Sample())
		e.f.Err = map[string]error{"CreateTask": domain.Invalid("persona_id", "não pertence à empresa")}
		rec := e.post(t, "/c/hanko/tasks/nova", url.Values{"title": {"x"}}, false)
		wantStatus(t, rec, http.StatusUnprocessableEntity)
		wantContains(t, rec, "persona_id: não pertence à empresa")
	})
	t.Run("ids inválidos", func(t *testing.T) {
		e := newEnv(t, uitest.Sample())
		wantStatus(t, e.post(t, "/c/hanko/tasks/nova", url.Values{"title": {"x"}, "persona": {"lixo"}}, false), http.StatusUnprocessableEntity)
		wantStatus(t, e.post(t, "/c/hanko/tasks/nova", url.Values{"title": {"x"}, "project": {"lixo"}}, false), http.StatusUnprocessableEntity)
	})
	t.Run("persona pré-selecionada por query", func(t *testing.T) {
		e := newEnv(t, uitest.Sample())
		ada := uitest.ID("persona:ada").String()
		rec := e.get(t, "/c/hanko/tasks/nova?persona="+ada)
		wantContains(t, rec, `value="`+ada+`" selected`)
	})
}

func TestHirePersona(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		e := newEnv(t, uitest.Sample())
		rec := e.post(t, "/c/hanko/personas/nova", url.Values{"name": {"Yuki Tanaka"}, "title": {"Dados"}, "agent": {"codex"}, "model": {"gpt-5"}, "instructions": {"faça"}, "heartbeat": {"30"}}, false)
		wantStatus(t, rec, http.StatusSeeOther)
		if loc := rec.Header().Get("Location"); loc != "/c/hanko/personas/yuki-tanaka" {
			t.Errorf("Location = %q", loc)
		}
		p := e.f.Personas[len(e.f.Personas)-1]
		if p.Agent != "codex" || p.HeartbeatIntervalSec != 1800 || p.Model != "gpt-5" || p.Slug != "yuki-tanaka" {
			t.Errorf("persona = %+v", p)
		}
		wantStatus(t, e.get(t, "/c/hanko/personas/yuki-tanaka"), http.StatusOK)
	})
	t.Run("validações", func(t *testing.T) {
		bad := []url.Values{
			{"name": {""}, "agent": {"claude"}},
			{"name": {"X"}, "agent": {"gpt"}},
			{"name": {"X"}, "agent": {"claude"}, "heartbeat": {"abc"}},
			{"name": {"X"}, "agent": {"claude"}, "heartbeat": {"-5"}},
			{"name": {"X"}, "agent": {"claude"}, "heartbeat": {"99999"}},
		}
		for _, f := range bad {
			e := newEnv(t, uitest.Sample())
			rec := e.post(t, "/c/hanko/personas/nova", f, false)
			wantStatus(t, rec, http.StatusUnprocessableEntity)
			wantContains(t, rec, `<form`)
			if e.f.Called("HirePersona") {
				t.Errorf("contratou com %v", f)
			}
		}
	})
	t.Run("slug repetido é 409", func(t *testing.T) {
		e := newEnv(t, uitest.Sample())
		rec := e.post(t, "/c/hanko/personas/nova", url.Values{"name": {"Ada"}, "agent": {"fake"}}, false)
		wantStatus(t, rec, http.StatusConflict)
		wantContains(t, rec, "Conflito", `value="Ada"`)
	})
	t.Run("heartbeat 0 é manual", func(t *testing.T) {
		e := newEnv(t, uitest.Sample())
		e.post(t, "/c/hanko/personas/nova", url.Values{"name": {"Rin"}, "agent": {"fake"}, "heartbeat": {"0"}}, false)
		if e.f.Personas[len(e.f.Personas)-1].HeartbeatIntervalSec != 0 {
			t.Error("heartbeat deveria ser 0")
		}
	})
}

func TestCancelRun(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	run := e.f.Runs[0].ID.String()
	rec := e.post(t, "/c/hanko/execucoes/"+run+"/cancelar", nil, true)
	wantStatus(t, rec, http.StatusNoContent)
	rec = e.get(t, "/c/hanko/execucoes/"+run)
	wantContains(t, rec, "Cancelada")
	wantNotContains(t, rec, "sse-connect") // terminou: sem stream
	rec = e.post(t, "/c/hanko/execucoes/"+run+"/cancelar", nil, false)
	wantStatus(t, rec, http.StatusSeeOther)
	wantStatus(t, e.post(t, "/c/hanko/execucoes/lixo/cancelar", nil, false), http.StatusNotFound)
}

func TestStatic(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	for path, ct := range map[string]string{
		"/static/hanko.css":           "text/css",
		"/static/hanko.js":            "javascript",
		"/static/vendor/htmx.min.js":  "javascript",
		"/static/vendor/sse.min.js":   "javascript",
		"/static/favicon.svg":         "image/svg+xml",
		"/static/vendor/LICENSE-htmx": "",
	} {
		rec := e.get(t, path)
		wantStatus(t, rec, http.StatusOK)
		if !strings.Contains(rec.Header().Get("Content-Type"), ct) {
			t.Errorf("%s Content-Type = %q", path, rec.Header().Get("Content-Type"))
		}
		if rec.Body.Len() == 0 {
			t.Errorf("%s vazio", path)
		}
	}
	wantStatus(t, e.get(t, "/static/"), http.StatusNotFound)
	wantStatus(t, e.get(t, "/static/vendor/"), http.StatusNotFound)
	wantStatus(t, e.get(t, "/static/nao-existe.css"), http.StatusNotFound)
	if strings.Contains(e.get(t, "/static/hanko.css").Body.String(), "prefers-color-scheme: dark") == false {
		t.Error("css sem modo escuro")
	}
}

// ---------- SSE ----------

type sseReader struct {
	lines  chan string
	cancel context.CancelFunc
}

func openSSE(t *testing.T, e *env, path string, hdr map[string]string) *sseReader {
	t.Helper()
	srv := httptest.NewServer(e.h)
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	rq, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+path, nil)
	for k, v := range hdr {
		rq.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(rq)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if resp.StatusCode != 200 {
		t.Fatalf("SSE status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q", ct)
	}
	r := &sseReader{lines: make(chan string, 256), cancel: cancel}
	go func() {
		defer close(r.lines)
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			r.lines <- sc.Text()
		}
	}()
	return r
}

// next returns the next complete message as (event, id, data).
func (r *sseReader) next(t *testing.T) (event, id, data string) {
	t.Helper()
	timeout := time.After(5 * time.Second)
	var datas []string
	for {
		select {
		case ln, ok := <-r.lines:
			if !ok {
				t.Fatal("stream fechou")
			}
			switch {
			case ln == "":
				if event != "" {
					return event, id, strings.Join(datas, "\n")
				}
				datas = nil
			case strings.HasPrefix(ln, "event: "):
				event = strings.TrimPrefix(ln, "event: ")
			case strings.HasPrefix(ln, "id: "):
				id = strings.TrimPrefix(ln, "id: ")
			case strings.HasPrefix(ln, "data: "):
				datas = append(datas, strings.TrimPrefix(ln, "data: "))
			}
		case <-timeout:
			t.Fatal("timeout esperando evento SSE")
		}
	}
}

func TestRunSSE(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	run := e.f.Runs[0]
	path := "/c/hanko/execucoes/" + run.ID.String() + "/sse"
	total := len(e.f.Events[run.ID])

	t.Run("replay, ao vivo, status e fim", func(t *testing.T) {
		r := openSSE(t, e, path+"?after="+itoa(total-2), nil)

		// replay dos dois últimos eventos
		for i := total - 1; i <= total; i++ {
			ev, id, data := r.next(t)
			if ev != "run-event" || id != itoa(i) || !strings.Contains(data, `class="log-line`) {
				t.Fatalf("replay %d: event=%q id=%q data=%q", i, ev, id, data)
			}
		}
		// primeiro status
		if ev, _, data := r.next(t); ev != "run-status" || !strings.Contains(data, "Executando") {
			t.Fatalf("status inicial: %q %q", ev, data)
		}

		// evento ao vivo via broker (caminho rápido)
		le := e.f.AppendEvent(run.ID, domain.EventAssistant, "linha <b>nova</b> ao vivo")
		e.broker.Publish(events.Message{Topic: events.RunTopic(run.ID.String()), Type: "run.event", Data: le})
		ev, id, data := r.next(t)
		if ev != "run-event" || id != itoa(total+1) {
			t.Fatalf("ao vivo: %q %q", ev, id)
		}
		if !strings.Contains(data, "linha &lt;b&gt;nova&lt;/b&gt; ao vivo") || strings.Contains(data, "<b>nova") {
			t.Errorf("linha sem escape: %s", data)
		}
		if !strings.Contains(data, "k-assistant") {
			t.Errorf("sem classe do tipo: %s", data)
		}

		// payload desconhecido no broker: a UI relê o banco
		e.f.AppendEvent(run.ID, domain.EventStdout, "por polling")
		e.broker.Publish(events.Message{Topic: events.RunTopic(run.ID.String()), Type: "run.event", Data: "opaco"})
		if ev, id, data := r.next(t); ev != "run-event" || id != itoa(total+2) || !strings.Contains(data, "por polling") {
			t.Fatalf("fallback: %q %q %q", ev, id, data)
		}

		// término
		e.f.FinishRun(run.ID, domain.RunDone)
		e.broker.Publish(events.Message{Topic: events.RunTopic(run.ID.String()), Type: "run.status", Data: nil})
		var sawStatus bool
		for {
			ev, _, data := r.next(t)
			if ev == "run-status" && strings.Contains(data, "Concluída") {
				sawStatus = true
			}
			if ev == "run-end" {
				break
			}
		}
		if !sawStatus {
			t.Error("não recebeu run-status final antes de run-end")
		}
	})

	t.Run("Last-Event-ID tem precedência", func(t *testing.T) {
		e := newEnv(t, uitest.Sample())
		run := e.f.Runs[0]
		n := len(e.f.Events[run.ID])
		r := openSSE(t, e, "/c/hanko/execucoes/"+run.ID.String()+"/sse?after=0", map[string]string{"Last-Event-ID": itoa(n - 1)})
		ev, id, _ := r.next(t)
		if ev != "run-event" || id != itoa(n) {
			t.Fatalf("got %q %q, want só o último evento", ev, id)
		}
	})

	t.Run("run já terminado: replay, status e fim", func(t *testing.T) {
		e := newEnv(t, uitest.Sample())
		done := e.f.Runs[2] // concluída
		r := openSSE(t, e, "/c/hanko/execucoes/"+done.ID.String()+"/sse", nil)
		var got []string
		for {
			ev, _, _ := r.next(t)
			got = append(got, ev)
			if ev == "run-end" {
				break
			}
		}
		if got[len(got)-2] != "run-status" {
			t.Errorf("sequência = %v", got)
		}
	})

	t.Run("run inexistente", func(t *testing.T) {
		e := newEnv(t, uitest.Sample())
		wantStatus(t, e.get(t, "/c/hanko/execucoes/"+uitest.ID("nada").String()+"/sse"), http.StatusNotFound)
		wantStatus(t, e.get(t, "/c/hanko/execucoes/lixo/sse"), http.StatusNotFound)
	})

	t.Run("perder o acesso encerra o stream", func(t *testing.T) {
		for name, lost := range map[string]error{"papel insuficiente": domain.ErrForbidden, "sem vínculo": domain.ErrNotFound} {
			e := newEnv(t, uitest.Sample())
			run := e.f.Runs[0]
			r := openSSE(t, e, "/c/hanko/execucoes/"+run.ID.String()+"/sse?after=999", nil)
			if ev, _, _ := r.next(t); ev != "run-status" {
				t.Fatalf("%s: primeiro evento %q", name, ev)
			}
			e.f.SetErr("GetRun", lost) // o service passa a recusar: o papel caiu com o stream aberto
			deadline := time.After(3 * time.Second)
			for open := true; open; {
				select {
				case _, open = <-r.lines:
				case <-deadline:
					t.Fatalf("%s: o stream continuou aberto depois que o acesso caiu", name)
				}
			}
		}
	})

	t.Run("desconectar libera a assinatura", func(t *testing.T) {
		e := newEnv(t, uitest.Sample())
		run := e.f.Runs[0]
		r := openSSE(t, e, "/c/hanko/execucoes/"+run.ID.String()+"/sse?after=999", nil)
		r.next(t) // run-status inicial
		r.cancel()
		time.Sleep(150 * time.Millisecond)
		// publicar sem assinantes não pode bloquear nem falhar
		e.broker.Publish(events.Message{Topic: events.RunTopic(run.ID.String()), Type: "run.event"})
	})
}

func TestRunPageLiveWiring(t *testing.T) {
	e := newEnv(t, uitest.Sample())
	run := e.f.Runs[0].ID.String()
	rec := e.get(t, "/c/hanko/execucoes/"+run)
	wantContains(t, rec, `sse-close="run-end"`, `hx-swap="beforeend"`, `id="run-log"`, `/static/vendor/sse.min.js`)
	// run terminado não abre stream
	rec = e.get(t, "/c/hanko/execucoes/"+e.f.Runs[2].ID.String())
	wantNotContains(t, rec, "sse-connect")
	wantContains(t, rec, "Concluída", "Plano de migração reenviado")
}

func personaOf(f *uitest.Fake, slug string) *domain.Persona {
	for i := range f.Personas {
		if f.Personas[i].Slug == slug {
			return &f.Personas[i]
		}
	}
	return nil
}

func TestPersonaControls(t *testing.T) {
	t.Run("pausada oferece retomar", func(t *testing.T) {
		e := newEnv(t, uitest.Sample())
		wantContains(t, e.get(t, "/c/hanko/personas/sato"), `action="/c/hanko/personas/sato/retomar"`, "Retomar")
		rec := e.post(t, "/c/hanko/personas/sato/retomar", nil, false)
		wantStatus(t, rec, http.StatusSeeOther)
		if loc := rec.Header().Get("Location"); loc != "/c/hanko/personas/sato?ok=retomada" {
			t.Errorf("Location = %q", loc)
		}
		if st := personaOf(e.f, "sato").Status; st != domain.PersonaActive {
			t.Errorf("status = %s", st)
		}
		wantContains(t, e.get(t, "/c/hanko/personas/sato?ok=retomada"), "Persona retomada", `action="/c/hanko/personas/sato/pausar"`)
	})
	t.Run("pausar", func(t *testing.T) {
		e := newEnv(t, uitest.Sample())
		wantContains(t, e.get(t, "/c/hanko/personas/mika"), `action="/c/hanko/personas/mika/pausar"`, "Pausar")
		rec := e.post(t, "/c/hanko/personas/mika/pausar", nil, true)
		wantStatus(t, rec, http.StatusNoContent)
		if got := rec.Header().Get("HX-Redirect"); got != "/c/hanko/personas/mika?ok=pausada" {
			t.Errorf("HX-Redirect = %q", got)
		}
		if st := personaOf(e.f, "mika").Status; st != domain.PersonaPaused {
			t.Errorf("status = %s", st)
		}
		// already paused → conflict, nothing else called
		wantStatus(t, e.post(t, "/c/hanko/personas/mika/pausar", nil, false), http.StatusConflict)
	})
	t.Run("em erro: mostra a falha e reativa", func(t *testing.T) {
		f := uitest.Sample()
		mika := personaOf(f, "mika")
		mika.Status = domain.PersonaError
		at := uitest.Now()
		f.Health = map[domain.ID]service.PersonaHealth{mika.ID: {LastError: "limite de uso atingido", LastErrorAt: &at, ConsecutiveFailures: 3}}
		e := newEnv(t, f)
		rec := e.get(t, "/c/hanko/personas/mika")
		wantContains(t, rec, "Reativar", `action="/c/hanko/personas/mika/reativar"`, "limite de uso atingido", "3 falhas seguidas")
		wantNotContains(t, rec, `/mika/pausar`)

		rec = e.post(t, "/c/hanko/personas/mika/reativar", nil, false)
		wantStatus(t, rec, http.StatusSeeOther)
		if loc := rec.Header().Get("Location"); loc != "/c/hanko/personas/mika?ok=reativada" {
			t.Errorf("Location = %q", loc)
		}
		if st := mika.Status; st != domain.PersonaActive {
			t.Errorf("status = %s", st)
		}
		rec = e.get(t, "/c/hanko/personas/mika?ok=reativada")
		wantContains(t, rec, "Persona reativada")
		wantNotContains(t, rec, "limite de uso atingido")
	})
	t.Run("persona inexistente é 404 e GET não age", func(t *testing.T) {
		e := newEnv(t, uitest.Sample())
		wantStatus(t, e.post(t, "/c/hanko/personas/nada/reativar", nil, false), http.StatusNotFound)
		wantStatus(t, e.get(t, "/c/hanko/personas/sato/retomar"), http.StatusMethodNotAllowed)
		if e.f.Called("UpdatePersona") {
			t.Error("UpdatePersona chamado")
		}
	})
	t.Run("post de outra origem é recusado", func(t *testing.T) {
		e := newEnv(t, uitest.Sample())
		rec := e.do(t, req{method: "POST", path: "/c/hanko/personas/sato/retomar", hdr: map[string]string{"Sec-Fetch-Site": "cross-site"}})
		if rec.Code < 400 || e.f.Called("UpdatePersona") {
			t.Errorf("code=%d", rec.Code)
		}
	})
}

var tagRE = regexp.MustCompile(`(?s)<[^>]*>`)

// ADR-0016: the UI says "Harness" and "Intervalo de verificação", never "agente" or "heartbeat".
func TestVocabularyHasNoAgentOrHeartbeat(t *testing.T) {
	for name, f := range map[string]*uitest.Fake{"vazio": uitest.Empty(), "amostra": uitest.Sample()} {
		e := newEnv(t, f)
		for _, path := range []string{"/c/hanko/", "/c/hanko/personas", "/c/hanko/personas/nova", "/c/hanko/personas/ada", "/c/hanko/personas/kenji", "/c/hanko/tasks",
			"/c/hanko/execucoes", "/c/hanko/atividade", "/c/hanko/tasks/nova", "/c/hanko/caixa", "/c/hanko/decisoes", "/c/hanko/gates",
			"/c/hanko/gates?status=todos", "/c/hanko/organograma", "/c/hanko/tasks/HNK-218", "/c/hanko/gates/" + uitest.ID("gate:plan").String(),
			"/c/hanko/custos", "/c/hanko/fluxos", "/c/hanko/fluxos/novo", "/c/hanko/fluxos/digest-semanal"} {
			rec := e.get(t, path)
			if rec.Code == http.StatusNotFound {
				continue
			}
			text := strings.ToLower(tagRE.ReplaceAllString(rec.Body.String(), " "))
			for _, bad := range []string{"agente", "agent", "heartbeat"} {
				if strings.Contains(text, bad) {
					i := strings.Index(text, bad)
					t.Errorf("%s %s mostra %q: …%s…", name, path, bad, text[max(0, i-40):min(len(text), i+40)])
				}
			}
		}
	}
	e := newEnv(t, uitest.Sample())
	wantContains(t, e.get(t, "/c/hanko/personas/nova"), "Harness", "Acordar a cada")
	wantContains(t, e.get(t, "/c/hanko/personas/ada"), "Intervalo de verificação", "Harness")
	// the run page logs raw harness output, so only its labels are checked
	wantContains(t, e.get(t, "/c/hanko/execucoes/"+e.f.Runs[0].ID.String()), "<dt>Harness</dt>")
}
