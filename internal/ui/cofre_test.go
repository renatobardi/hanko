package ui

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/uitest"
)

// Tela do cofre (ADR-0068, fatia 1): campos de um segredo e coleções. O backend é um fake em memória que, de
// propósito, devolve texto também para o campo secreto: a tela é que não pode mostrá-lo.

const (
	cofreSecretText  = "SENTINELA-campo-secreto-7741"
	cofreVisibleText = "usuario-visivel-9921"
)

type cofreFake struct {
	*uitest.Fake
	removed []string
	deleted []string
	gen     []domain.GenOptions // opções do gerador que chegaram ao backend, na ordem

	envPreviews, envImports []service.EnvImportInput // pedidos do import de .env que chegaram ao backend
	envImportErr            error                    // erro que ImportEnv devolve (nil = grava)
	reauthPasswords         []string                 // senhas que chegaram em ReauthWithPassword
}

func (c *cofreFake) CreateSecretField(_ context.Context, _ domain.Actor, _ domain.ID, _ domain.SecretScope, _ string, in service.CreateSecretFieldInput) (service.SecretFieldView, error) {
	c.gen = append(c.gen, in.Gen)
	return service.SecretFieldView{}, nil
}

// VaultHealth devolve um achado de cada categoria, para a página de saúde ter linhas para mostrar.
func (c *cofreFake) VaultHealth(context.Context, domain.ID) (service.HealthReport, error) {
	return service.HealthReport{Findings: []domain.HealthFinding{
		{Kind: domain.HealthOrphan, Secret: "solto", Detail: "nenhuma persona, integração, ambiente, plugin, canal ou gatilho usa este segredo"},
		{Kind: domain.HealthStale, Secret: "velho", Field: "value", Detail: "sem rotação há 100 dias (prazo 30)"},
	}}, nil
}

func (c *cofreFake) ListSecretFields(context.Context, domain.ID, domain.SecretScope, string) ([]service.SecretFieldView, error) {
	return []service.SecretFieldView{
		{SecretField: domain.SecretField{Name: "senha", Kind: domain.SecretFieldText, Sensitive: true, Version: 2}, VisibleText: cofreSecretText},
		{SecretField: domain.SecretField{Name: "usuario", Kind: domain.SecretFieldText, Sensitive: false, Version: 1}, VisibleText: cofreVisibleText},
	}, nil
}

func (c *cofreFake) UpdateSecretFieldMeta(context.Context, domain.Actor, domain.ID, domain.SecretScope, string, string, *bool, *int) (service.SecretFieldView, error) {
	return service.SecretFieldView{}, nil
}

func (c *cofreFake) RotateSecretField(_ context.Context, _ domain.Actor, _ domain.ID, _ domain.SecretScope, _ string, _ string, in service.SecretFieldValueInput) (service.SecretFieldView, error) {
	c.gen = append(c.gen, in.Gen)
	return service.SecretFieldView{}, nil
}

func (c *cofreFake) CreateCollection(context.Context, domain.Actor, domain.ID, service.CollectionInput) (service.CollectionView, error) {
	return service.CollectionView{}, nil
}

func (c *cofreFake) ListCollections(context.Context, domain.ID) ([]service.CollectionView, error) {
	return []service.CollectionView{
		{SecretCollection: domain.SecretCollection{Name: "padrao", IsDefault: true}, Secrets: 3},
		{SecretCollection: domain.SecretCollection{Name: "infra"}, Secrets: 1},
	}, nil
}

func (c *cofreFake) GetCollection(_ context.Context, _ domain.ID, ref string) (service.CollectionView, error) {
	return service.CollectionView{SecretCollection: domain.SecretCollection{Name: ref, IsDefault: ref == "padrao"}}, nil
}

func (c *cofreFake) DeleteCollection(_ context.Context, _ domain.Actor, _ domain.ID, ref string) error {
	c.deleted = append(c.deleted, ref)
	return nil
}

func (c *cofreFake) ListCollectionSecrets(context.Context, domain.ID, string) ([]domain.Secret, error) {
	return []domain.Secret{{Name: "openrouter-key"}}, nil
}

func (c *cofreFake) AddSecretToCollection(context.Context, domain.Actor, domain.ID, string, string) error {
	return nil
}

func (c *cofreFake) RemoveSecretFromCollection(_ context.Context, _ domain.Actor, _ domain.ID, _, name string) error {
	c.removed = append(c.removed, name)
	return nil
}

func (c *cofreFake) ListCollectionGrants(context.Context, domain.ID, string) ([]service.CollectionGrantView, error) {
	return nil, nil
}

func newCofreEnv(t *testing.T, role domain.MemberRole) (*env, *cofreFake) {
	t.Helper()
	f := uitest.Sample()
	f.MyRoleIs = role
	cf := &cofreFake{Fake: f}
	b := events.NewBroker()
	s := &server{be: cf, broker: b, version: "test", now: uitest.Now, sseTick: 50 * time.Millisecond, log: testLogger(t)}
	mux := http.NewServeMux()
	s.routes(mux)
	return &env{f: f, broker: b, h: mux}, cf
}

const (
	cofreFieldsPage = "/c/hanko/configuracoes/segredos/openrouter-key/campos"
	cofreCollPage   = "/c/hanko/configuracoes/colecoes"
)

func TestCofreFieldsNeverShowSecretValue(t *testing.T) {
	for _, role := range []domain.MemberRole{domain.MemberMember, domain.MemberAdmin} {
		e, _ := newCofreEnv(t, role)
		rec := e.get(t, cofreFieldsPage)
		wantStatus(t, rec, 200)
		wantNotContains(t, rec, cofreSecretText)                   // (a)
		wantContains(t, rec, cofreVisibleText, "senha", "usuario") // (b)
	}
}

func TestCofreFieldFormsByRole(t *testing.T) {
	e, _ := newCofreEnv(t, domain.MemberMember)
	rec := e.get(t, cofreFieldsPage)
	wantNotContains(t, rec, `action="`+cofreFieldsPage+`"`, "/valor", "/marca")
	e, _ = newCofreEnv(t, domain.MemberAdmin)
	rec = e.get(t, cofreFieldsPage)
	wantContains(t, rec, `action="`+cofreFieldsPage+`"`, "/campos/senha/valor", "/campos/usuario/marca", "requer reautenticação")
}

func TestCofreCollectionsMemberHasNoWriteControls(t *testing.T) {
	e, _ := newCofreEnv(t, domain.MemberMember)
	rec := e.get(t, cofreCollPage)
	wantStatus(t, rec, 200)
	wantContains(t, rec, "infra", "padrao")
	wantNotContains(t, rec, `action="`+cofreCollPage+`"`) // (c) criar
	rec = e.get(t, cofreCollPage+"/infra")
	wantStatus(t, rec, 200)
	wantContains(t, rec, "openrouter-key")
	wantNotContains(t, rec, "/apagar", "/remover", "/colecoes/infra/segredos\"") // (c) apagar, tirar, pôr
}

func TestCofreCollectionsAdminAndDefault(t *testing.T) {
	e, cf := newCofreEnv(t, domain.MemberAdmin)
	wantContains(t, e.get(t, cofreCollPage), `action="`+cofreCollPage+`"`)
	wantContains(t, e.get(t, cofreCollPage+"/infra"), cofreCollPage+"/infra/apagar", "/infra/segredos/openrouter-key/remover")
	rec := e.get(t, cofreCollPage+"/padrao") // (d)
	wantStatus(t, rec, 200)
	wantNotContains(t, rec, "/padrao/apagar")
	wantContains(t, rec, "/padrao/segredos/openrouter-key/remover")

	rec = e.post(t, cofreCollPage+"/infra/apagar", url.Values{}, false)
	wantStatus(t, rec, 303)
	rec = e.post(t, cofreCollPage+"/infra/segredos/openrouter-key/remover", url.Values{}, false)
	wantStatus(t, rec, 303)
	if len(cf.deleted) != 1 || len(cf.removed) != 1 {
		t.Fatalf("deleted=%v removed=%v", cf.deleted, cf.removed)
	}
}
