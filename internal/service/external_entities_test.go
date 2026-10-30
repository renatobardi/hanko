package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// entityID acha o id de um agente ou host pelo nome na empresa.
func entityID(t *testing.T, e *env, table, name string) domain.ID {
	t.Helper()
	var id domain.ID
	if err := e.svc.Pool.QueryRow(context.Background(), `SELECT id FROM `+table+` WHERE company_id = $1 AND name = $2`, e.co.ID, name).Scan(&id); err != nil {
		t.Fatalf("id de %s %q: %v", table, name, err)
	}
	return id
}

func verbCount(t *testing.T, e *env, verb string) int {
	t.Helper()
	var n int
	if err := e.svc.Pool.QueryRow(context.Background(), `SELECT count(*) FROM activity_events WHERE company_id = $1 AND verb = $2`, e.co.ID, verb).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Renomear guarda o nome antigo como apelido: fato com o nome antigo cai no mesmo agente, e o evento sai.
func TestRenameKeepsTheOldNameAsAlias(t *testing.T) {
	e, key, _ := derivEnv(t)
	admin := mkUser(t, e, "admin.renomeia@x.com", domain.RoleMember)
	join(t, e, admin, domain.MemberAdmin)
	writeBatch(t, e, key, derivBatch(t, derivRec{id: "n1", agent: "claude", host: "oute-server", at: time.Now().UnixNano()}))
	id := entityID(t, e, "external_agent", "claude")

	v, err := e.svc.UpdateExternalAgent(as(admin), domain.HumanActor, e.co.ID, id, service_patchName("claude-code"))
	if err != nil {
		t.Fatal(err)
	}
	if v.Name != "claude-code" {
		t.Fatalf("nome = %q, quero claude-code", v.Name)
	}
	if !hasAlias(v.Aliases, "claude") {
		t.Errorf("apelidos = %v, quero o nome antigo claude", v.Aliases)
	}
	if n := verbCount(t, e, "external_agent.renamed"); n != 1 {
		t.Errorf("eventos de renomear = %d, quero 1", n)
	}
	writeBatch(t, e, key, derivBatch(t, derivRec{id: "n2", agent: "claude", host: "oute-server", at: time.Now().UnixNano() + 1}))
	if n := rowCount(t, e, `SELECT count(*) FROM external_agent WHERE company_id = $1`, e.co.ID); n != 1 {
		t.Errorf("agentes depois do nome antigo = %d, quero 1 (o fato cai no mesmo agente)", n)
	}
}

// Renomear para o nome de outro agente é conflito, e nada muda.
func TestRenameToAnExistingNameConflicts(t *testing.T) {
	e, key, _ := derivEnv(t)
	admin := mkUser(t, e, "admin.conflito@x.com", domain.RoleMember)
	join(t, e, admin, domain.MemberAdmin)
	at := time.Now().UnixNano()
	writeBatch(t, e, key, derivBatch(t,
		derivRec{id: "c1", agent: "claude", host: "oute-server", at: at},
		derivRec{id: "c2", agent: "codex", host: "oute-server", at: at + 1}))
	_, err := e.svc.UpdateExternalAgent(as(admin), domain.HumanActor, e.co.ID, entityID(t, e, "external_agent", "codex"), service_patchName("claude"))
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("erro = %v, quero ErrConflict", err)
	}
}

// Arquivar tira o agente da lista padrão; desarquivar devolve. Cada passo gera evento.
func TestArchiveHidesAgentAndUnarchiveBringsItBack(t *testing.T) {
	e, key, _ := derivEnv(t)
	admin := mkUser(t, e, "admin.arquiva@x.com", domain.RoleMember)
	join(t, e, admin, domain.MemberAdmin)
	writeBatch(t, e, key, derivBatch(t, derivRec{id: "a1", agent: "gemini", host: "oute-server", at: time.Now().UnixNano()}))
	id := entityID(t, e, "external_agent", "gemini")
	archived := true
	if _, err := e.svc.UpdateExternalAgent(as(admin), domain.HumanActor, e.co.ID, id, service.ExternalAgentPatch{Archived: &archived}); err != nil {
		t.Fatal(err)
	}
	if list, _ := e.svc.ListExternalAgents(as(admin), e.co.ID, false, 50, 0); listHasAgent(list, "gemini") {
		t.Error("agente arquivado aparece na lista padrão")
	}
	if list, _ := e.svc.ListExternalAgents(as(admin), e.co.ID, true, 50, 0); !listHasAgent(list, "gemini") {
		t.Error("agente arquivado some com include_archived")
	}
	back := false
	if _, err := e.svc.UpdateExternalAgent(as(admin), domain.HumanActor, e.co.ID, id, service.ExternalAgentPatch{Archived: &back}); err != nil {
		t.Fatal(err)
	}
	if list, _ := e.svc.ListExternalAgents(as(admin), e.co.ID, false, 50, 0); !listHasAgent(list, "gemini") {
		t.Error("agente desarquivado não volta à lista")
	}
	if verbCount(t, e, "external_agent.archived") != 1 || verbCount(t, e, "external_agent.unarchived") != 1 {
		t.Error("faltam eventos de arquivar ou desarquivar")
	}
}

// Quem só vê não altera nem junta agente ou host.
func TestViewerCannotChangeExternalEntities(t *testing.T) {
	e, key, _ := derivEnv(t)
	viewer := mkUser(t, e, "visualiza@x.com", domain.RoleMember)
	join(t, e, viewer, domain.MemberViewer)
	at := time.Now().UnixNano()
	writeBatch(t, e, key, derivBatch(t,
		derivRec{id: "v1", agent: "claude", host: "oute-server", at: at},
		derivRec{id: "v2", agent: "codex", host: "oute-mac", at: at + 1}))
	if _, err := e.svc.UpdateExternalAgent(as(viewer), domain.HumanActor, e.co.ID, entityID(t, e, "external_agent", "claude"), service_patchName("x")); !errors.Is(err, domain.ErrNotAllowed) {
		t.Errorf("renomear por visualizador: erro = %v, quero ErrNotAllowed", err)
	}
	if _, err := e.svc.MergeExternalHost(as(viewer), domain.HumanActor, e.co.ID, entityID(t, e, "external_host", "oute-mac"), entityID(t, e, "external_host", "oute-server")); !errors.Is(err, domain.ErrNotAllowed) {
		t.Errorf("juntar host por visualizador: erro = %v, quero ErrNotAllowed", err)
	}
}

// Juntar agente move apelidos e sessões para o que fica, arquiva o juntado, e o nome dele vira apelido do que fica.
func TestMergeAgentMovesAliasesAndSessions(t *testing.T) {
	e, key, _ := derivEnv(t)
	admin := mkUser(t, e, "admin.junta@x.com", domain.RoleMember)
	join(t, e, admin, domain.MemberAdmin)
	at := time.Now().UnixNano()
	writeBatch(t, e, key, derivBatch(t,
		derivRec{id: "j1", agent: "claude", host: "oute-server", at: at},
		derivRec{id: "j2", agent: "claude-velho", task: "tarefa-velha", host: "oute-server", at: at + 1}))
	into := entityID(t, e, "external_agent", "claude")
	from := entityID(t, e, "external_agent", "claude-velho")

	v, err := e.svc.MergeExternalAgent(as(admin), domain.HumanActor, e.co.ID, from, into)
	if err != nil {
		t.Fatal(err)
	}
	if !hasAlias(v.Aliases, "claude-velho") {
		t.Errorf("apelidos do que fica = %v, quero claude-velho", v.Aliases)
	}
	if n := rowCount(t, e, `SELECT count(*) FROM external_session WHERE company_id = $1 AND agent_id = $2`, e.co.ID, into); n != 1 {
		t.Errorf("sessões do agente que fica = %d, quero 1 (a sessão do juntado passou)", n)
	}
	if n := rowCount(t, e, `SELECT count(*) FROM external_agent WHERE company_id = $1 AND archived_at IS NOT NULL`, e.co.ID); n != 1 {
		t.Errorf("agentes arquivados = %d, quero 1 (o juntado)", n)
	}
	// fato com o nome antigo depois da junção cai no agente que ficou
	writeBatch(t, e, key, derivBatch(t, derivRec{id: "j3", agent: "claude-velho", host: "oute-server", at: at + 2}))
	if n := rowCount(t, e, `SELECT count(*) FROM external_agent WHERE company_id = $1`, e.co.ID); n != 2 {
		t.Errorf("agentes depois de fato com o nome juntado = %d, quero 2 (nenhum novo)", n)
	}
	if n := verbCount(t, e, "external_agent.merged"); n != 1 {
		t.Errorf("eventos de junção = %d, quero 1", n)
	}
}

// Juntar host mantém o sempre ligado de qualquer um dos dois e a hora de fato mais nova.
func TestMergeHostKeepsAlwaysOnAndTheNewestSeen(t *testing.T) {
	e, key, _ := derivEnv(t)
	admin := mkUser(t, e, "admin.host@x.com", domain.RoleMember)
	join(t, e, admin, domain.MemberAdmin)
	base := time.Now().Truncate(time.Second).Add(time.Hour)
	writeBatch(t, e, key, derivBatch(t,
		derivRec{id: "h1", agent: "claude", host: "oute-a", at: base.UnixNano()},
		derivRec{id: "h2", agent: "claude", host: "oute-b", at: base.Add(time.Minute).UnixNano()}))
	always := true
	if _, err := e.svc.UpdateExternalHost(as(admin), domain.HumanActor, e.co.ID, entityID(t, e, "external_host", "oute-a"), service.ExternalHostPatch{AlwaysOn: &always}); err != nil {
		t.Fatal(err)
	}
	into := entityID(t, e, "external_host", "oute-b")
	v, err := e.svc.MergeExternalHost(as(admin), domain.HumanActor, e.co.ID, entityID(t, e, "external_host", "oute-a"), into)
	if err != nil {
		t.Fatal(err)
	}
	if !v.AlwaysOn {
		t.Error("o host que fica perdeu o sempre ligado do juntado")
	}
	if v.LastSeenAt == nil || !v.LastSeenAt.Equal(base.Add(time.Minute)) {
		t.Errorf("última hora = %v, quero a do fato mais novo %v", v.LastSeenAt, base.Add(time.Minute))
	}
}

// Juntar um agente nele mesmo é recusado.
func TestMergeIntoItselfIsRefused(t *testing.T) {
	e, key, _ := derivEnv(t)
	admin := mkUser(t, e, "admin.mesmo@x.com", domain.RoleMember)
	join(t, e, admin, domain.MemberAdmin)
	writeBatch(t, e, key, derivBatch(t, derivRec{id: "m1", agent: "claude", host: "oute-server", at: time.Now().UnixNano()}))
	id := entityID(t, e, "external_agent", "claude")
	if _, err := e.svc.MergeExternalAgent(as(admin), domain.HumanActor, e.co.ID, id, id); err == nil {
		t.Error("juntar o agente nele mesmo foi aceito")
	}
}

func service_patchName(name string) service.ExternalAgentPatch {
	return service.ExternalAgentPatch{Name: &name}
}

func hasAlias(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func listHasAgent(list []service.ExternalAgentView, name string) bool {
	for _, a := range list {
		if a.Name == name {
			return true
		}
	}
	return false
}

func rowCount(t *testing.T, e *env, query string, args ...any) int {
	t.Helper()
	var n int
	if err := e.svc.Pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}
