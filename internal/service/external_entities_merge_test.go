package service_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// insertConversation põe uma conversa externa ligada a um agente e a um host (o mínimo que a tabela exige).
func insertConversation(t *testing.T, e *env, session, name string, agent, host domain.ID) {
	t.Helper()
	if _, err := e.svc.Pool.Exec(context.Background(),
		`INSERT INTO external_conversation (id, company_id, session_id, name, agent_id, host_id, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, now(), now())`, domain.NewID(), e.co.ID, session, name, agent, host); err != nil {
		t.Fatal(err)
	}
}

// Juntar move a conversa e o host para o que fica: a conversa passa a apontar para o host e o agente de quem fica.
func TestMergeMovesConversationsOfAgentAndHost(t *testing.T) {
	e, key, _ := derivEnv(t)
	admin := mkUser(t, e, "admin.conversa@x.com", domain.RoleMember)
	join(t, e, admin, domain.MemberAdmin)
	at := time.Now().UnixNano()
	writeBatch(t, e, key, derivBatch(t,
		derivRec{id: "k1", agent: "claude", host: "oute-server", at: at},
		derivRec{id: "k2", agent: "claude-velho", host: "oute-mac", at: at + 1}))
	insertConversation(t, e, "sess-velha", "Brave_Otter", entityID(t, e, "external_agent", "claude-velho"), entityID(t, e, "external_host", "oute-mac"))

	if _, err := e.svc.MergeExternalAgent(as(admin), domain.HumanActor, e.co.ID,
		entityID(t, e, "external_agent", "claude-velho"), entityID(t, e, "external_agent", "claude")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.MergeExternalHost(as(admin), domain.HumanActor, e.co.ID,
		entityID(t, e, "external_host", "oute-mac"), entityID(t, e, "external_host", "oute-server")); err != nil {
		t.Fatal(err)
	}
	if n := rowCount(t, e, `SELECT count(*) FROM external_conversation WHERE company_id = $1 AND agent_id = $2 AND host_id = $3`,
		e.co.ID, entityID(t, e, "external_agent", "claude"), entityID(t, e, "external_host", "oute-server")); n != 1 {
		t.Errorf("conversas no agente e no host que ficaram = %d, quero 1", n)
	}
}

// Juntar em um agente arquivado é recusado (409), e nada muda: o juntado segue ativo, com os seus apelidos.
func TestMergeIntoArchivedAgentIsRefusedAndChangesNothing(t *testing.T) {
	e, key, _ := derivEnv(t)
	admin := mkUser(t, e, "admin.recusa@x.com", domain.RoleMember)
	join(t, e, admin, domain.MemberAdmin)
	at := time.Now().UnixNano()
	writeBatch(t, e, key, derivBatch(t,
		derivRec{id: "r1", agent: "claude", host: "oute-server", at: at},
		derivRec{id: "r2", agent: "codex", host: "oute-server", at: at + 1}))
	into := entityID(t, e, "external_agent", "claude")
	from := entityID(t, e, "external_agent", "codex")
	archived := true
	if _, err := e.svc.UpdateExternalAgent(as(admin), domain.HumanActor, e.co.ID, into, service.ExternalAgentPatch{Archived: &archived}); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.MergeExternalAgent(as(admin), domain.HumanActor, e.co.ID, from, into)
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("junção em arquivado: erro = %v, quero ErrConflict", err)
	}
	if n := verbCount(t, e, "external_agent.merged"); n != 0 {
		t.Errorf("evento de junção numa recusa = %d, quero 0", n)
	}
	if n := rowCount(t, e, `SELECT count(*) FROM external_agent WHERE company_id = $1 AND id = $2 AND archived_at IS NULL`, e.co.ID, from); n != 1 {
		t.Error("o juntado foi arquivado mesmo com a junção recusada")
	}
}

// Juntar um agente já arquivado não apaga o horário em que ele foi arquivado.
func TestMergePreservesArchivedAtOfTheMergedAgent(t *testing.T) {
	e, key, _ := derivEnv(t)
	admin := mkUser(t, e, "admin.arquivado@x.com", domain.RoleMember)
	join(t, e, admin, domain.MemberAdmin)
	at := time.Now().UnixNano()
	writeBatch(t, e, key, derivBatch(t,
		derivRec{id: "p1", agent: "claude", host: "oute-server", at: at},
		derivRec{id: "p2", agent: "codex", host: "oute-server", at: at + 1}))
	from := entityID(t, e, "external_agent", "codex")
	if _, err := e.svc.Pool.Exec(context.Background(),
		`UPDATE external_agent SET archived_at = now() - interval '5 days' WHERE id = $1`, from); err != nil {
		t.Fatal(err)
	}
	var before time.Time
	if err := e.svc.Pool.QueryRow(context.Background(), `SELECT archived_at FROM external_agent WHERE id = $1`, from).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.MergeExternalAgent(as(admin), domain.HumanActor, e.co.ID, from, entityID(t, e, "external_agent", "claude")); err != nil {
		t.Fatal(err)
	}
	var after time.Time
	if err := e.svc.Pool.QueryRow(context.Background(), `SELECT archived_at FROM external_agent WHERE id = $1`, from).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if !after.Equal(before) {
		t.Errorf("archived_at mudou de %v para %v; quero o horário original", before, after)
	}
}

// Repetir a junção não muda nada: mesmo resultado, sem apelido duplicado nem novo arquivamento.
func TestMergeIsIdempotent(t *testing.T) {
	e, key, _ := derivEnv(t)
	admin := mkUser(t, e, "admin.idem@x.com", domain.RoleMember)
	join(t, e, admin, domain.MemberAdmin)
	at := time.Now().UnixNano()
	writeBatch(t, e, key, derivBatch(t,
		derivRec{id: "i1", agent: "claude", host: "oute-server", at: at},
		derivRec{id: "i2", agent: "codex", host: "oute-server", at: at + 1}))
	from := entityID(t, e, "external_agent", "codex")
	into := entityID(t, e, "external_agent", "claude")
	first, err := e.svc.MergeExternalAgent(as(admin), domain.HumanActor, e.co.ID, from, into)
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.svc.MergeExternalAgent(as(admin), domain.HumanActor, e.co.ID, from, into)
	if err != nil {
		t.Fatalf("segunda junção: %v", err)
	}
	if len(first.Aliases) != len(second.Aliases) {
		t.Errorf("apelidos na primeira = %v, na segunda = %v; quero os mesmos", first.Aliases, second.Aliases)
	}
	if second.ArchivedAt == nil && first.ArchivedAt != nil || second.ArchivedAt != nil && first.ArchivedAt == nil {
		t.Error("o que ficou ganhou arquivado")
	}
}

// Juntar entre empresas não acha o agente da outra: não encontrado, e nada muda.
func TestMergeAcrossCompaniesIsNotFound(t *testing.T) {
	e, key, _ := derivEnv(t)
	admin := mkUser(t, e, "admin.empresa@x.com", domain.RoleMember)
	join(t, e, admin, domain.MemberAdmin)
	writeBatch(t, e, key, derivBatch(t, derivRec{id: "x1", agent: "claude", host: "oute-server", at: time.Now().UnixNano()}))
	other, err := e.svc.CreateCompany(context.Background(), domain.HumanActor, service.CreateCompanyInput{Name: "Outra"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Pool.Exec(context.Background(),
		`INSERT INTO external_agent (id, company_id, name, created_at, updated_at) VALUES ($1, $2, 'alheio', now(), now())`,
		domain.NewID(), other.ID); err != nil {
		t.Fatal(err)
	}
	var outro domain.ID
	if err := e.svc.Pool.QueryRow(context.Background(), `SELECT id FROM external_agent WHERE name = 'alheio'`).Scan(&outro); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.MergeExternalAgent(as(admin), domain.HumanActor, e.co.ID, entityID(t, e, "external_agent", "claude"), outro)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("junção com agente de outra empresa: erro = %v, quero ErrNotFound", err)
	}
}

// Duas junções cruzadas ao mesmo tempo (A em B e B em A): só uma passa; a outra vê o que ficou arquivado e é recusada.
func TestConcurrentCrossMergesOnlyOneWins(t *testing.T) {
	e, key, _ := derivEnv(t)
	admin := mkUser(t, e, "admin.cruzada@x.com", domain.RoleMember)
	join(t, e, admin, domain.MemberAdmin)
	at := time.Now().UnixNano()
	writeBatch(t, e, key, derivBatch(t,
		derivRec{id: "z1", agent: "claude", host: "oute-server", at: at},
		derivRec{id: "z2", agent: "codex", host: "oute-server", at: at + 1}))
	a := entityID(t, e, "external_agent", "claude")
	b := entityID(t, e, "external_agent", "codex")

	var ready sync.WaitGroup
	ready.Add(2)
	start := make(chan struct{})
	errs := make(chan error, 2)
	for _, pair := range [][2]domain.ID{{a, b}, {b, a}} {
		from, into := pair[0], pair[1]
		go func() {
			ready.Done()
			<-start
			_, err := e.svc.MergeExternalAgent(as(admin), domain.HumanActor, e.co.ID, from, into)
			errs <- err
		}()
	}
	ready.Wait()
	close(start)
	wins, refused := 0, 0
	for i := 0; i < 2; i++ {
		err := <-errs
		switch {
		case err == nil:
			wins++
		case errors.Is(err, domain.ErrConflict):
			refused++
		default:
			t.Fatalf("junção cruzada: erro inesperado %v", err)
		}
	}
	if wins != 1 || refused != 1 {
		t.Errorf("junções que passaram = %d, recusadas = %d; quero 1 e 1", wins, refused)
	}
	if n := rowCount(t, e, `SELECT count(*) FROM external_agent WHERE company_id = $1 AND archived_at IS NULL`, e.co.ID); n != 1 {
		t.Errorf("agentes ativos depois das duas junções = %d, quero 1", n)
	}
}

// Renomear para um texto que já é apelido de outro agente é conflito (409), e o agente segue como estava.
func TestRenameToAnotherAgentsAliasConflicts(t *testing.T) {
	e, key, _ := derivEnv(t)
	admin := mkUser(t, e, "admin.apelido@x.com", domain.RoleMember)
	join(t, e, admin, domain.MemberAdmin)
	at := time.Now().UnixNano()
	writeBatch(t, e, key, derivBatch(t,
		derivRec{id: "q1", agent: "claude", host: "oute-server", at: at},
		derivRec{id: "q2", agent: "codex", host: "oute-server", at: at + 1}))
	claude := entityID(t, e, "external_agent", "claude")
	if _, err := e.svc.UpdateExternalAgent(as(admin), domain.HumanActor, e.co.ID, claude, service_patchName("gpt-helper")); err != nil {
		t.Fatal(err)
	}
	// agora "claude" é apelido de quem se chama gpt-helper
	_, err := e.svc.UpdateExternalAgent(as(admin), domain.HumanActor, e.co.ID, entityID(t, e, "external_agent", "codex"), service_patchName("claude"))
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("renomear para apelido de outro: erro = %v, quero ErrConflict", err)
	}
	if n := rowCount(t, e, `SELECT count(*) FROM external_agent WHERE company_id = $1 AND name = 'codex'`, e.co.ID); n != 1 {
		t.Error("o agente foi renomeado mesmo com o conflito")
	}
}
