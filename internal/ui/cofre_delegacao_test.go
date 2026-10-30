package ui

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Delegação de um segredo pessoal na tela de campos (ADR-0068 §6, #117 fatia 2): criar com alvo e prazo, listar só as
// do segredo e revogar. A tela só existe no escopo do usuário; o valor do segredo nunca entra na página.

const (
	delegBase       = "/c/hanko/configuracoes/segredos/openrouter-key"
	delegPage       = delegBase + "/campos?escopo=usuario"
	delegCreatePath = delegBase + "/delegacoes?escopo=usuario"
	delegTask       = "0198f2a0-aaaa-7000-8000-000000000001"
	delegAtiva      = "0198f2a0-bbbb-7000-8000-000000000002"
	delegRevogada   = "0198f2a0-bbbb-7000-8000-000000000003"
	delegOutroSeg   = "0198f2a0-cccc-7000-8000-000000000004"
	delegVencida    = "0198f2a0-bbbb-7000-8000-000000000005"
)

func delegID(t *testing.T, s string) domain.ID {
	t.Helper()
	id, err := domain.ParseID(s)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// CreateSecretDelegation guarda o pedido e devolve o erro configurado.
func (c *cofreFake) CreateSecretDelegation(_ context.Context, _ domain.Actor, _ domain.ID, name string, in service.CreateDelegationInput) (service.DelegationView, error) {
	c.delegSecrets = append(c.delegSecrets, name)
	c.delegCreated = append(c.delegCreated, in)
	return service.DelegationView{}, c.delegErr
}

// ListMyDelegations devolve as delegações configuradas no teste (de todos os segredos do usuário).
func (c *cofreFake) ListMyDelegations(context.Context, domain.ID, int) ([]service.DelegationView, error) {
	return c.delegations, nil
}

// RevokeSecretDelegation guarda o id revogado e devolve o erro configurado.
func (c *cofreFake) RevokeSecretDelegation(_ context.Context, _ domain.Actor, _ domain.ID, id domain.ID) error {
	c.delegRevoked = append(c.delegRevoked, id)
	return c.delegErr
}

func delegFixture(t *testing.T) []service.DelegationView {
	t.Helper()
	exp := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	revogada := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	return []service.DelegationView{
		{ID: delegID(t, delegAtiva), Secret: "openrouter-key", TargetType: domain.DelegationTask, TargetID: delegID(t, delegTask), ExpiresAt: exp, Active: true},
		{ID: delegID(t, delegRevogada), Secret: "openrouter-key", TargetType: domain.DelegationConversation, TargetID: delegID(t, delegTask), ExpiresAt: exp, RevokedAt: &revogada},
		{ID: delegID(t, delegOutroSeg), Secret: "outro-segredo", TargetType: domain.DelegationTask, TargetID: delegID(t, delegTask), ExpiresAt: exp, Active: true},
		{ID: delegID(t, delegVencida), Secret: "openrouter-key", TargetType: domain.DelegationTask, TargetID: delegID(t, delegTask), ExpiresAt: exp},
	}
}

func TestDelegacaoSoNoEscopoDoUsuario(t *testing.T) {
	e, _ := newDelegEnv(t)
	rec := e.get(t, delegPage)
	wantStatus(t, rec, 200)
	wantContains(t, rec, "Delegações deste segredo", `action="`+delegCreatePath+`"`, `name="alvo_tipo"`, `name="alvo"`, `name="prazo"`)

	// no escopo da empresa a tela não oferece delegação, nem para admin
	e, _ = newCofreEnv(t, domain.MemberAdmin)
	rec = e.get(t, cofreFieldsPage)
	wantStatus(t, rec, 200)
	wantNotContains(t, rec, "Delegações deste segredo", "/delegacoes")
}

func TestDelegacaoListaSoAsDoSegredoEMostraEstado(t *testing.T) {
	e, cf := newDelegEnv(t)
	cf.delegations = delegFixture(t)
	rec := e.get(t, delegPage)
	wantStatus(t, rec, 200)
	wantContains(t, rec, delegTask, "ativa", "revogada", "inativa", delegBase+"/delegacoes/"+delegAtiva+"/revogar?escopo=usuario")
	wantNotContains(t, rec, delegBase+"/delegacoes/"+delegVencida+"/revogar")                 // inativa não tem botão de revogar
	wantNotContains(t, rec, delegOutroSeg, delegBase+"/delegacoes/"+delegRevogada+"/revogar") // revogada não tem botão; outro segredo fica de fora
}

func TestDelegacaoSemDelegacoesDizSoIsso(t *testing.T) {
	e, _ := newDelegEnv(t)
	rec := e.get(t, delegPage)
	wantStatus(t, rec, 200)
	wantContains(t, rec, "Nenhuma delegação deste segredo.")
}

func TestDelegacaoCriaComAlvoEPrazo(t *testing.T) {
	e, cf := newDelegEnv(t)
	rec := e.post(t, delegCreatePath, map[string][]string{"alvo_tipo": {"task"}, "alvo": {delegTask}, "prazo": {"604800"}}, false)
	if rec.Code != http.StatusSeeOther && rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want redirecionamento", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != delegBase+"/campos?escopo=usuario&ok=delegacao" {
		t.Fatalf("Location = %q", loc)
	}
	if len(cf.delegCreated) != 1 || cf.delegSecrets[0] != "openrouter-key" {
		t.Fatalf("pedido: %+v nomes %v", cf.delegCreated, cf.delegSecrets)
	}
	in := cf.delegCreated[0]
	if in.TargetType != domain.DelegationTask || in.TargetID != delegID(t, delegTask) || in.TTL != 7*24*time.Hour {
		t.Fatalf("pedido ao service: %+v", in)
	}
}

func TestDelegacaoRecusaPrazoAcimaDeSeteDiasSemChamarService(t *testing.T) {
	e, cf := newDelegEnv(t)
	rec := e.post(t, delegCreatePath, map[string][]string{"alvo_tipo": {"task"}, "alvo": {delegTask}, "prazo": {"604801"}}, false)
	wantContains(t, rec, "Prazo inválido: de 0 a 7 dias.")
	if len(cf.delegCreated) != 0 {
		t.Fatalf("prazo acima de 7 dias não pode chegar ao service: %+v", cf.delegCreated)
	}
}

func TestDelegacaoIdDeAlvoInvalidoVoltaSemChamarService(t *testing.T) {
	e, cf := newDelegEnv(t)
	rec := e.post(t, delegCreatePath, map[string][]string{"alvo_tipo": {"task"}, "alvo": {"nao-e-uuid"}, "prazo": {"86400"}}, false)
	wantContains(t, rec, "Alvo inválido: informe o id de uma task, conversa ou run sua.", `value="nao-e-uuid"`)
	if len(cf.delegCreated) != 0 {
		t.Fatalf("alvo que não é uuid não pode chegar ao service: %+v", cf.delegCreated)
	}
}

func TestDelegacaoRecusaDoServiceVoltaComoErro(t *testing.T) {
	e, cf := newDelegEnv(t)
	cf.delegErr = fmt.Errorf("o alvo não é um trabalho que você pediu: %w", domain.ErrForbidden)
	rec := e.post(t, delegCreatePath, map[string][]string{"alvo_tipo": {"task"}, "alvo": {delegTask}, "prazo": {"86400"}}, false)
	wantStatus(t, rec, http.StatusForbidden)
}

func TestDelegacaoRevogaPeloId(t *testing.T) {
	e, cf := newDelegEnv(t)
	rec := e.post(t, delegBase+"/delegacoes/"+delegAtiva+"/revogar?escopo=usuario", nil, false)
	if loc := rec.Header().Get("Location"); loc != delegBase+"/campos?escopo=usuario&ok=delegacao-revogada" {
		t.Fatalf("Location = %q (status %d)", loc, rec.Code)
	}
	if len(cf.delegRevoked) != 1 || cf.delegRevoked[0] != delegID(t, delegAtiva) {
		t.Fatalf("revogadas: %v", cf.delegRevoked)
	}
}

func TestDelegacaoRevogaComIdInvalidoNaoChamaService(t *testing.T) {
	e, cf := newDelegEnv(t)
	rec := e.post(t, delegBase+"/delegacoes/nao-e-uuid/revogar?escopo=usuario", nil, false)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	if len(cf.delegRevoked) != 0 {
		t.Fatalf("id inválido não pode chegar ao service: %v", cf.delegRevoked)
	}
}

func TestDelegacaoAvisoDeCriadaEhDeRevogada(t *testing.T) {
	e, _ := newDelegEnv(t)
	wantContains(t, e.get(t, delegPage+"&ok=delegacao"), "Delegação criada.")
	wantContains(t, e.get(t, delegPage+"&ok=delegacao-revogada"), "Delegação revogada.")
}

// newDelegEnv abre o cofre com uma pessoa autenticada por sessão: a tela de delegação só escreve com identidade.
func newDelegEnv(t *testing.T) (*env, *cofreFake) {
	t.Helper()
	e, cf := newCofreEnv(t, domain.MemberMember)
	comSessao(e)
	return e, cf
}

func TestDelegacaoRevogaRecusadaPeloServiceVoltaComoErro(t *testing.T) {
	e, cf := newDelegEnv(t)
	cf.delegErr = domain.ErrNotFound
	rec := e.post(t, delegBase+"/delegacoes/"+delegAtiva+"/revogar?escopo=usuario", nil, false)
	wantStatus(t, rec, http.StatusNotFound)
}
