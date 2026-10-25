//go:build integration

package ui_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/secretbox"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/settings"
	"github.com/renatobardi/hanko/internal/storage"
	"github.com/renatobardi/hanko/internal/storage/storagetest"
	"github.com/renatobardi/hanko/internal/testdb"
	"github.com/renatobardi/hanko/internal/ui"
)

const uiSentinel = "SENTINELA-ui-valor-secreto-3380"

func newSecretsUI(t *testing.T, envSet ...string) *realUI {
	t.Helper()
	pool := testdb.New(t)
	broker := events.NewBroker()
	svc := service.New(pool, noopJobs{}, broker)
	box, _ := secretbox.New("0123456789abcdef0123456789abcdef-ui", "")
	env := map[string]bool{}
	for _, k := range envSet {
		env[k] = true
	}
	m := settings.NewManager(map[string]any{settings.ACPAllow: false, settings.ACPCommands: []string{}, settings.ArtifactsMaxBytes: int64(5 << 20),
		settings.ArtifactsQuotaBytes: int64(512 << 20), settings.AlertsSilentFactor: int64(3), settings.AlertsRunStuckAfter: 15 * time.Minute}, env)
	svc.Settings = m
	svc.Integrations = service.IntegrationsConfig{Secrets: box, Settings: m}
	fake := storagetest.New(t, "AK", "SK", "bkt-test", "us-east-1")
	s3, err := storage.NewS3(storage.S3Config{Endpoint: fake.URL(), Bucket: "bkt-test", AccessKey: "AK", SecretKey: "SK", PathStyle: true})
	if err != nil {
		t.Fatal(err)
	}
	svc.Blobs = storage.NewRegistry(storage.NewPostgres(pool), s3)
	mux := http.NewServeMux()
	ui.Mount(mux, ui.Deps{Svc: svc, Broker: broker, Logger: slog.New(slog.DiscardHandler), Version: "it", Platform: ui.PlatformInfo{SecretKeySet: true}})
	co, err := svc.CreateCompany(context.Background(), domain.HumanActor, service.CreateCompanyInput{Name: "Acme", Slug: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	return &realUI{t: t, svc: svc, co: co, h: mux}
}

func bodyOf(rec *httptest.ResponseRecorder) string { return rec.Body.String() }

func TestUISecretsPage(t *testing.T) {
	u := newSecretsUI(t)
	const page = "/c/acme/configuracoes/segredos"

	rec := u.get(page)
	if rec.Code != 200 || !strings.Contains(bodyOf(rec), "Nenhum segredo") {
		t.Fatalf("página vazia: %d", rec.Code)
	}

	// criar com valor: redireciona e o valor nunca aparece
	rec = u.form(page, url.Values{"name": {"openrouter-key"}, "description": {"chave do gateway"}, "value": {uiSentinel}})
	if rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "ok=criado") {
		t.Fatalf("criar: %d %s", rec.Code, bodyOf(rec))
	}
	rec = u.get(page)
	b := bodyOf(rec)
	if rec.Code != 200 || !strings.Contains(b, "openrouter-key") || !strings.Contains(b, "${secret:openrouter-key}") || strings.Contains(b, uiSentinel) {
		t.Fatalf("lista: %d (valor na página? %v)", rec.Code, strings.Contains(b, uiSentinel))
	}

	// gerar: o valor aparece na resposta (sem redirecionar, sem cache) e sai da lista seguinte
	rec = u.form(page, url.Values{"name": {"gerado"}, "generate": {"1"}})
	b = bodyOf(rec)
	m := regexp.MustCompile(`hk_[A-Za-z0-9_-]{40,}`).FindString(b)
	if rec.Code != 200 || m == "" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("gerar: %d cache=%q valor=%q", rec.Code, rec.Header().Get("Cache-Control"), m)
	}
	if strings.Contains(bodyOf(u.get(page)), m) {
		t.Fatal("o valor gerado não pode reaparecer na lista")
	}

	// revelar uma vez: a página mostra o valor; a segunda vez mostra o conflito
	rec = u.form(page+"/openrouter-key/revelar", url.Values{})
	if rec.Code != 200 || !strings.Contains(bodyOf(rec), uiSentinel) {
		t.Fatalf("revelar: %d", rec.Code)
	}
	rec = u.form(page+"/openrouter-key/revelar", url.Values{})
	if rec.Code != 409 || strings.Contains(bodyOf(rec), uiSentinel) {
		t.Fatalf("revelar de novo: %d (valor na página? %v)", rec.Code, strings.Contains(bodyOf(rec), uiSentinel))
	}

	// rotacionar
	rec = u.form(page+"/openrouter-key/rotacionar", url.Values{"value": {"novo-valor"}})
	if rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "ok=rotacionado") {
		t.Fatalf("rotacionar: %d", rec.Code)
	}

	// erros no formulário: nome inválido e duplicado, sempre sem ecoar o valor
	rec = u.form(page, url.Values{"name": {"1ruim"}, "value": {uiSentinel}})
	if rec.Code != 422 || strings.Contains(bodyOf(rec), uiSentinel) {
		t.Fatalf("nome inválido: %d", rec.Code)
	}
	rec = u.form(page, url.Values{"name": {"openrouter-key"}, "value": {uiSentinel}})
	if rec.Code != 409 || strings.Contains(bodyOf(rec), uiSentinel) {
		t.Fatalf("duplicado: %d", rec.Code)
	}

	// em uso: apagar é recusado; com "apagar mesmo em uso" apaga
	if _, err := u.svc.HirePersona(context.Background(), domain.HumanActor, u.co.ID, service.HirePersonaInput{Name: "Ana", Agent: "fake",
		AdapterConfig: []byte(`{"env":{"K":"${secret:openrouter-key}"}}`)}); err != nil {
		t.Fatal(err)
	}
	rec = u.form(page+"/openrouter-key/apagar", url.Values{})
	if rec.Code != 409 || !strings.Contains(bodyOf(rec), "persona:ana") && !strings.Contains(bodyOf(rec), "em uso") {
		t.Fatalf("apagar em uso: %d", rec.Code)
	}
	rec = u.form(page+"/openrouter-key/apagar", url.Values{"force": {"1"}})
	if rec.Code != 303 {
		t.Fatalf("apagar com force: %d", rec.Code)
	}

	// segredos pessoais pedem uma pessoa autenticada; no modo trusted a seção explica
	if !strings.Contains(bodyOf(u.get(page)), "pessoa autenticada") {
		t.Fatal("a seção pessoal deveria explicar o requisito")
	}
}

func TestUIInstanceSettingsPage(t *testing.T) {
	u := newSecretsUI(t, "HANKO_ACP_ALLOW")
	const page = "/c/acme/configuracoes/instancia"

	rec := u.get(page)
	b := bodyOf(rec)
	if rec.Code != 200 || !strings.Contains(b, "acp.allow") || !strings.Contains(b, "travada por HANKO_ACP_ALLOW") || !strings.Contains(b, "postgres") {
		t.Fatalf("página: %d", rec.Code)
	}
	// salvar uma chave livre
	rec = u.form(page+"/alerts.silent_factor", url.Values{"value": {"8"}})
	if rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "ok=salvo") {
		t.Fatalf("salvar: %d %s", rec.Code, bodyOf(rec))
	}
	if !strings.Contains(bodyOf(u.get(page)), "alerts.silent_factor") || !strings.Contains(bodyOf(u.get(page)), "Voltar ao padrão") {
		t.Fatal("a chave com valor no banco deve oferecer voltar ao padrão")
	}
	// chave travada: recusa com a explicação
	rec = u.form(page+"/acp.allow", url.Values{"value": {"true"}})
	if rec.Code != 409 || !strings.Contains(bodyOf(rec), "Conflito") && !strings.Contains(bodyOf(rec), "HANKO_ACP_ALLOW") {
		t.Fatalf("chave travada: %d", rec.Code)
	}
	// valor inválido
	rec = u.form(page+"/alerts.silent_factor", url.Values{"value": {"0"}})
	if rec.Code != 422 {
		t.Fatalf("valor inválido: %d", rec.Code)
	}
	// voltar ao padrão
	rec = u.form(page+"/alerts.silent_factor/padrao", url.Values{})
	if rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "ok=padrao") {
		t.Fatalf("padrão: %d", rec.Code)
	}
	// histórico
	if !strings.Contains(bodyOf(u.get(page)), "Histórico de mudanças") {
		t.Fatal("falta o histórico")
	}
	// as abas aparecem nas duas telas
	for _, p := range []string{page, "/c/acme/configuracoes/segredos"} {
		b := bodyOf(u.get(p))
		if !strings.Contains(b, "/configuracoes/segredos") || !strings.Contains(b, "/configuracoes/instancia") {
			t.Fatalf("abas ausentes em %s", p)
		}
	}
}

// Import de .env pela UI com o service de verdade (fatia 7, #62): o link aparece, a prévia não grava, gravar cria os
// segredos e o valor não volta na lista.
func TestUIImportEnv(t *testing.T) {
	u := newSecretsUI(t)
	const page = "/c/acme/configuracoes/segredos"
	const envPage = page + "/importar-env"
	const valor = "SENTINELA-ui-import-env-3b9f"

	if rec := u.get(page); rec.Code != 200 || !strings.Contains(bodyOf(rec), "importar-env") {
		t.Fatalf("link do import na lista: %d", rec.Code)
	}
	if rec := u.get(envPage); rec.Code != 200 || !strings.Contains(bodyOf(rec), `name="content"`) {
		t.Fatalf("formulário: %d", rec.Code)
	}
	form := url.Values{"content": {"# banco\nexport DB_HOST=h1\nTOKEN=\"" + valor + "\" # fim\nVAZIA=\n"}, "mode": {"items"}, "etapa": {"previa"}}
	rec := u.form(envPage, form)
	b := bodyOf(rec)
	if rec.Code != 200 || !strings.Contains(b, "DB_HOST") || !strings.Contains(b, "linha 4: valor vazio") {
		t.Fatalf("prévia: %d %s", rec.Code, b)
	}
	if strings.Contains(bodyOf(u.get(page)), "DB_HOST") {
		t.Fatal("a prévia gravou")
	}

	form.Set("etapa", "gravar")
	rec = u.form(envPage, form)
	if rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "ok=env") {
		t.Fatalf("gravar: %d %s", rec.Code, bodyOf(rec))
	}
	b = bodyOf(u.get(page))
	if !strings.Contains(b, "DB_HOST") || !strings.Contains(b, "TOKEN") || strings.Contains(b, valor) {
		t.Fatalf("lista depois do import (valor na página? %v)", strings.Contains(b, valor))
	}
}
