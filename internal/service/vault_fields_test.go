package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Valores sentinela da fatia 1 do cofre (ADR-0068 §1.3 e §3): se algum aparecer onde não deve, o teste falha.
const (
	sentinelV1    = "SENTINELA-cofre-versao-um-1a2b3c"
	sentinelV2    = "SENTINELA-cofre-versao-dois-4d5e6f"
	sentinelV3    = "SENTINELA-cofre-versao-tres-7a8b9c"
	sentinelCampo = "SENTINELA-cofre-campo-secreto-0d1e2f"
)

// personaSemColecao tira o item da coleção padrão e o põe numa coleção sem concessão: a persona não alcança o item.
func personaSemColecao(t *testing.T, e *secEnv, secret, colecao string) {
	t.Helper()
	bg := context.Background()
	e.create(t, secret, sentinelV1)
	if err := e.svc.RemoveSecretFromCollection(bg, domain.HumanActor, e.co.ID, domain.DefaultCollectionName, secret); err != nil {
		t.Fatalf("tirar %s da padrão: %v", secret, err)
	}
	if _, err := e.svc.CreateCollection(bg, domain.HumanActor, e.co.ID, service.CollectionInput{Name: colecao}); err != nil {
		t.Fatalf("criar coleção %s: %v", colecao, err)
	}
	if err := e.svc.AddSecretToCollection(bg, domain.HumanActor, e.co.ID, colecao, secret); err != nil {
		t.Fatalf("pôr %s em %s: %v", secret, colecao, err)
	}
}

func grantPersona(t *testing.T, e *secEnv, colecao string, subject domain.CollectionSubject, slug string) {
	t.Helper()
	if _, err := e.svc.CreateCollectionGrant(context.Background(), domain.HumanActor, e.co.ID, colecao,
		service.GrantInput{SubjectType: subject, Subject: slug, Permission: domain.CollectionUse}); err != nil {
		t.Fatalf("conceder %s a %s: %v", colecao, slug, err)
	}
}

// Criar persona sem coleção falha cedo na run: o erro nomeia a persona e o item, nunca o valor (ADR-0068 §2.5).
func TestPersonaSemColecaoFalhaCedo(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()
	ana := e.persona(t, "Ana", nil)
	personaSemColecao(t, e, "restrito", "infra")
	cfg := json.RawMessage(`{"api_key_ref":"${secret:restrito}"}`)

	_, _, err := e.svc.ResolveAdapterConfig(bg, e.co.ID, ana.ID, cfg)
	if err == nil {
		t.Fatal("persona fora da coleção resolveu o item")
	}
	if !strings.Contains(err.Error(), "restrito") || !strings.Contains(err.Error(), ana.Slug) {
		t.Fatalf("o erro devia nomear o item e a persona: %v", err)
	}
	if strings.Contains(err.Error(), sentinelV1) {
		t.Fatalf("o erro vazou o valor: %v", err)
	}

	// a mesma persona passa quando a coleção é concedida a ela
	grantPersona(t, e, "infra", domain.CollectionSubjectPersona, ana.Slug)
	out, used, err := e.svc.ResolveAdapterConfig(bg, e.co.ID, ana.ID, cfg)
	if err != nil || !strings.Contains(string(out), sentinelV1) || len(used) != 1 {
		t.Fatalf("com a concessão, a run devia resolver: %s %v %v", out, used, err)
	}
	// e uma persona que não recebeu a concessão continua de fora
	beto := e.persona(t, "Beto", nil)
	if _, _, err := e.svc.ResolveAdapterConfig(bg, e.co.ID, beto.ID, cfg); err == nil {
		t.Fatal("concessão a uma persona vazou para outra")
	}
}

// persona_tree vale para a persona e a cadeia abaixo dela no organograma (ADR-0068 §2.2).
func TestColecaoPersonaTree(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()
	ana := e.persona(t, "Ana", nil)
	beto := e.persona(t, "Beto", func(in *service.HirePersonaInput) { in.ReportsTo = &ana.ID })
	caio := e.persona(t, "Caio", nil)
	personaSemColecao(t, e, "tree", "time-ana")
	grantPersona(t, e, "time-ana", domain.CollectionSubjectPersonaTree, ana.Slug)
	cfg := json.RawMessage(`{"api_key_ref":"${secret:tree}"}`)

	if _, _, err := e.svc.ResolveAdapterConfig(bg, e.co.ID, beto.ID, cfg); err != nil {
		t.Fatalf("subordinada da persona_tree devia alcançar: %v", err)
	}
	if _, _, err := e.svc.ResolveAdapterConfig(bg, e.co.ID, caio.ID, cfg); err == nil {
		t.Fatal("persona fora da subárvore alcançou o item")
	}
}

// Campo visível sai para quem lê o segredo; campo secreto nunca sai na listagem, e member sem `manage` não escreve
// (ADR-0068 §1.3 e §2.3).
func TestCampoVisivelESecretoPorPapel(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()
	e.create(t, "login", sentinelCampo)
	visivel := false
	if _, err := e.svc.CreateSecretField(bg, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "login", service.CreateSecretFieldInput{
		Name: "usuario", Sensitive: &visivel, SecretFieldValueInput: service.SecretFieldValueInput{Value: "ana@exemplo.com"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateSecretField(bg, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "login", service.CreateSecretFieldInput{
		Name: "senha", SecretFieldValueInput: service.SecretFieldValueInput{Value: sentinelCampo}}); err != nil {
		t.Fatal(err)
	}

	membro := mkUser(t, e.env, "membro-campos@exemplo.com", domain.RoleMember)
	join(t, e.env, membro, domain.MemberMember)
	fields, err := e.svc.ListSecretFields(as(membro), e.co.ID, domain.SecretScopeCompany, "login")
	if err != nil {
		t.Fatalf("member lista os campos: %v", err)
	}
	seen := map[string]service.SecretFieldView{}
	for _, f := range fields {
		seen[f.Name] = f
	}
	if seen["usuario"].VisibleText != "ana@exemplo.com" {
		t.Fatalf("campo visível não saiu para member: %q", seen["usuario"].VisibleText)
	}
	if seen["senha"].VisibleText != "" || seen["senha"].RevealedValue != "" {
		t.Fatal("campo secreto saiu na listagem")
	}

	err = func() error {
		_, err := e.svc.CreateSecretField(as(membro), domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "login",
			service.CreateSecretFieldInput{Name: "extra", SecretFieldValueInput: service.SecretFieldValueInput{Value: "x"}})
		return err
	}()
	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("member sem manage criou campo: %v", err)
	}

	// o campo value é o próprio segredo: não pode virar visível (sairia para member)
	if _, err := e.svc.UpdateSecretFieldMeta(bg, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "login", domain.DefaultSecretField,
		&visivel, nil, nil); err == nil {
		t.Fatal("o campo value virou visível")
	}
}

// Histórico com retenção (N versões), restaurar cria versão nova e o valor atual segue o campo (ADR-0068 §3).
func TestHistoricoRetencaoERestaurar(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()
	e.create(t, "api", sentinelV1)
	for _, v := range []string{sentinelV2, sentinelV3} {
		if _, err := e.svc.RotateSecretField(bg, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "api", "value",
			service.SecretFieldValueInput{Value: v}); err != nil {
			t.Fatal(err)
		}
	}
	hist, err := e.svc.ListSecretFieldHistory(bg, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "api", "value")
	if err != nil || len(hist) != 2 {
		t.Fatalf("histórico = %d versões (%v), quer 2", len(hist), err)
	}

	// a cópia legada em secrets segue o valor atual: resolver a referência devolve o último valor
	if val, _, err := e.svc.ResolveSecretText(bg, e.co.ID, "${secret:api}"); err != nil || val != sentinelV3 {
		t.Fatalf("referência depois de duas rotações = %q (%v), quer o valor atual", val, err)
	}

	// restaurar a versão 1 cria a versão 4 (nada é apagado) e o valor atual passa a ser o da versão 1
	view, err := e.svc.RestoreSecretFieldVersion(bg, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "api", "value", 1)
	if err != nil {
		t.Fatal(err)
	}
	if view.Version != 4 {
		t.Fatalf("restaurar deveria subir a versão para 4, veio %d", view.Version)
	}
	hist, err = e.svc.ListSecretFieldHistory(bg, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "api", "value")
	if err != nil || len(hist) != 3 {
		t.Fatalf("depois de restaurar, histórico = %d (%v), quer 3", len(hist), err)
	}
	if val, _, err := e.svc.ResolveSecretText(bg, e.co.ID, "${secret:api}"); err != nil || val != sentinelV1 {
		t.Fatalf("referência depois de restaurar = %q (%v), quer a versão 1", val, err)
	}
	var fatos int
	if err := e.svc.Pool.QueryRow(bg, `SELECT count(*) FROM activity_events WHERE verb = 'secret.field_restored'`).Scan(&fatos); err != nil || fatos != 1 {
		t.Fatalf("fato secret.field_restored: %d (%v), quer 1", fatos, err)
	}
}

// A retenção apaga o que passou de N versões ou de D dias, o que guardar menos (ADR-0068 §3.2).
func TestRetencaoDoHistoricoApagaExcedente(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()
	e.create(t, "rot", sentinelV1)
	for i := 0; i < 7; i++ {
		if _, err := e.svc.RotateSecretField(bg, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "rot", "value",
			service.SecretFieldValueInput{Value: sentinelV2}); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := e.svc.PruneSecretFieldHistory(bg)
	if err != nil {
		t.Fatal(err)
	}
	hist, err := e.svc.ListSecretFieldHistory(bg, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "rot", "value")
	if err != nil || len(hist) != domain.DefaultHistoryKeepVersions || removed != 2 {
		t.Fatalf("retenção por N: restou %d (quer %d), removidas %d (quer 2): %v", len(hist), domain.DefaultHistoryKeepVersions, removed, err)
	}

	// versões de mais de 90 dias saem mesmo dentro do limite de N
	if _, err := e.svc.Pool.Exec(bg, `UPDATE secret_field_versions SET replaced_at = now() - interval '100 days'
		WHERE field_id IN (SELECT id FROM secret_fields WHERE secret_id IN (SELECT id FROM secrets WHERE name = 'rot'))`); err != nil {
		t.Fatal(err)
	}
	removed, err = e.svc.PruneSecretFieldHistory(bg)
	if err != nil || removed != domain.DefaultHistoryKeepVersions {
		t.Fatalf("retenção por D: removidas %d (quer %d): %v", removed, domain.DefaultHistoryKeepVersions, err)
	}
}

// Nenhum valor sentinela entra em fato de atividade nem no log, nem no histórico que a API mostra (ADR-0050 §A.10).
func TestNaoVazamentoDeCamposEHistorico(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()
	e.create(t, "vazio", sentinelV1)
	if _, err := e.svc.CreateSecretField(bg, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "vazio", service.CreateSecretFieldInput{
		Name: "segredo", SecretFieldValueInput: service.SecretFieldValueInput{Value: sentinelCampo}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RotateSecretField(bg, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "vazio", "segredo",
		service.SecretFieldValueInput{Value: sentinelV2}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RotateSecretField(bg, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "vazio", "value",
		service.SecretFieldValueInput{Value: sentinelV3}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RestoreSecretFieldVersion(bg, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "vazio", "segredo", 1); err != nil {
		t.Fatal(err)
	}
	for _, sentinela := range []string{sentinelV1, sentinelV2, sentinelV3, sentinelCampo} {
		for _, consulta := range []string{
			`SELECT count(*) FROM activity_events WHERE data::text LIKE '%' || $1 || '%'`,
			`SELECT count(*) FROM secret_fields WHERE value_enc LIKE '%' || $1 || '%' OR external_ref LIKE '%' || $1 || '%'`,
			`SELECT count(*) FROM secret_field_versions WHERE value_enc LIKE '%' || $1 || '%'`,
		} {
			var n int
			if err := e.svc.Pool.QueryRow(bg, consulta, sentinela).Scan(&n); err != nil || n != 0 {
				t.Fatalf("o valor vazou (%d linhas) pela consulta %q: %v", n, consulta, err)
			}
		}
	}
	if strings.Contains(e.logs.String(), sentinelV1) || strings.Contains(e.logs.String(), sentinelCampo) {
		t.Fatal("o log vazou valor de campo")
	}
}

// Coleções: a padrão não se apaga nem se renomeia; nome repetido é recusado; a contagem de segredos segue o conteúdo.
func TestColecoesCRUDEPadrao(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()
	e.create(t, "gh", sentinelV1)
	if _, err := e.svc.CreateCollection(bg, domain.HumanActor, e.co.ID, service.CollectionInput{Name: "infra"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateCollection(bg, domain.HumanActor, e.co.ID, service.CollectionInput{Name: "infra"}); err == nil {
		t.Fatal("nome de coleção repetido foi aceito")
	}
	if err := e.svc.DeleteCollection(bg, domain.HumanActor, e.co.ID, domain.DefaultCollectionName); err == nil {
		t.Fatal("a coleção padrão foi apagada")
	}
	if _, err := e.svc.UpdateCollection(bg, domain.HumanActor, e.co.ID, domain.DefaultCollectionName,
		service.CollectionInput{Name: "outra"}); err == nil {
		t.Fatal("a coleção padrão foi renomeada")
	}
	padrao, err := e.svc.GetCollection(bg, e.co.ID, domain.DefaultCollectionName)
	if err != nil || !padrao.IsDefault || padrao.Secrets != 1 {
		t.Fatalf("padrão = %+v (%v), quer 1 segredo e marcada como padrão", padrao, err)
	}
	if err := e.svc.AddSecretToCollection(bg, domain.HumanActor, e.co.ID, "infra", "gh"); err != nil {
		t.Fatal(err)
	}
	itens, err := e.svc.ListCollectionSecrets(bg, e.co.ID, "infra")
	if err != nil || len(itens) != 1 || itens[0].Name != "gh" {
		t.Fatalf("itens de infra = %v (%v)", itens, err)
	}
	grants, err := e.svc.ListCollectionGrants(bg, e.co.ID, domain.DefaultCollectionName)
	if err != nil || len(grants) != 1 || grants[0].SubjectType != domain.CollectionSubjectAllPersonas {
		t.Fatalf("concessões da padrão = %v (%v), quer all_personas", grants, err)
	}
}
