package ui

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Import de .env na UI (fatia 7, #62): só admin vê e usa; a prévia mostra nomes e não grava; gravar leva o conteúdo e as
// escolhas ao backend; reautenticação pedida abre o campo de senha.

const (
	cofreEnvPage  = "/c/hanko/configuracoes/segredos/importar-env"
	cofreEnvValue = "SENTINELA-ui-env-valor-6d2c"
)

func (c *cofreFake) PreviewEnvImport(_ context.Context, _ domain.ID, in service.EnvImportInput) (service.EnvImportPlan, error) {
	c.envPreviews = append(c.envPreviews, in)
	return service.EnvImportPlan{Collection: "infra", Items: []service.EnvImportItem{
		{Secret: "DB_HOST", Fields: []string{"value"}}, {Secret: "TOKEN", Fields: []string{"value"}, Exists: true}},
		Skipped: []service.EnvImportSkip{{Line: 4, Reason: "valor vazio"}}}, nil
}

func (c *cofreFake) ImportEnv(ctx context.Context, _ domain.Actor, _ domain.ID, in service.EnvImportInput) (service.VaultImportResult, error) {
	c.envImports = append(c.envImports, in)
	if c.needProof && !comProva(ctx) {
		return service.VaultImportResult{}, &domain.ReauthError{WindowMinutes: 5}
	}
	if c.needProof {
		c.proved++
	}
	return service.VaultImportResult{Secrets: 2}, c.envImportErr
}

func (c *cofreFake) ReauthWithPassword(_ context.Context, _ auth.Identity, password string) (service.ReauthProof, error) {
	c.reauthPasswords = append(c.reauthPasswords, password)
	if c.reauthWrong {
		return service.ReauthProof{}, domain.ErrUnauthenticated
	}
	return service.ReauthProof{Method: "password"}, nil
}

func envForm(etapa string) url.Values {
	return url.Values{"content": {"DB_HOST=h\nTOKEN=" + cofreEnvValue + "\n"}, "mode": {"single_item"}, "item_name": {"app"},
		"collection": {"infra"}, "overwrite": {"1"}, "etapa": {etapa}}
}

func TestEnvImportPageIsAdminOnly(t *testing.T) {
	e, _ := newCofreEnv(t, domain.MemberMember)
	wantStatus(t, e.get(t, cofreEnvPage), http.StatusForbidden)

	e, _ = newCofreEnv(t, domain.MemberAdmin)
	rec := e.get(t, cofreEnvPage)
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, `name="content"`, `name="mode"`, `name="collection"`, "infra", "Ver prévia")
	wantNotContains(t, rec, `value="gravar"`)
}

func TestEnvImportPreviewShowsMappingWithoutWriting(t *testing.T) {
	e, cf := newCofreEnv(t, domain.MemberAdmin)
	rec := e.post(t, cofreEnvPage, envForm("previa"), false)
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "DB_HOST", "TOKEN", "já existe", "linha 4: valor vazio", "infra", `value="gravar"`, `name="senha"`)
	if len(cf.envPreviews) != 1 || len(cf.envImports) != 0 {
		t.Fatalf("prévias %d, gravações %d: a prévia não pode gravar", len(cf.envPreviews), len(cf.envImports))
	}
	// a tabela da prévia só tem nomes: o valor aparece, no máximo, no campo de texto que a pessoa colou
	body := rec.Body.String()
	table := body[strings.Index(body, "<table"):strings.Index(body, "</table>")]
	if strings.Contains(table, cofreEnvValue) {
		t.Fatal("a prévia mostrou um valor")
	}
}

func TestEnvImportApplySendsChoicesAndRedirects(t *testing.T) {
	e, cf := newCofreEnv(t, domain.MemberAdmin)
	rec := e.post(t, cofreEnvPage, envForm("gravar"), false)
	wantStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/c/hanko/configuracoes/segredos?ok=env" {
		t.Fatalf("redirect = %q", loc)
	}
	if len(cf.envImports) != 1 {
		t.Fatalf("gravações = %d", len(cf.envImports))
	}
	got := cf.envImports[0]
	want := service.EnvImportInput{Content: "DB_HOST=h\nTOKEN=" + cofreEnvValue + "\n", Mode: service.EnvImportSingleItem, ItemName: "app",
		Collection: "infra", Overwrite: true}
	if got != want {
		t.Fatalf("pedido = %+v, quer %+v", got, want)
	}
}

func TestEnvImportReauthRequiredAsksForThePassword(t *testing.T) {
	e, cf := newCofreEnv(t, domain.MemberAdmin)
	cf.envImportErr = &domain.ReauthError{WindowMinutes: 5}
	rec := e.post(t, cofreEnvPage, envForm("gravar"), false)
	wantStatus(t, rec, http.StatusUnauthorized)
	wantContains(t, rec, "prova recente de identidade", `name="senha"`, "DB_HOST")
}

func TestEnvImportInvalidShowsMessageAndKeepsForm(t *testing.T) {
	e, cf := newCofreEnv(t, domain.MemberAdmin)
	cf.envImportErr = domain.Invalid("content", "nenhuma variável com valor para importar")
	rec := e.post(t, cofreEnvPage, envForm("gravar"), false)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	wantContains(t, rec, "nenhuma variável com valor para importar", `name="content"`)
}
