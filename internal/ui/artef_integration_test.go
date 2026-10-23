//go:build integration

package ui_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/testdb"
	"github.com/renatobardi/hanko/internal/ui"
)

// Artefatos e metas na UI contra o service e o banco reais (ADR-0028): o envio multipart, a entrega segura do
// conteúdo, o diff, as metas e a Caixa de entrada.

type noopJobs struct{}

func (noopJobs) WakeTx(context.Context, pgx.Tx, service.Wake) error                { return nil }
func (noopJobs) ExecuteRunTx(context.Context, pgx.Tx, domain.ID, domain.ID) error  { return nil }
func (noopJobs) StartReviewTx(context.Context, pgx.Tx, domain.ID, domain.ID) error { return nil }
func (noopJobs) DeliverMemoryTx(context.Context, pgx.Tx, domain.ID) error          { return nil }

type realUI struct {
	t   *testing.T
	svc *service.Service
	co  domain.Company
	h   http.Handler
}

func newRealUI(t *testing.T) *realUI {
	t.Helper()
	pool := testdb.New(t)
	broker := events.NewBroker()
	svc := service.New(pool, noopJobs{}, broker)
	mux := http.NewServeMux()
	ui.Mount(mux, ui.Deps{Svc: svc, Broker: broker, Logger: slog.New(slog.DiscardHandler), Version: "it"})
	co, err := svc.CreateCompany(context.Background(), domain.HumanActor, service.CreateCompanyInput{Name: "Acme", Slug: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	return &realUI{t: t, svc: svc, co: co, h: mux}
}

func (u *realUI) do(method, path string, body io.Reader, ctype string) *httptest.ResponseRecorder {
	u.t.Helper()
	rq := httptest.NewRequest(method, path, body)
	if ctype != "" {
		rq.Header.Set("Content-Type", ctype)
	}
	if method == "POST" {
		rq.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	rec := httptest.NewRecorder()
	u.h.ServeHTTP(rec, rq)
	return rec
}

func (u *realUI) get(path string) *httptest.ResponseRecorder { return u.do("GET", path, nil, "") }

func (u *realUI) form(path string, v url.Values) *httptest.ResponseRecorder {
	return u.do("POST", path, strings.NewReader(v.Encode()), "application/x-www-form-urlencoded")
}

func (u *realUI) upload(path string, fields map[string]string, filename string, content []byte) *httptest.ResponseRecorder {
	u.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			u.t.Fatal(err)
		}
	}
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		u.t.Fatal(err)
	}
	if _, err := fw.Write(content); err != nil {
		u.t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		u.t.Fatal(err)
	}
	return u.do("POST", path, &buf, mw.FormDataContentType())
}

func mustContain(t *testing.T, rec *httptest.ResponseRecorder, want int, subs ...string) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, quer %d\n%.500s", rec.Code, want, rec.Body.String())
	}
	for _, s := range subs {
		if !strings.Contains(rec.Body.String(), s) {
			t.Errorf("corpo sem %q", s)
		}
	}
}

func TestRealArtifactUploadServeAndDiff(t *testing.T) {
	u := newRealUI(t)
	ctx := context.Background()
	tk, err := u.svc.CreateTask(ctx, domain.HumanActor, u.co.ID, service.CreateTaskInput{Title: "Relatório", Status: domain.TaskTodo, Priority: domain.PriorityMedium})
	if err != nil {
		t.Fatal(err)
	}

	// v1 e v2 pela tela, na mesma task: a segunda cria a versão seguinte
	rec := u.upload("/c/acme/artefatos/novo", map[string]string{"task": tk.Key}, "plano.md", []byte("# um\n\nlinha igual\n"))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("upload v1: %d %.300s", rec.Code, rec.Body.String())
	}
	rec = u.upload("/c/acme/artefatos/novo", map[string]string{"task": tk.Key}, "plano.md", []byte("# dois\n\nlinha igual\nlinha nova\n"))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("upload v2: %d %.300s", rec.Code, rec.Body.String())
	}
	v2 := strings.TrimSuffix(strings.TrimPrefix(rec.Header().Get("Location"), "/c/acme/artefatos/"), "?ok=enviado")
	list, err := u.svc.ListArtifacts(ctx, u.co.ID, service.ArtifactFilter{AllVersions: true})
	if err != nil || len(list) != 2 {
		t.Fatalf("artefatos = %d, %v", len(list), err)
	}
	var v1 string
	for _, a := range list {
		if a.Version == 1 {
			v1 = a.ID.String()
		}
	}

	mustContain(t, u.get("/c/acme/artefatos"), 200, "plano.md", "v2", tk.Key)
	mustContain(t, u.get("/c/acme/artefatos/"+v2+"?ok=enviado"), 200, "<h1>dois</h1>", "comparar com a anterior", "Artefato enviado.")
	mustContain(t, u.get("/c/acme/artefatos/"+v2+"?contra="+v1), 200, "Diferenças: 1.0 → 2.0", "diff-row add", "diff-row del", "linha nova")
	mustContain(t, u.get("/c/acme/tasks/"+tk.Key), 200, "plano.md")

	// conteúdo: HTML nunca abre no navegador
	rec = u.upload("/c/acme/artefatos/novo", nil, "pagina.html", []byte("<script>alert(1)</script>"))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("upload html: %d", rec.Code)
	}
	id := strings.TrimSuffix(strings.TrimPrefix(rec.Header().Get("Location"), "/c/acme/artefatos/"), "?ok=enviado")
	rec = u.get("/c/acme/artefatos/" + id + "/conteudo")
	mustContain(t, rec, 200, "<script>alert(1)</script>")
	if got := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "attachment") {
		t.Errorf("Content-Disposition = %q", got)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(rec.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Errorf("cabeçalhos de segurança ausentes: %v", rec.Header())
	}
	mustContain(t, u.get("/c/acme/artefatos/"+id), 200, "&lt;script&gt;alert(1)&lt;/script&gt;")

	// nome com barras e diretórios do navegador: só o nome do arquivo vale
	rec = u.upload("/c/acme/artefatos/novo", nil, `../../etc/passwd`, []byte("x"))
	mustContain(t, rec, 303)
	if a, err := u.svc.ListArtifacts(ctx, u.co.ID, service.ArtifactFilter{Query: "passwd"}); err != nil || len(a) != 1 || a[0].Name != "passwd" {
		t.Errorf("artefato do caminho: %+v, %v", a, err)
	}

	// admin apaga a cadeia toda
	rec = u.form("/c/acme/artefatos/"+v2+"/excluir", nil)
	mustContain(t, rec, 303)
	mustContain(t, u.get("/c/acme/artefatos/"+v1), 404)
}

func TestRealArtifactLimitsAreFriendly(t *testing.T) {
	u := newRealUI(t)
	maxFile, _ := u.svc.ArtifactLimits()
	// corpo acima do limite: 413 com a explicação, sem gravar nada
	rec := u.upload("/c/acme/artefatos/novo", nil, "enorme.bin", bytes.Repeat([]byte("a"), int(maxFile)+2<<20))
	mustContain(t, rec, http.StatusRequestEntityTooLarge, "O arquivo passa do limite de 16 MiB por arquivo.")
	// um pouco acima do limite (cabe no corpo): 422 do service, mesma mensagem
	rec = u.upload("/c/acme/artefatos/novo", nil, "grande.bin", bytes.Repeat([]byte("a"), int(maxFile)+1))
	mustContain(t, rec, http.StatusUnprocessableEntity, "O arquivo passa do limite de 16 MiB por arquivo.")
	if n := len(mustList(t, u)); n != 0 {
		t.Errorf("artefatos gravados = %d", n)
	}
}

func mustList(t *testing.T, u *realUI) []service.ArtifactView {
	t.Helper()
	list, err := u.svc.ListArtifacts(context.Background(), u.co.ID, service.ArtifactFilter{AllVersions: true})
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func TestRealGoalsFlow(t *testing.T) {
	u := newRealUI(t)
	ctx := context.Background()
	rec := u.form("/c/acme/metas/nova", url.Values{"title": {"Chegar a 100 seguidores"}, "metric": {"seguidores"}, "unit": {"seg."}, "start": {"20"}, "target": {"100"}, "due": {"2027-06-30"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/c/acme/metas/chegar-a-100-seguidores?ok=criada" {
		t.Fatalf("criar: %d %q\n%.300s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	mustContain(t, u.get("/c/acme/metas"), 200, "Chegar a 100 seguidores", "0%", "0 de 100 seg.")

	tk, err := u.svc.CreateTask(ctx, domain.HumanActor, u.co.ID, service.CreateTaskInput{Title: "Postar", Status: domain.TaskTodo, Priority: domain.PriorityMedium})
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, u.form("/c/acme/metas/chegar-a-100-seguidores/tasks", url.Values{"task": {tk.Key}}), 303)
	mustContain(t, u.get("/c/acme/metas/chegar-a-100-seguidores"), 200, tk.Key, "20 seg.", "Tasks ligadas")
	mustContain(t, u.get("/c/acme/tasks?aba=todas&meta=chegar-a-100-seguidores"), 200, tk.Key)

	// progresso parcial: o gráfico ganha pontos
	mustContain(t, u.form("/c/acme/metas/chegar-a-100-seguidores/progresso", url.Values{"value": {"60"}, "note": {"semana 1"}}), 303)
	mustContain(t, u.get("/c/acme/metas/chegar-a-100-seguidores?ok=progresso"), 200, "50%", `<svg class="goal-chart"`, "semana 1", "Progresso registrado.")

	// cruzar o alvo: atingida e item informativo na Caixa de entrada
	rec = u.form("/c/acme/metas/chegar-a-100-seguidores/progresso", url.Values{"value": {"104"}})
	if rec.Header().Get("Location") != "/c/acme/metas/chegar-a-100-seguidores?ok=atingida" {
		t.Fatalf("Location = %q", rec.Header().Get("Location"))
	}
	mustContain(t, u.get("/c/acme/metas?aba=atingidas"), 200, "Chegar a 100 seguidores")
	mustContain(t, u.get("/c/acme/caixa"), 200, "Metas atingidas", "Chegar a 100 seguidores")
	// meta atingida não recebe mais progresso nem tasks novas
	if rec := u.form("/c/acme/metas/chegar-a-100-seguidores/progresso", url.Values{"value": {"110"}}); rec.Code < 400 {
		t.Errorf("progresso em meta atingida: %d", rec.Code)
	}
}
