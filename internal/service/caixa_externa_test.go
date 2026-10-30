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
