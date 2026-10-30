package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// findExternal devolve o item de agente ou host externo da Caixa pelo tipo e pelo nome.
func findExternal(in service.Inbox, kind domain.InboxItemKind, name string) (service.InboxItem, bool) {
	for _, it := range in.Items() {
		if it.Kind == kind && it.Title == name {
			return it, true
		}
	}
	return service.InboxItem{}, false
}

// Agente e host externos novos aparecem na Caixa como informativos: ninguém precisa agir, então ficam fora de Count
// e de Unread (ADR-0075 item 2; ADR-0077).
func TestInboxListsNewExternalAgentsAndHostsAsInformational(t *testing.T) {
	e, key, _ := derivEnv(t)
	ana := mkUser(t, e, "ana.externa@x.com", domain.RoleMember)
	join(t, e, ana, domain.MemberMember)
	writeBatch(t, e, key, derivBatch(t, derivRec{id: "i1", agent: "claude", host: "oute-server", at: time.Now().UnixNano()}))

	in, err := e.svc.Inbox(as(ana), e.co.ID)
	if err != nil {
		t.Fatal(err)
	}
	agent, ok := findExternal(in, domain.InboxExternalAgent, "claude")
	if !ok || !agent.Informational {
		t.Fatalf("agente novo na Caixa = %+v (%v), quero informativo", agent, ok)
	}
	if _, ok := findExternal(in, domain.InboxExternalHost, "oute-server"); !ok {
		t.Fatal("host novo não aparece na Caixa")
	}
	if in.Count() != 0 || in.Unread != 0 {
		t.Errorf("contagem = %d, não lidos = %d; quero 0 e 0 (informativos ficam fora)", in.Count(), in.Unread)
	}
}

// Agente criado há mais de 30 dias não é mais novo: sai da Caixa.
func TestInboxLeavesOutOldExternalAgents(t *testing.T) {
	e, key, _ := derivEnv(t)
	ana := mkUser(t, e, "ana.antiga@x.com", domain.RoleMember)
	join(t, e, ana, domain.MemberMember)
	writeBatch(t, e, key, derivBatch(t, derivRec{id: "o1", agent: "codex", host: "oute-server", at: time.Now().UnixNano()}))
	if _, err := e.svc.Pool.Exec(context.Background(),
		`UPDATE external_agent SET created_at = now() - interval '40 days' WHERE company_id = $1 AND name = 'codex'`, e.co.ID); err != nil {
		t.Fatal(err)
	}
	in, err := e.svc.Inbox(as(ana), e.co.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findExternal(in, domain.InboxExternalAgent, "codex"); ok {
		t.Error("agente de 40 dias ainda está na Caixa; quero fora da janela de 30 dias")
	}
}

// Lido segue o estado da pessoa, como em todo item da Caixa: o item continua, marcado como lido.
func TestInboxExternalItemFollowsReadState(t *testing.T) {
	e, key, _ := derivEnv(t)
	ana := mkUser(t, e, "ana.lida@x.com", domain.RoleMember)
	join(t, e, ana, domain.MemberMember)
	writeBatch(t, e, key, derivBatch(t, derivRec{id: "r1", agent: "gemini", host: "oute-server", at: time.Now().UnixNano()}))

	in, err := e.svc.Inbox(as(ana), e.co.ID)
	if err != nil {
		t.Fatal(err)
	}
	agent, ok := findExternal(in, domain.InboxExternalAgent, "gemini")
	if !ok {
		t.Fatal("agente novo não está na Caixa")
	}
	if _, err := e.svc.SetInboxState(as(ana), domain.HumanActor, e.co.ID, domain.InboxRead,
		[]service.InboxRef{{Kind: domain.InboxExternalAgent, ID: agent.ID}}); err != nil {
		t.Fatal(err)
	}
	in, err = e.svc.Inbox(as(ana), e.co.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := findExternal(in, domain.InboxExternalAgent, "gemini"); !ok || !got.Read {
		t.Errorf("depois de lido: presente=%v lido=%v; quero presente e lido", ok, got.Read)
	}
}

// names devolve os nomes dos itens externos de uma visão da Caixa.
func externalNames(t *testing.T, e *env, u domain.User, view domain.InboxView) map[string]int {
	t.Helper()
	items, err := e.svc.ListInboxItems(as(u), e.co.ID, view)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int{}
	for _, it := range items {
		if it.Kind == domain.InboxExternalAgent || it.Kind == domain.InboxExternalHost {
			out[it.Title]++
		}
	}
	return out
}

// As visões seguem o estado da pessoa: arquivado sai de "tudo" e vai para "arquivados"; lido sai de "não lidos".
func TestExternalItemsFollowTheInboxViews(t *testing.T) {
	e, key, _ := derivEnv(t)
	ana := mkUser(t, e, "ana.visoes@x.com", domain.RoleMember)
	join(t, e, ana, domain.MemberMember)
	writeBatch(t, e, key, derivBatch(t, derivRec{id: "v1", agent: "claude", host: "oute-server", at: time.Now().UnixNano()}))

	if got := externalNames(t, e, ana, domain.InboxViewUnread); got["claude"] != 1 {
		t.Fatalf("não lidos antes de qualquer ação = %v, quero claude", got)
	}
	in, err := e.svc.Inbox(as(ana), e.co.ID)
	if err != nil {
		t.Fatal(err)
	}
	agent, _ := findExternal(in, domain.InboxExternalAgent, "claude")
	ref := []service.InboxRef{{Kind: domain.InboxExternalAgent, ID: agent.ID}}

	if _, err := e.svc.SetInboxState(as(ana), domain.HumanActor, e.co.ID, domain.InboxRead, ref); err != nil {
		t.Fatal(err)
	}
	if got := externalNames(t, e, ana, domain.InboxViewUnread); got["claude"] != 0 {
		t.Errorf("não lidos depois de lido = %v, quero fora", got)
	}
	if got := externalNames(t, e, ana, domain.InboxViewAll); got["claude"] != 1 {
		t.Errorf("tudo depois de lido = %v, quero claude", got)
	}

	if _, err := e.svc.SetInboxState(as(ana), domain.HumanActor, e.co.ID, domain.InboxArchive, ref); err != nil {
		t.Fatal(err)
	}
	if got := externalNames(t, e, ana, domain.InboxViewAll); got["claude"] != 0 {
		t.Errorf("tudo depois de arquivar = %v, quero fora", got)
	}
	if got := externalNames(t, e, ana, domain.InboxViewArchived); got["claude"] != 1 {
		t.Errorf("arquivados depois de arquivar = %v, quero claude", got)
	}

	if _, err := e.svc.SetInboxState(as(ana), domain.HumanActor, e.co.ID, domain.InboxUnarchive, ref); err != nil {
		t.Fatal(err)
	}
	if got := externalNames(t, e, ana, domain.InboxViewArchived); got["claude"] != 0 {
		t.Errorf("arquivados depois de desarquivar = %v, quero fora", got)
	}
}

// Agente de outra empresa não aparece na Caixa desta.
func TestExternalItemsAreScopedToTheCompany(t *testing.T) {
	e, key, _ := derivEnv(t)
	ctx := context.Background()
	ana := mkUser(t, e, "ana.empresa@x.com", domain.RoleMember)
	join(t, e, ana, domain.MemberMember)
	writeBatch(t, e, key, derivBatch(t, derivRec{id: "e1", agent: "claude", host: "oute-server", at: time.Now().UnixNano()}))

	other, err := e.svc.CreateCompany(ctx, domain.HumanActor, service.CreateCompanyInput{Name: "Outra empresa"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Pool.Exec(ctx, `INSERT INTO external_agent (id, company_id, name, created_at, updated_at) VALUES ($1, $2, 'outro-agente', now(), now())`,
		domain.NewID(), other.ID); err != nil {
		t.Fatal(err)
	}
	got := externalNames(t, e, ana, domain.InboxViewAll)
	if got["outro-agente"] != 0 || got["claude"] != 1 {
		t.Errorf("itens desta empresa = %v; quero claude e nada da outra empresa", got)
	}
}

// Repetir o mesmo agente, em outro lote ou com outra caixa, não repete o item.
func TestExternalItemRepeatedFactDoesNotRepeatTheItem(t *testing.T) {
	e, key, _ := derivEnv(t)
	ana := mkUser(t, e, "ana.repete@x.com", domain.RoleMember)
	join(t, e, ana, domain.MemberMember)
	at := time.Now().UnixNano()
	writeBatch(t, e, key, derivBatch(t, derivRec{id: "p1", agent: "claude", host: "oute-server", at: at}))
	writeBatch(t, e, key, derivBatch(t, derivRec{id: "p2", agent: "claude", host: "oute-server", at: at + 1}))
	writeBatch(t, e, key, derivBatch(t, derivRec{id: "p3", agent: "Claude", host: "oute-server", at: at + 2}))
	if got := externalNames(t, e, ana, domain.InboxViewAll); got["claude"] != 1 {
		t.Errorf("itens de claude = %d, quero 1", got["claude"])
	}
}

// Host criado há mais de 30 dias não está na Caixa, mesmo com fato recente.
func TestExternalHostOutsideTheWindowLeavesTheInbox(t *testing.T) {
	e, key, _ := derivEnv(t)
	ana := mkUser(t, e, "ana.janela@x.com", domain.RoleMember)
	join(t, e, ana, domain.MemberMember)
	writeBatch(t, e, key, derivBatch(t, derivRec{id: "j1", agent: "claude", host: "oute-velho", at: time.Now().UnixNano()}))
	if _, err := e.svc.Pool.Exec(context.Background(),
		`UPDATE external_host SET created_at = now() - interval '40 days' WHERE company_id = $1 AND name = 'oute-velho'`, e.co.ID); err != nil {
		t.Fatal(err)
	}
	if got := externalNames(t, e, ana, domain.InboxViewAll); got["oute-velho"] != 0 {
		t.Errorf("host de 40 dias na Caixa = %d, quero 0", got["oute-velho"])
	}
}
