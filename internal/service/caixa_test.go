package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Caixa por pessoa (ADR-0042 §A.1–A.4): lido e arquivado são de cada usuário; arquivar não fecha o que está na origem.

// caixaEnv monta uma empresa com Ana e Bruno como membros.
func caixaEnv(t *testing.T) (e *env, ana, bruno domain.User) {
	t.Helper()
	e = newEnv(t)
	ana = mkUser(t, e, "ana.souza@x.com", domain.RoleMember)
	bruno = mkUser(t, e, "bruno@x.com", domain.RoleMember)
	join(t, e, ana, domain.MemberMember)
	join(t, e, bruno, domain.MemberMember)
	return e, ana, bruno
}

func gateRef(g service.GateView) service.InboxRef {
	return service.InboxRef{Kind: domain.InboxGate, ID: g.ID}
}

func TestInboxArchiveIsPerUserAndDoesNotCloseTheTask(t *testing.T) {
	e, ana, bruno := caixaEnv(t)
	bg := context.Background()
	tk := e.task(t, service.CreateTaskInput{Title: "revisar contrato"})
	g, err := e.svc.RequestGate(bg, domain.HumanActor, e.co.ID, service.RequestGateInput{Title: "aprovar?", TaskID: &tk.ID})
	if err != nil {
		t.Fatal(err)
	}

	for _, u := range []domain.User{ana, bruno} {
		in, err := e.svc.Inbox(as(u), e.co.ID)
		if err != nil || in.Count() != 1 || in.Unread != 1 || !in.Personal {
			t.Fatalf("%s vê o gate como não lido: count=%d unread=%d %v", u.Email, in.Count(), in.Unread, err)
		}
	}

	// Ana lê: sai de "Não lidos" mas continua na Caixa; Bruno não muda
	if n, err := e.svc.SetInboxState(as(ana), ana.Actor(), e.co.ID, domain.InboxRead, []service.InboxRef{gateRef(g)}); err != nil || n != 1 {
		t.Fatalf("ler: %d %v", n, err)
	}
	unread, _ := e.svc.InboxView(as(ana), e.co.ID, domain.InboxViewUnread)
	if unread.Count() != 0 || unread.Unread != 0 {
		t.Fatalf("Ana sem não lidos: %+v", unread.Count())
	}
	all, _ := e.svc.Inbox(as(ana), e.co.ID)
	if all.Count() != 1 || all.StateOf(domain.InboxGate, g.ID).Read != true {
		t.Fatalf("lido continua na Caixa: %d", all.Count())
	}
	if b, _ := e.svc.InboxView(as(bruno), e.co.ID, domain.InboxViewUnread); b.Count() != 1 {
		t.Fatal("o estado de Ana não pode afetar Bruno")
	}

	// Ana arquiva: limpa a Caixa dela, não a de Bruno, e não mexe no gate nem na task
	if _, err := e.svc.SetInboxState(as(ana), ana.Actor(), e.co.ID, domain.InboxArchive, []service.InboxRef{gateRef(g)}); err != nil {
		t.Fatal(err)
	}
	if in, _ := e.svc.Inbox(as(ana), e.co.ID); in.Count() != 0 || in.ArchivedCount != 1 {
		t.Fatalf("Caixa de Ana limpa: count=%d arquivados=%d", in.Count(), in.ArchivedCount)
	}
	if arch, _ := e.svc.InboxView(as(ana), e.co.ID, domain.InboxViewArchived); len(arch.Gates) != 1 {
		t.Fatal("o gate arquivado aparece na visão Arquivados")
	}
	if in, _ := e.svc.Inbox(as(bruno), e.co.ID); in.Count() != 1 {
		t.Fatal("Bruno ainda vê o gate")
	}
	cur, err := e.svc.GetGate(bg, e.co.ID, g.ID)
	if err != nil || cur.Status != domain.GatePending {
		t.Fatalf("o gate segue pendente: %+v %v", cur.Status, err)
	}
	if tv, _ := e.svc.GetTask(bg, e.co.ID, tk.ID.String()); tv.Status.Open() == false {
		t.Fatalf("arquivar não fecha a task: %s", tv.Status)
	}
	// o fato de arquivar fica na trilha; ler não
	acts, _ := e.svc.ListActivity(bg, e.co.ID, service.ActivityFilter{SubjectType: "inbox", Limit: 50})
	if len(acts) != 1 || acts[0].Verb != "inbox.archived" {
		t.Fatalf("atividade: %+v", acts)
	}

	// desarquivar devolve à Caixa
	if _, err := e.svc.SetInboxState(as(ana), ana.Actor(), e.co.ID, domain.InboxUnarchive, []service.InboxRef{gateRef(g)}); err != nil {
		t.Fatal(err)
	}
	if in, _ := e.svc.Inbox(as(ana), e.co.ID); in.Count() != 1 || in.ArchivedCount != 0 {
		t.Fatalf("desarquivado: %d", in.Count())
	}
	// marcar como não lido
	if _, err := e.svc.SetInboxState(as(ana), ana.Actor(), e.co.ID, domain.InboxUnread, []service.InboxRef{gateRef(g)}); err != nil {
		t.Fatal(err)
	}
	if n, _ := e.svc.InboxUnread(as(ana), e.co.ID); n != 1 {
		t.Fatalf("selo = %d", n)
	}
}

// Reaparece se muda: uma task bloqueada que a pessoa arquivou volta como nova quando ela muda.
func TestInboxItemReappearsWhenItChanges(t *testing.T) {
	e, ana, _ := caixaEnv(t)
	bg := context.Background()
	tk := e.task(t, service.CreateTaskInput{Title: "travada"})
	blocked := domain.TaskBlocked
	if _, err := e.svc.UpdateTask(bg, domain.HumanActor, e.co.ID, tk.ID, service.UpdateTaskInput{Status: &blocked}); err != nil {
		t.Fatal(err)
	}
	ref := service.InboxRef{Kind: domain.InboxTaskBlocked, ID: tk.ID}
	in, _ := e.svc.Inbox(as(ana), e.co.ID)
	if len(in.BlockedTasks) != 1 {
		t.Fatalf("bloqueada na Caixa: %d", len(in.BlockedTasks))
	}
	if _, err := e.svc.SetInboxState(as(ana), ana.Actor(), e.co.ID, domain.InboxArchive, []service.InboxRef{ref}); err != nil {
		t.Fatal(err)
	}
	if in, _ = e.svc.Inbox(as(ana), e.co.ID); len(in.BlockedTasks) != 0 {
		t.Fatal("arquivada some")
	}
	// a task muda (novo título → updated_at novo): o item volta não lido e não arquivado
	time.Sleep(5 * time.Millisecond)
	title := "travada de novo"
	if _, err := e.svc.UpdateTask(bg, domain.HumanActor, e.co.ID, tk.ID, service.UpdateTaskInput{Title: &title}); err != nil {
		t.Fatal(err)
	}
	in, _ = e.svc.Inbox(as(ana), e.co.ID)
	if len(in.BlockedTasks) != 1 || in.StateOf(domain.InboxTaskBlocked, tk.ID).Read || in.Unread != 1 {
		t.Fatalf("deveria reaparecer como novo: %d %+v unread=%d", len(in.BlockedTasks), in.StateOf(domain.InboxTaskBlocked, tk.ID), in.Unread)
	}
}

func TestInboxReadAllAndValidation(t *testing.T) {
	e, ana, _ := caixaEnv(t)
	bg := context.Background()
	for i := 0; i < 3; i++ {
		tk := e.task(t, service.CreateTaskInput{Title: "t"})
		if _, err := e.svc.RequestGate(bg, domain.HumanActor, e.co.ID, service.RequestGateInput{Title: "g", TaskID: &tk.ID}); err != nil {
			t.Fatal(err)
		}
	}
	n, err := e.svc.SetInboxState(as(ana), ana.Actor(), e.co.ID, domain.InboxReadAll, nil)
	if err != nil || n != 3 {
		t.Fatalf("read_all: %d %v", n, err)
	}
	if in, _ := e.svc.Inbox(as(ana), e.co.ID); in.Unread != 0 || in.Count() != 3 {
		t.Fatalf("tudo lido continua na Caixa: unread=%d count=%d", in.Unread, in.Count())
	}
	acts, _ := e.svc.ListActivity(bg, e.co.ID, service.ActivityFilter{SubjectType: "company", Limit: 50})
	found := false
	for _, a := range acts {
		found = found || a.Verb == "inbox.read_all"
	}
	if !found {
		t.Fatal("inbox.read_all deveria constar")
	}

	ghost := service.InboxRef{Kind: domain.InboxGate, ID: domain.NewID()}
	_, err = e.svc.SetInboxState(as(ana), ana.Actor(), e.co.ID, domain.InboxRead, []service.InboxRef{ghost})
	wantErr(t, err, domain.ErrNotFound)
	_, err = e.svc.SetInboxState(as(ana), ana.Actor(), e.co.ID, domain.InboxRead, nil)
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.SetInboxState(as(ana), ana.Actor(), e.co.ID, "dance", []service.InboxRef{ghost})
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.SetInboxState(as(ana), ana.Actor(), e.co.ID, domain.InboxRead, []service.InboxRef{{Kind: "x", ID: domain.NewID()}})
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.SetInboxState(as(ana), domain.PersonaActor(domain.NewID()), e.co.ID, domain.InboxReadAll, nil)
	wantErr(t, err, domain.ErrForbidden)
	_, err = e.svc.InboxView(as(ana), e.co.ID, "nada")
	wantErr(t, err, domain.ErrInvalid)

	// sem pessoa autenticada (modo trusted): a Caixa mostra tudo como não lido e marcar é recusado
	in, err := e.svc.Inbox(bg, e.co.ID)
	if err != nil || in.Personal || in.Count() != 3 || in.Unread != 3 {
		t.Fatalf("trusted: personal=%v count=%d unread=%d %v", in.Personal, in.Count(), in.Unread, err)
	}
	_, err = e.svc.SetInboxState(bg, domain.HumanActor, e.co.ID, domain.InboxReadAll, nil)
	wantErr(t, err, domain.ErrInvalid)

	// quem não é membro não enxerga a empresa
	stranger := mkUser(t, e, "estranho@x.com", domain.RoleMember)
	_, err = e.svc.Inbox(as(stranger), e.co.ID)
	wantErr(t, err, domain.ErrNotFound)

	items, err := e.svc.ListInboxItems(as(ana), e.co.ID, domain.InboxViewAll)
	if err != nil || len(items) != 3 || !items[0].Read {
		t.Fatalf("itens: %+v %v", items, err)
	}
	for _, in := range []string{"gate:" + items[0].ID.String()} {
		ref, err := service.ParseInboxRef(in)
		if err != nil || ref.Kind != domain.InboxGate || ref.ID != items[0].ID {
			t.Fatalf("ParseInboxRef: %+v %v", ref, err)
		}
	}
	for _, bad := range []string{"", "gate", "gate:xyz", "nada:" + items[0].ID.String()} {
		if _, err := service.ParseInboxRef(bad); err == nil {
			t.Errorf("ParseInboxRef(%q) deveria falhar", bad)
		}
	}
}

// Viewer também tem Caixa pessoal (é estado dele, não escreve na empresa).
func TestInboxViewerCanMarkOwnState(t *testing.T) {
	e := newEnv(t)
	v := mkUser(t, e, "leitor@x.com", domain.RoleMember)
	join(t, e, v, domain.MemberViewer)
	tk := e.task(t, service.CreateTaskInput{Title: "t"})
	g, _ := e.svc.RequestGate(context.Background(), domain.HumanActor, e.co.ID, service.RequestGateInput{Title: "g", TaskID: &tk.ID})
	if _, err := e.svc.SetInboxState(as(v), v.Actor(), e.co.ID, domain.InboxArchive, []service.InboxRef{gateRef(g)}); err != nil {
		t.Fatalf("viewer arquiva o próprio estado: %v", err)
	}
}

// "Vence em" por tipo (ADR-0083 item 6): o alerta de agente externo arquivado volta depois do prazo do tipo, contado desde a
// marca; a empresa pode trocar o prazo, e "sem prazo" (0) mantém o arquivamento. A marca não é apagada.
func TestInboxAlertDueReturnsAfterTheTypeDeadline(t *testing.T) {
	e, ana, _ := caixaEnv(t)
	bg := context.Background()
	alertID := domain.NewID()
	if _, err := e.svc.Pool.Exec(bg, `INSERT INTO alerts (id, company_id, kind, subject_type, subject_id, severity, message, data, opened_at, last_seen_at)
		VALUES ($1, $2, 'spool', 'external_host', $3, 'critical', 'Spool cheio', '{}', now(), now())`, alertID, e.co.ID, domain.NewID()); err != nil {
		t.Fatal(err)
	}
	ref := service.InboxRef{Kind: domain.InboxAlert, ID: alertID}
	count := func() int {
		in, err := e.svc.Inbox(as(ana), e.co.ID)
		if err != nil {
			t.Fatal(err)
		}
		return len(in.Alerts)
	}
	if _, err := e.svc.SetInboxState(as(ana), ana.Actor(), e.co.ID, domain.InboxArchive, []service.InboxRef{ref}); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 0 {
		t.Fatalf("arquivado some: %d alertas", n)
	}
	age := func(h int) {
		if _, err := e.svc.Pool.Exec(bg, `UPDATE inbox_state SET updated_at = now() - make_interval(hours => $1) WHERE company_id = $2 AND item_id = $3`, h, e.co.ID, alertID); err != nil {
			t.Fatal(err)
		}
	}
	age(23)
	if n := count(); n != 0 {
		t.Errorf("marca de 23 h, prazo padrão de 24 h: o alerta devia seguir arquivado, veio %d", n)
	}
	age(25)
	if n := count(); n != 1 {
		t.Errorf("marca de 25 h, prazo padrão de 24 h: o alerta devia voltar, veio %d", n)
	}
	if _, err := e.svc.PutAlertSetting(bg, domain.HumanActor, e.co.ID, "due.spool", 0); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 0 {
		t.Errorf("sem prazo (0): a marca vale de novo, veio %d", n)
	}
	var kept int
	if err := e.svc.Pool.QueryRow(bg, `SELECT count(*) FROM inbox_state WHERE company_id = $1 AND item_id = $2`, e.co.ID, alertID).Scan(&kept); err != nil || kept != 1 {
		t.Errorf("vencer não apaga a marca: %d linhas (%v)", kept, err)
	}
}
