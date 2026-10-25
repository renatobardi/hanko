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

// Pendências herdadas do #72 e do #86 (issue #95): o 404 ao remover o que não existe, a coleção escolhida na criação,
// o registro de uso no despacho e o aviso de alcance na configuração da persona.

// Critério 4: remover o que já não existe responde "não encontrado", não sucesso.
func TestRemoverDoQueNaoExisteRespondeNaoEncontrado(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()
	e.create(t, "gh", sentinelV1)
	if err := e.svc.RemoveSecretFromCollection(bg, domain.HumanActor, e.co.ID, domain.DefaultCollectionName, "gh"); err != nil {
		t.Fatalf("primeira remoção: %v", err)
	}
	if err := e.svc.RemoveSecretFromCollection(bg, domain.HumanActor, e.co.ID, domain.DefaultCollectionName, "gh"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("remover membro que já saiu: %v, quer ErrNotFound", err)
	}
	if _, err := e.svc.CreateCollection(bg, domain.HumanActor, e.co.ID, service.CollectionInput{Name: "infra"}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.RemoveSecretFromCollection(bg, domain.HumanActor, e.co.ID, "infra", "gh"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("remover de coleção que não tem o item: %v, quer ErrNotFound", err)
	}
	if err := e.svc.DeleteCollection(bg, domain.HumanActor, e.co.ID, "infra"); err != nil {
		t.Fatalf("apagar infra: %v", err)
	}
	if err := e.svc.DeleteCollection(bg, domain.HumanActor, e.co.ID, "infra"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("apagar coleção que já não existe: %v, quer ErrNotFound", err)
	}
}

// Critério 3: escolher a coleção na criação; sem escolha, o segredo entra na padrão; coleção inexistente não cria nada.
func TestCriarSegredoEscolheAColecao(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()
	if _, err := e.svc.CreateCollection(bg, domain.HumanActor, e.co.ID, service.CollectionInput{Name: "infra"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateSecret(bg, domain.HumanActor, e.co.ID, service.CreateSecretInput{Name: "gh", Value: sentinelV1, Collection: "infra"}); err != nil {
		t.Fatalf("criar na coleção escolhida: %v", err)
	}
	if _, err := e.svc.CreateSecret(bg, domain.HumanActor, e.co.ID, service.CreateSecretInput{Name: "padrao", Value: sentinelV2}); err != nil {
		t.Fatalf("criar sem escolha: %v", err)
	}
	if got := secretNames(t, e, "infra"); got != "gh" {
		t.Fatalf("itens de infra = %q, quer só gh", got)
	}
	if got := secretNames(t, e, domain.DefaultCollectionName); got != "padrao" {
		t.Fatalf("itens da padrão = %q, quer só padrao", got)
	}
	if _, err := e.svc.CreateSecret(bg, domain.HumanActor, e.co.ID, service.CreateSecretInput{Name: "pelo-nome", Value: sentinelV3, Collection: domain.DefaultCollectionName}); err != nil {
		t.Fatalf("escolher a padrão pelo nome: %v", err)
	}
	if got := secretNames(t, e, domain.DefaultCollectionName); got != "padrao,pelo-nome" && got != "pelo-nome,padrao" {
		t.Fatalf("itens da padrão = %q, quer padrao e pelo-nome", got)
	}
	_, err := e.svc.CreateSecret(bg, domain.HumanActor, e.co.ID, service.CreateSecretInput{Name: "perdido", Value: sentinelV3, Collection: "não existe"})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("coleção inexistente: %v, quer ErrNotFound", err)
	}
	var n int
	if err := e.svc.Pool.QueryRow(bg, `SELECT count(*) FROM secrets WHERE name = 'perdido'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("segredo com coleção inexistente ficou gravado (%d linhas, %v)", n, err)
	}
}

// secretNames lista os nomes dos itens de uma coleção, separados por vírgula.
func secretNames(t *testing.T, e *secEnv, colecao string) string {
	t.Helper()
	items, err := e.svc.ListCollectionSecrets(context.Background(), e.co.ID, colecao)
	if err != nil {
		t.Fatalf("listar %s: %v", colecao, err)
	}
	names := make([]string, 0, len(items))
	for _, it := range items {
		names = append(names, it.Name)
	}
	return strings.Join(names, ",")
}

// Critério 2: o despacho que resolve ${secret:} grava o uso (via dispatch, com a run e a persona), sem o valor. Fora de
// uma run (prontidão, teste de adapter) não grava nada.
func TestDespachoGravaUsoEmSecretAccessLog(t *testing.T) {
	d := newDelegEnv(t)
	bg := context.Background()
	if _, err := d.svc.CreateSecret(bg, domain.HumanActor, d.co.ID, service.CreateSecretInput{Name: "gh", Value: sentinelV1}); err != nil {
		t.Fatal(err)
	}
	run, _ := d.run(d.ac, t, d.alice, "publicar")
	cfg := json.RawMessage(`{"api_key_ref":"${secret:gh}"}`)

	if _, _, err := d.svc.ResolveAdapterConfig(bg, d.co.ID, d.ana.ID, cfg); err != nil {
		t.Fatalf("resolver sem run: %v", err)
	}
	if dump := accessDump(t, d); dump != "" {
		t.Fatalf("sem run não deve gravar uso, mas gravou: %s", dump)
	}

	out, used, err := d.svc.ResolveRunAdapterConfig(bg, d.co.ID, d.ana.ID, run.ID, cfg)
	if err != nil || !strings.Contains(string(out), sentinelV1) || len(used) != 1 {
		t.Fatalf("despacho da run: %s %v %v", out, used, err)
	}
	want := "dispatch|value|" + run.ID.String() + "|" + d.ana.ID.String()
	if dump := accessDump(t, d); dump != want {
		t.Fatalf("secret_access_log = %q, quer %q", dump, want)
	}
	if dump := accessDump(t, d); strings.Contains(dump, sentinelV1) {
		t.Fatalf("o registro de uso vazou o valor: %s", dump)
	}
}

// accessDump lista as linhas de secret_access_log do segredo gh como via|campo|run|persona. Vazio se não há nenhuma.
func accessDump(t *testing.T, d *delegEnv) string {
	t.Helper()
	var dump string
	if err := d.svc.Pool.QueryRow(context.Background(), `SELECT coalesce(string_agg(l.via || '|' || l.field_name || '|' || coalesce(l.run_id::text, '') || '|' || coalesce(l.persona_id::text, ''), ' '), '')
		FROM secret_access_log l JOIN secrets s ON s.id = l.secret_id WHERE s.name = 'gh'`).Scan(&dump); err != nil {
		t.Fatalf("ler secret_access_log: %v", err)
	}
	return dump
}

// Critério 1: gravar a configuração com um item fora do alcance da persona devolve o aviso (não bloqueia), nomeando a
// persona e a referência, nunca o valor; com a concessão, o aviso some.
func TestAvisoDeAlcancePersonaNaConfiguracao(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()
	personaSemColecao(t, e, "restrito", "infra")
	ana := e.persona(t, "Ana", func(in *service.HirePersonaInput) {
		in.AdapterConfig = json.RawMessage(`{"api_key_ref":"${secret:restrito}"}`)
	})
	ws, err := e.svc.PersonaReachWarnings(bg, e.co.ID, ana.ID)
	if err != nil || len(ws) != 1 {
		t.Fatalf("avisos da persona sem alcance = %v (%v), quer 1", ws, err)
	}
	if !strings.Contains(ws[0], ana.Slug) || !strings.Contains(ws[0], "${secret:restrito}") {
		t.Fatalf("o aviso devia nomear a persona e o item: %q", ws[0])
	}
	if strings.Contains(ws[0], sentinelV1) {
		t.Fatalf("o aviso vazou o valor: %q", ws[0])
	}

	grantPersona(t, e, "infra", domain.CollectionSubjectPersona, ana.Slug)
	if ws, err := e.svc.PersonaReachWarnings(bg, e.co.ID, ana.ID); err != nil || len(ws) != 0 {
		t.Fatalf("com a concessão, ainda há aviso: %v (%v)", ws, err)
	}

	beto := e.persona(t, "Beto", nil)
	if ws, err := e.svc.PersonaReachWarnings(bg, e.co.ID, beto.ID); err != nil || len(ws) != 0 {
		t.Fatalf("persona sem referência com aviso: %v (%v)", ws, err)
	}
}
