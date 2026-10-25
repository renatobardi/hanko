package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Fatia 5 do cofre (ADR-0068 §6, issue #60): delegação de segredo pessoal por alvo e saída do humano.

const (
	sentinelPessoal  = "SENTINELA-token-pessoal-7c1d9e"
	sentinelPessoalB = "SENTINELA-token-do-bob-5a4b3c"
	refPessoal       = "tok=${secret:user/meu-token}"
)

// delegEnv é uma empresa com a persona Ana (tool hanko_secret_get em allow), Alice com o item pessoal `meu-token` e Bob.
type delegEnv struct {
	*toolsEnv
	alice, bob domain.User
	ac, bc     context.Context
	ana        domain.Persona
}

func newDelegEnv(t *testing.T) *delegEnv {
	t.Helper()
	e := newToolsEnv(t)
	alice := mkUser(t, e.env, "alice@x.com", domain.RoleMember)
	bob := mkUser(t, e.env, "bob@x.com", domain.RoleMember)
	join(t, e.env, alice, domain.MemberMember)
	join(t, e.env, bob, domain.MemberMember)
	d := &delegEnv{toolsEnv: e, alice: alice, bob: bob, ac: as(alice), bc: as(bob), ana: e.persona(t, "Ana", nil)}
	d.personal(d.ac, t, alice, "meu-token", sentinelPessoal)
	e.policy(t, e.toolPolicy(&d.ana, domain.PolicyTargetNative, nil, service.HankoSecretGetTool, domain.PolicyAllow))
	return d
}

// personal cria um segredo pessoal do usuário.
func (d *delegEnv) personal(ctx context.Context, t *testing.T, u domain.User, name, value string) {
	t.Helper()
	in := service.CreateSecretInput{Scope: domain.SecretScopeUser, Name: name, Value: value}
	if _, err := d.svc.CreateSecret(ctx, u.Actor(), d.co.ID, in); err != nil {
		t.Fatalf("criar %s de %s: %v", name, u.Email, err)
	}
}

// run abre uma task pedida por u e a run da Ana nela.
func (d *delegEnv) run(ctx context.Context, t *testing.T, u domain.User, title string) (domain.Run, service.TaskView) {
	t.Helper()
	tk, err := d.svc.CreateTask(ctx, u.Actor(), d.co.ID, service.CreateTaskInput{Title: title, PersonaID: &d.ana.ID})
	if err != nil {
		t.Fatalf("task de %s: %v", u.Email, err)
	}
	run, err := d.svc.CheckoutRun(context.Background(), wakeOf(d.ana, tk, domain.TriggerAssign))
	if err != nil {
		t.Fatalf("run de %s: %v", u.Email, err)
	}
	return run, tk
}

func (d *delegEnv) resolve(runID domain.ID, text string) (string, []string, error) {
	return d.svc.ResolveRunUserSecrets(context.Background(), d.co.ID, runID, text)
}

func (d *delegEnv) get(runID domain.ID, item string) (service.SecretGetResult, error) {
	return d.svc.SecretGet(context.Background(), d.co.ID, service.SecretGetInput{RunID: runID, Item: item})
}

func (d *delegEnv) delegate(ctx context.Context, u domain.User, kind domain.DelegationTarget, target domain.ID, ttl time.Duration) (service.DelegationView, error) {
	return d.svc.CreateSecretDelegation(ctx, u.Actor(), d.co.ID, "meu-token",
		service.CreateDelegationInput{TargetType: kind, TargetID: target, TTL: ttl})
}

// wantDenied confere que a resolução foi recusada sem vazar nenhum dos valores pessoais.
func wantDenied(t *testing.T, what, out string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: resolveu sem delegação válida (saída %q)", what, out)
	}
	for _, s := range []string{sentinelPessoal, sentinelPessoalB} {
		if strings.Contains(out, s) || strings.Contains(err.Error(), s) {
			t.Fatalf("%s: o valor pessoal vazou: %q %v", what, out, err)
		}
	}
}

// Critério 1: run sem delegação não resolve o item pessoal; run de outro humano também não.
func TestDelegacaoRunSemDelegacaoOuDeOutroHumanoNaoResolve(t *testing.T) {
	d := newDelegEnv(t)
	runA, taskA := d.run(d.ac, t, d.alice, "tarefa da Alice")
	runB, taskB := d.run(d.bc, t, d.bob, "tarefa do Bob")

	// sem delegação nenhuma, nem a run da dona resolve (texto e tool)
	out, _, err := d.resolve(runA.ID, refPessoal)
	wantDenied(t, "texto da dona sem delegação", out, err)
	res, err := d.get(runA.ID, "user/meu-token")
	wantDenied(t, "tool da dona sem delegação", res.Value, err)

	// o item pessoal de uma pessoa não é delegável por outra, nem a um trabalho que ela não pediu
	if _, err := d.delegate(d.bc, d.bob, domain.DelegationTask, taskB.ID, 0); !isNotFound(err) {
		t.Fatalf("Bob não tem meu-token: %v", err)
	}
	if _, err := d.delegate(d.ac, d.alice, domain.DelegationTask, taskB.ID, 0); err == nil || !strings.Contains(err.Error(), "pediu") {
		t.Fatalf("Alice delegando à task do Bob: %v", err)
	}

	// a delegação da Alice à task dela libera a run dela, mas não a run do Bob, mesmo com um item homônimo do Bob
	d.personal(d.bc, t, d.bob, "meu-token", sentinelPessoalB)
	if _, err := d.delegate(d.ac, d.alice, domain.DelegationTask, taskA.ID, 0); err != nil {
		t.Fatal(err)
	}
	out, vals, err := d.resolve(runA.ID, refPessoal)
	if err != nil || out != "tok="+sentinelPessoal || len(vals) != 1 || vals[0] != sentinelPessoal {
		t.Fatalf("run da dona com delegação: %q %v %v", out, vals, err)
	}
	out, _, err = d.resolve(runB.ID, refPessoal)
	wantDenied(t, "run de outro humano", out, err)
	res, err = d.get(runB.ID, "user/meu-token")
	wantDenied(t, "tool de outro humano", res.Value, err)

	// a tool da run delegada entrega o valor
	res, err = d.get(runA.ID, "user/meu-token")
	if err != nil || res.Decision != service.NativeAllow || res.Value != sentinelPessoal {
		t.Fatalf("tool da run delegada: %+v %v", res, err)
	}
	// referência da empresa dentro do texto da task não é tocada por este caminho
	out, _, err = d.resolve(runA.ID, "a=${secret:base} "+refPessoal)
	if err != nil || out != "a=${secret:base} tok="+sentinelPessoal {
		t.Fatalf("referência da empresa no mesmo texto: %q %v", out, err)
	}
}

func isNotFound(err error) bool { return errors.Is(err, domain.ErrNotFound) }

// Critério 2: a delegação deixa de valer depois do prazo e depois da revogação.
func TestDelegacaoPrazoERevogacao(t *testing.T) {
	d := newDelegEnv(t)
	runA, taskA := d.run(d.ac, t, d.alice, "tarefa da Alice")
	now := time.Now().UTC().Truncate(time.Millisecond) // o banco guarda microssegundos
	d.svc.Now = func() time.Time { return now }

	// prazo: padrão de 24 h, máximo de 7 dias
	v, err := d.delegate(d.ac, d.alice, domain.DelegationTask, taskA.ID, 0)
	if err != nil || !v.ExpiresAt.Equal(now.Add(24*time.Hour)) || !v.Active || v.Secret != "meu-token" {
		t.Fatalf("prazo padrão: %+v %v", v, err)
	}
	if _, err := d.delegate(d.ac, d.alice, domain.DelegationTask, taskA.ID, 8*24*time.Hour); err == nil {
		t.Fatal("prazo de 8 dias passa do máximo de 7")
	}
	// uma delegação curta (2 h) vale agora e deixa de valer 5 h depois; a de 24 h segue valendo
	if err := d.svc.RevokeSecretDelegation(d.ac, d.alice.Actor(), d.co.ID, v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.delegate(d.ac, d.alice, domain.DelegationTask, taskA.ID, 2*time.Hour); err != nil {
		t.Fatal(err)
	}
	if out, _, err := d.resolve(runA.ID, refPessoal); err != nil || !strings.Contains(out, sentinelPessoal) {
		t.Fatalf("dentro do prazo: %q %v", out, err)
	}
	now = now.Add(5 * time.Hour)
	out, _, err := d.resolve(runA.ID, refPessoal)
	wantDenied(t, "depois do prazo (texto)", out, err)
	res, err := d.get(runA.ID, "user/meu-token")
	wantDenied(t, "depois do prazo (tool)", res.Value, err)

	// revogação: vale até revogar, e quem revoga é só o dono
	v, err = d.delegate(d.ac, d.alice, domain.DelegationTask, taskA.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if out, _, err := d.resolve(runA.ID, refPessoal); err != nil || !strings.Contains(out, sentinelPessoal) {
		t.Fatalf("antes de revogar: %q %v", out, err)
	}
	if err := d.svc.RevokeSecretDelegation(d.bc, d.bob.Actor(), d.co.ID, v.ID); !isNotFound(err) {
		t.Fatalf("Bob revogando a delegação da Alice: %v", err)
	}
	if err := d.svc.RevokeSecretDelegation(d.ac, d.alice.Actor(), d.co.ID, v.ID); err != nil {
		t.Fatal(err)
	}
	out, _, err = d.resolve(runA.ID, refPessoal)
	wantDenied(t, "depois de revogar (texto)", out, err)
	res, err = d.get(runA.ID, "user/meu-token")
	wantDenied(t, "depois de revogar (tool)", res.Value, err)
	if err := d.svc.RevokeSecretDelegation(d.ac, d.alice.Actor(), d.co.ID, v.ID); !isNotFound(err) {
		t.Fatalf("revogar de novo: %v", err)
	}

	// o dono vê as delegações dele (nenhuma ativa) e só ele
	mine, err := d.svc.ListMyDelegations(d.ac, d.co.ID, 50)
	if err != nil || len(mine) != 3 {
		t.Fatalf("delegações da Alice: %d %v", len(mine), err)
	}
	for _, m := range mine {
		if m.Active {
			t.Fatalf("nenhuma delegação deve estar ativa: %+v", m)
		}
	}
	if theirs, err := d.svc.ListMyDelegations(d.bc, d.co.ID, 50); err != nil || len(theirs) != 0 {
		t.Fatalf("Bob não vê as da Alice: %d %v", len(theirs), err)
	}
}

// Delegação a uma run em curso vale só para ela; o relatório do dono mostra quando e em que run o item foi usado.
func TestDelegacaoPorRunERelatorioDoDono(t *testing.T) {
	d := newDelegEnv(t)
	run1, _ := d.run(d.ac, t, d.alice, "tarefa 1")
	run2, _ := d.run(d.ac, t, d.alice, "tarefa 2")
	runB, _ := d.run(d.bc, t, d.bob, "tarefa do Bob")

	if _, err := d.delegate(d.ac, d.alice, domain.DelegationRun, runB.ID, 0); err == nil {
		t.Fatal("run de outro humano não é delegável")
	}
	if _, err := d.delegate(d.ac, d.alice, domain.DelegationRun, run1.ID, 0); err != nil {
		t.Fatal(err)
	}
	if out, _, err := d.resolve(run1.ID, refPessoal); err != nil || !strings.Contains(out, sentinelPessoal) {
		t.Fatalf("run delegada: %q %v", out, err)
	}
	out, _, err := d.resolve(run2.ID, refPessoal)
	wantDenied(t, "outra run da mesma pessoa", out, err)
	if _, err := d.get(run1.ID, "user/meu-token"); err != nil {
		t.Fatal(err)
	}

	use, err := d.svc.ListUserSecretUsage(d.ac, d.co.ID, "meu-token", 50)
	if err != nil || len(use) != 2 {
		t.Fatalf("usos no relatório: %d %v", len(use), err)
	}
	vias := map[domain.SecretAccessVia]bool{}
	for _, u := range use {
		vias[u.Via] = true
		if u.RunID == nil || *u.RunID != run1.ID || u.Persona != d.ana.Slug || !u.Delegated || u.Field != "value" || u.AccessedAt.IsZero() {
			t.Fatalf("uso sem a run, a persona ou a delegação: %+v", u)
		}
	}
	if !vias[domain.SecretAccessDispatch] || !vias[domain.SecretAccessTool] {
		t.Fatalf("vias registradas: %v", vias)
	}
	// só o dono vê o relatório; o homônimo do Bob não mostra os usos da Alice
	d.personal(d.bc, t, d.bob, "meu-token", sentinelPessoalB)
	if theirs, err := d.svc.ListUserSecretUsage(d.bc, d.co.ID, "meu-token", 50); err != nil || len(theirs) != 0 {
		t.Fatalf("Bob vê os usos da Alice: %d %v", len(theirs), err)
	}
	// o registro não leva valor
	var leaked int
	if err := d.svc.Pool.QueryRow(context.Background(),
		`SELECT (SELECT count(*) FROM activity_events WHERE data::text LIKE '%'||$1||'%')
		      + (SELECT count(*) FROM secret_access_log WHERE field_name LIKE '%'||$1||'%')`, sentinelPessoal).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatalf("valor pessoal no registro de fatos ou de uso: %d %v", leaked, err)
	}
}

// Critério 3: a saída apaga os itens pessoais, lista as delegações ativas e grava o fato, sem nome nem valor.
func TestSaidaDoHumanoApagaItensEListaDelegacoes(t *testing.T) {
	d := newDelegEnv(t)
	d.personal(d.ac, t, d.alice, "outro-token", "SENTINELA-outro-1f2e3d")
	_, taskA := d.run(d.ac, t, d.alice, "tarefa 1")
	_, taskA2 := d.run(d.ac, t, d.alice, "tarefa 2")
	live, err := d.delegate(d.ac, d.alice, domain.DelegationTask, taskA.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	gone, err := d.delegate(d.ac, d.alice, domain.DelegationTask, taskA2.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.svc.RevokeSecretDelegation(d.ac, d.alice.Actor(), d.co.ID, gone.ID); err != nil {
		t.Fatal(err)
	}
	d.personal(d.bc, t, d.bob, "meu-token", sentinelPessoalB)

	rep, err := d.svc.RemoveMemberReport(d.ac, d.alice.Actor(), d.co.ID, d.alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rep.UserSecrets != 2 || len(rep.Delegations) != 1 || rep.Delegations[0].ID != live.ID || rep.Delegations[0].TargetID != taskA.ID {
		t.Fatalf("relatório da saída: %+v", rep)
	}

	ctx := context.Background()
	var secrets, delegs int
	if err := d.svc.Pool.QueryRow(ctx, `SELECT count(*) FROM secrets WHERE company_id=$1 AND scope='user' AND owner_user_id=$2`,
		d.co.ID, d.alice.ID).Scan(&secrets); err != nil || secrets != 0 {
		t.Fatalf("segredos pessoais que sobraram: %d %v", secrets, err)
	}
	if err := d.svc.Pool.QueryRow(ctx, `SELECT count(*) FROM secret_delegations WHERE company_id=$1 AND owner_user_id=$2`,
		d.co.ID, d.alice.ID).Scan(&delegs); err != nil || delegs != 0 {
		t.Fatalf("delegações que sobraram: %d %v", delegs, err)
	}
	// o item do Bob fica
	if err := d.svc.Pool.QueryRow(ctx, `SELECT count(*) FROM secrets WHERE company_id=$1 AND scope='user' AND owner_user_id=$2`,
		d.co.ID, d.bob.ID).Scan(&secrets); err != nil || secrets != 1 {
		t.Fatalf("o item do Bob deve ficar: %d %v", secrets, err)
	}

	var items, active string
	var data string
	if err := d.svc.Pool.QueryRow(ctx, `SELECT data->>'items', data->>'active_delegations', data::text FROM activity_events
		WHERE company_id=$1 AND verb='secret.user_purged' AND subject_id=$2`, d.co.ID, d.alice.ID).Scan(&items, &active, &data); err != nil {
		t.Fatalf("fato secret.user_purged: %v", err)
	}
	if items != "2" || active != "1" || !strings.Contains(data, live.ID.String()) {
		t.Fatalf("fato da saída: items=%s ativas=%s %s", items, active, data)
	}
	for _, s := range []string{"meu-token", "outro-token", sentinelPessoal} {
		if strings.Contains(data, s) {
			t.Fatalf("o fato leva nome ou valor (%q): %s", s, data)
		}
	}
	var removed int
	if err := d.svc.Pool.QueryRow(ctx, `SELECT count(*) FROM activity_events WHERE company_id=$1 AND verb='member.removed' AND subject_id=$2`,
		d.co.ID, d.alice.ID).Scan(&removed); err != nil || removed != 1 {
		t.Fatalf("fato member.removed: %d %v", removed, err)
	}

	// quem sai sem item pessoal nem delegação não gera o fato de purga
	if _, err := d.svc.RemoveMemberReport(d.ac, d.alice.Actor(), d.co.ID, d.alice.ID); err == nil {
		t.Fatal("a Alice já saiu")
	}
	d2 := mkUser(t, d.env, "carol@x.com", domain.RoleMember)
	join(t, d.env, d2, domain.MemberMember)
	rep, err = d.svc.RemoveMemberReport(as(d2), d2.Actor(), d.co.ID, d2.ID)
	if err != nil || rep.UserSecrets != 0 || len(rep.Delegations) != 0 {
		t.Fatalf("saída sem item pessoal: %+v %v", rep, err)
	}
	var purged int
	if err := d.svc.Pool.QueryRow(ctx, `SELECT count(*) FROM activity_events WHERE company_id=$1 AND verb='secret.user_purged' AND subject_id=$2`,
		d.co.ID, d2.ID).Scan(&purged); err != nil || purged != 0 {
		t.Fatalf("fato de purga sem nada a purgar: %d %v", purged, err)
	}
}

// Alvo conversa (ADR-0068 §6): a delegação feita na conversa da dona libera a run dela; arquivada, a conversa fecha
// o alvo e a delegação para de valer até a reabertura.
func TestDelegacaoAlvoConversa(t *testing.T) {
	d := newDelegEnv(t)
	cv, err := d.svc.CreateConversation(d.ac, d.alice.Actor(), d.co.ID, service.CreateConversationInput{PersonaID: d.ana.ID, Message: "oi"})
	if err != nil {
		t.Fatal(err)
	}
	runC := lastExec(d.env) // a run que a mensagem abriu

	out, _, err := d.resolve(runC, refPessoal)
	wantDenied(t, "conversa sem delegação", out, err)
	if _, err := d.delegate(d.bc, d.bob, domain.DelegationConversation, cv.ID, 0); err == nil {
		t.Fatal("Bob delegou a conversa da Alice")
	}
	if _, err := d.delegate(d.ac, d.alice, domain.DelegationConversation, cv.ID, 0); err != nil {
		t.Fatal(err)
	}
	if out, vals, err := d.resolve(runC, refPessoal); err != nil || out != "tok="+sentinelPessoal || len(vals) != 1 {
		t.Fatalf("conversa delegada: %q %v %v", out, vals, err)
	}

	if _, err := d.svc.SetConversationStatus(d.ac, d.alice.Actor(), d.co.ID, cv.ID, domain.ConversationArchived); err != nil {
		t.Fatal(err)
	}
	out, _, err = d.resolve(runC, refPessoal)
	wantDenied(t, "conversa arquivada (texto)", out, err)
	res, err := d.get(runC, "user/meu-token")
	wantDenied(t, "conversa arquivada (tool)", res.Value, err)

	if _, err := d.svc.SetConversationStatus(d.ac, d.alice.Actor(), d.co.ID, cv.ID, domain.ConversationOpen); err != nil {
		t.Fatal(err)
	}
	if out, _, err := d.resolve(runC, refPessoal); err != nil || out != "tok="+sentinelPessoal {
		t.Fatalf("conversa reaberta: %q %v", out, err)
	}
}

// Alvo task (ADR-0068 §6.3): fechada (cancelada), a task fecha o alvo e a delegação para de valer; reaberta, volta.
func TestDelegacaoSoEnquantoATaskEstaAberta(t *testing.T) {
	d := newDelegEnv(t)
	runA, taskA := d.run(d.ac, t, d.alice, "tarefa da Alice")
	if _, err := d.delegate(d.ac, d.alice, domain.DelegationTask, taskA.ID, 0); err != nil {
		t.Fatal(err)
	}
	if out, _, err := d.resolve(runA.ID, refPessoal); err != nil || out != "tok="+sentinelPessoal {
		t.Fatalf("task aberta: %q %v", out, err)
	}

	cancelled := domain.TaskCancelled
	if _, err := d.svc.UpdateTask(d.ac, d.alice.Actor(), d.co.ID, taskA.ID, service.UpdateTaskInput{Status: &cancelled}); err != nil {
		t.Fatal(err)
	}
	out, _, err := d.resolve(runA.ID, refPessoal)
	wantDenied(t, "task cancelada (texto)", out, err)
	res, err := d.get(runA.ID, "user/meu-token")
	wantDenied(t, "task cancelada (tool)", res.Value, err)

	todo := domain.TaskTodo
	if _, err := d.svc.UpdateTask(d.ac, d.alice.Actor(), d.co.ID, taskA.ID, service.UpdateTaskInput{Status: &todo}); err != nil {
		t.Fatal(err)
	}
	// fechar a task cancela a run dela; reabrir não a reativa. A run nova da task reaberta resolve de novo.
	novaRun, err := d.svc.CheckoutRun(context.Background(), wakeOf(d.ana, taskA, domain.TriggerAssign))
	if err != nil {
		t.Fatalf("run nova da task reaberta: %v", err)
	}
	if out, _, err := d.resolve(novaRun.ID, refPessoal); err != nil || out != "tok="+sentinelPessoal {
		t.Fatalf("task reaberta, run nova: %q %v", out, err)
	}
}

// Uso do segredo da empresa: o admin vê; o membro só com `view` ou `manage` numa coleção que contém o segredo.
func TestUsoDoSegredoDaEmpresaSoParaQuemVe(t *testing.T) {
	d := newDelegEnv(t)
	bg := context.Background()
	if _, err := d.svc.CreateSecret(bg, domain.HumanActor, d.co.ID, service.CreateSecretInput{Scope: domain.SecretScopeCompany,
		Name: "base", Value: "SENTINELA-base-6b7a8c"}); err != nil {
		t.Fatal(err)
	}
	if uses, err := d.svc.ListSecretUsage(bg, d.co.ID, "base", 50); err != nil || len(uses) != 0 {
		t.Fatalf("admin: %d usos %v", len(uses), err)
	}
	if _, err := d.svc.ListSecretUsage(d.ac, d.co.ID, "base", 50); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("membro sem coleção: %v", err)
	}
	if _, err := d.svc.CreateCollectionGrant(bg, domain.HumanActor, d.co.ID, domain.DefaultCollectionName,
		service.GrantInput{SubjectType: domain.CollectionSubjectUser, Subject: d.alice.ID.String(), Permission: domain.CollectionView}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.svc.ListSecretUsage(d.ac, d.co.ID, "base", 50); err != nil {
		t.Fatalf("membro com view na coleção padrão: %v", err)
	}
	if _, err := d.svc.ListSecretUsage(d.bc, d.co.ID, "base", 50); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("Bob sem concessão: %v", err)
	}
}
