package service_test

import (
	"context"
	"sort"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Visões por participação, busca estendida e atividade por ator (ADR-0042 §B).

func reasonsOf(tasks []service.TaskView) map[string][]string {
	out := map[string][]string{}
	for _, t := range tasks {
		out[t.Key] = t.Participation
	}
	return out
}

func TestMineTasksByParticipation(t *testing.T) {
	e, ana, bruno := caixaEnv(t)
	bg := context.Background()
	p := e.persona(t, "Ajudante", nil)

	// criei: Ana cria; Bruno cria outra
	mine, err := e.svc.CreateTask(as(ana), ana.Actor(), e.co.ID, service.CreateTaskInput{Title: "criada pela Ana"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := e.svc.CreateTask(as(bruno), bruno.Actor(), e.co.ID, service.CreateTaskInput{Title: "criada pelo Bruno"})
	if err != nil {
		t.Fatal(err)
	}
	// comentei: Ana comenta na do Bruno
	if _, err := e.svc.AddComment(as(ana), ana.Actor(), e.co.ID, other.ID, service.AddCommentInput{Body: "boa ideia"}); err != nil {
		t.Fatal(err)
	}
	// mencionado: Bruno cita Ana numa task de ninguém
	third := e.task(t, service.CreateTaskInput{Title: "terceira"})
	if _, err := e.svc.AddComment(as(bruno), bruno.Actor(), e.co.ID, third.ID, service.AddCommentInput{Body: "@ana-souza veja isto"}); err != nil {
		t.Fatal(err)
	}
	// atribuí: Ana atribui a persona a uma task do Bruno... e outra que ninguém tocou
	assigned := e.task(t, service.CreateTaskInput{Title: "para atribuir"})
	untouched := e.task(t, service.CreateTaskInput{Title: "intocada"})
	if _, err := e.svc.UpdateTask(as(ana), ana.Actor(), e.co.ID, assigned.ID, service.UpdateTaskInput{PersonaID: &p.ID}); err != nil {
		t.Fatal(err)
	}
	// audiência de gate: Ana decide um gate de outra task
	gtask := e.task(t, service.CreateTaskInput{Title: "com gate"})
	g, err := e.svc.RequestGate(bg, domain.HumanActor, e.co.ID, service.RequestGateInput{Title: "ok?", TaskID: &gtask.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.DecideGate(as(ana), ana.Actor(), e.co.ID, g.ID, service.DecideGateInput{Decision: domain.GateApproved}); err != nil {
		t.Fatal(err)
	}

	got, err := e.svc.ListTasks(as(ana), e.co.ID, service.TaskFilter{Mine: true})
	if err != nil {
		t.Fatal(err)
	}
	reasons := reasonsOf(got)
	want := map[string][]string{
		mine.Key:     {"created"},
		other.Key:    {"commented"},
		third.Key:    {"mentioned"},
		assigned.Key: {"assigned"},
		gtask.Key:    {"gate"},
	}
	if len(reasons) != len(want) {
		t.Fatalf("Minhas da Ana: %v", reasons)
	}
	for key, r := range want {
		g := reasons[key]
		sort.Strings(g)
		if len(g) != len(r) || g[0] != r[0] {
			t.Errorf("%s: motivos %v, quer %v", key, g, r)
		}
	}
	if _, ok := reasons[untouched.Key]; ok {
		t.Error("task intocada não é da Ana")
	}

	// Bruno: criou a sua, comentou na terceira ao mencionar a Ana, e foi comentada pela Ana (não conta como dele)
	bgot, _ := e.svc.ListTasks(as(bruno), e.co.ID, service.TaskFilter{Mine: true})
	br := reasonsOf(bgot)
	if _, ok := br[other.Key]; !ok || len(br[other.Key]) == 0 || br[other.Key][0] != "created" {
		t.Errorf("Bruno criou %s: %v", other.Key, br)
	}
	if rs := br[third.Key]; len(rs) != 1 || rs[0] != "commented" {
		t.Errorf("Bruno comentou na terceira: %v", rs)
	}
	if _, ok := br[mine.Key]; ok {
		t.Error("a task da Ana não é do Bruno")
	}

	// o filtro combina com os demais e sem pessoa autenticada é vazio
	open, _ := e.svc.ListTasks(as(ana), e.co.ID, service.TaskFilter{Mine: true, Statuses: []domain.TaskStatus{domain.TaskDone}})
	if len(open) != 0 {
		t.Errorf("nenhuma Minha concluída: %v", reasonsOf(open))
	}
	none, err := e.svc.ListTasks(bg, e.co.ID, service.TaskFilter{Mine: true})
	if err != nil || len(none) != 0 {
		t.Fatalf("sem pessoa: %v %v", none, err)
	}
	// sem Mine, a listagem não traz participação
	all, _ := e.svc.ListTasks(as(ana), e.co.ID, service.TaskFilter{})
	for _, tv := range all {
		if tv.Participation != nil {
			t.Fatal("Participation só vem com Mine")
		}
	}
}

func TestMineGatesAudience(t *testing.T) {
	e, ana, bruno := caixaEnv(t)
	bg := context.Background()
	mine := e.task(t, service.CreateTaskInput{Title: "da Ana"})
	if _, err := e.svc.AddComment(as(ana), ana.Actor(), e.co.ID, mine.ID, service.AddCommentInput{Body: "estou nesta"}); err != nil {
		t.Fatal(err)
	}
	onMine, _ := e.svc.RequestGate(bg, domain.HumanActor, e.co.ID, service.RequestGateInput{Title: "numa task em que a Ana participa", TaskID: &mine.ID})
	elsewhereTask := e.task(t, service.CreateTaskInput{Title: "de ninguém"})
	elsewhere, _ := e.svc.RequestGate(bg, domain.HumanActor, e.co.ID, service.RequestGateInput{Title: "alheio", TaskID: &elsewhereTask.ID})
	decidedTask := e.task(t, service.CreateTaskInput{Title: "outra"})
	decided, _ := e.svc.RequestGate(bg, domain.HumanActor, e.co.ID, service.RequestGateInput{Title: "decidido por ela", TaskID: &decidedTask.ID})
	if _, err := e.svc.DecideGate(as(ana), ana.Actor(), e.co.ID, decided.ID, service.DecideGateInput{Decision: domain.GateRejected, Note: "não"}); err != nil {
		t.Fatal(err)
	}
	ids := func(ctx context.Context) map[domain.ID]bool {
		gs, err := e.svc.ListGates(ctx, e.co.ID, service.GateFilter{Mine: true})
		if err != nil {
			t.Fatal(err)
		}
		m := map[domain.ID]bool{}
		for _, g := range gs {
			m[g.ID] = true
		}
		return m
	}
	a := ids(as(ana))
	if !a[onMine.ID] || !a[decided.ID] || a[elsewhere.ID] {
		t.Fatalf("audiência da Ana: %v", a)
	}
	if b := ids(as(bruno)); len(b) != 0 {
		t.Fatalf("Bruno não participa de nada: %v", b)
	}
	if n := ids(bg); len(n) != 0 {
		t.Fatalf("sem pessoa: %v", n)
	}
}

func TestSearchCoversGoalsProjectsGatesArtifactsAndFlows(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.goal(t, func(in *service.CreateGoalInput) {
		in.Title = "Reduzir latência do checkout"
		in.Description = "Meta de performance do pagamento"
	})
	e.project(t, "Plataforma de pagamentos", func(in *service.CreateProjectInput) { in.Description = "Gateway e conciliação" })
	tk := e.task(t, service.CreateTaskInput{Title: "Revisar contrato"})
	if _, err := e.svc.RequestGate(ctx, domain.HumanActor, e.co.ID, service.RequestGateInput{Title: "Liberar verba do gateway", Body: "Custo mensal estimado do gateway", TaskID: &tk.ID}); err != nil {
		t.Fatal(err)
	}
	put := func(name, body string) service.ArtifactView {
		v, err := e.svc.PutArtifact(ctx, domain.HumanActor, e.co.ID, service.PutArtifactInput{Name: name, Content: []byte(body), TaskID: &tk.ID})
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	put("relatorio-gateway.md", "# v1")
	v2 := put("relatorio-gateway.md", "# v2 mudou")
	if _, err := e.svc.CreateFlow(ctx, domain.HumanActor, e.co.ID, service.CreateFlowInput{Name: "Fluxo do gateway", Description: "diário", Cron: "0 9 * * *",
		PersonaID: e.persona(t, "Ana", nil).ID}); err != nil {
		t.Fatal(err)
	}

	search := func(q string, types ...service.SearchType) service.SearchResult {
		r, err := e.svc.Search(ctx, e.co.ID, q, service.SearchFilter{Types: types})
		if err != nil {
			t.Fatalf("Search(%q): %v", q, err)
		}
		return r
	}
	// acento e caixa são dobrados, como nos demais tipos
	if h := group(search("latencia"), service.SearchGoal); len(h) != 1 || h[0].Ref == "" || h[0].Status != "active" {
		t.Fatalf("meta: %+v", h)
	}
	if h := group(search("pagamentos"), service.SearchProject); len(h) != 1 || h[0].Ref != "plataforma-de-pagamentos" {
		t.Fatalf("projeto: %+v", h)
	}
	r := search("gateway")
	if h := group(r, service.SearchGate); len(h) != 1 || h[0].TaskKey != tk.Key || h[0].Status != "pending" {
		t.Fatalf("gate: %+v", h)
	}
	if h := group(r, service.SearchFlow); len(h) != 1 {
		t.Fatalf("fluxo segue achável: %+v", h)
	}
	// artefato: só a versão mais recente da cadeia
	ah := group(r, service.SearchArtifact)
	if len(ah) != 1 || ah[0].ID != v2.ID || ah[0].TaskKey != tk.Key {
		t.Fatalf("artefato: %+v (quer só a v2 %s)", ah, v2.ID)
	}
	// o filtro por tipo
	only := search("gateway", service.SearchGate, service.SearchArtifact)
	if len(only.Groups) != 2 {
		t.Fatalf("grupos: %+v", only.Groups)
	}
	if _, err := e.svc.Search(ctx, e.co.ID, "x", service.SearchFilter{Types: []service.SearchType{"nada"}}); err == nil {
		t.Fatal("tipo desconhecido deveria falhar")
	}
	// outra empresa não enxerga nada disto
	other, err := e.svc.CreateCompany(ctx, domain.HumanActor, service.CreateCompanyInput{Name: "Outra"})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := e.svc.Search(ctx, other.ID, "gateway", service.SearchFilter{})
	if err != nil || r2.Total != 0 {
		t.Fatalf("multi-org: %+v %v", r2, err)
	}
	// ParseSearchTypes aceita os novos
	ts, err := service.ParseSearchTypes("goal,project,gate,artifact")
	if err != nil || len(ts) != 4 {
		t.Fatalf("ParseSearchTypes: %v %v", ts, err)
	}
}

func TestActivityByActorAndResolveActor(t *testing.T) {
	e, ana, bruno := caixaEnv(t)
	bg := context.Background()
	p := e.persona(t, "Ajudante", nil)
	if _, err := e.svc.CreateTask(as(ana), ana.Actor(), e.co.ID, service.CreateTaskInput{Title: "da Ana"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateTask(as(bruno), bruno.Actor(), e.co.ID, service.CreateTaskInput{Title: "do Bruno"}); err != nil {
		t.Fatal(err)
	}
	tk := e.task(t, service.CreateTaskInput{Title: "t"})
	if _, err := e.svc.AddComment(bg, domain.PersonaActor(p.ID), e.co.ID, tk.ID, service.AddCommentInput{Body: "oi"}); err != nil {
		t.Fatal(err)
	}

	list := func(f service.ActivityFilter) []domain.ActivityEvent {
		f.Limit = 200
		out, err := e.svc.ListActivity(as(ana), e.co.ID, f)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	af, err := e.svc.ResolveActor(as(ana), e.co.ID, "human:ANA.SOUZA@x.com")
	if err != nil || af.Type != "human" || af.ID == nil || *af.ID != ana.ID {
		t.Fatalf("resolver pessoa por e-mail (sem caixa): %+v %v", af, err)
	}
	for _, ev := range list(service.ActivityFilter{ActorType: af.Type, ActorID: af.ID}) {
		if ev.Actor.Type != domain.ActorHuman || ev.Actor.ID == nil || *ev.Actor.ID != ana.ID {
			t.Fatalf("só eventos da Ana: %+v", ev)
		}
	}
	if len(list(service.ActivityFilter{ActorType: "human", ActorID: af.ID})) == 0 {
		t.Fatal("a Ana criou uma task")
	}
	byID, err := e.svc.ResolveActor(as(ana), e.co.ID, "human:"+bruno.ID.String())
	if err != nil || *byID.ID != bruno.ID {
		t.Fatalf("por id: %+v %v", byID, err)
	}
	pf, err := e.svc.ResolveActor(as(ana), e.co.ID, "persona:"+p.Slug)
	if err != nil {
		t.Fatal(err)
	}
	pevs := list(service.ActivityFilter{ActorType: pf.Type, ActorID: pf.ID})
	if len(pevs) == 0 {
		t.Fatal("a persona comentou")
	}
	for _, ev := range pevs {
		if ev.Actor.Type != domain.ActorPersona || *ev.Actor.ID != p.ID {
			t.Fatalf("só eventos da persona: %+v", ev)
		}
	}
	// todos os humanos / o sistema
	allHumans, err := e.svc.ResolveActor(as(ana), e.co.ID, "human")
	if err != nil || allHumans.ID != nil {
		t.Fatalf("human sem referência: %+v %v", allHumans, err)
	}
	for _, ev := range list(service.ActivityFilter{ActorType: "human"}) {
		if ev.Actor.Type != domain.ActorHuman {
			t.Fatalf("só humanos: %+v", ev)
		}
	}
	if sys, err := e.svc.ResolveActor(as(ana), e.co.ID, "system"); err != nil || sys.Type != "system" {
		t.Fatalf("system: %+v %v", sys, err)
	}
	for _, bad := range []string{"", "alguem", "persona:fantasma", "human:fantasma@x.com", "robo:x"} {
		if _, err := e.svc.ResolveActor(as(ana), e.co.ID, bad); err == nil {
			t.Errorf("ResolveActor(%q) deveria falhar", bad)
		}
	}
	if _, err := e.svc.ListActivity(as(ana), e.co.ID, service.ActivityFilter{ActorType: "robo"}); err == nil {
		t.Fatal("tipo de ator inválido deveria falhar")
	}
	stranger := mkUser(t, e, "estranho@x.com", domain.RoleMember)
	if _, err := e.svc.ResolveActor(as(stranger), e.co.ID, "system"); err == nil {
		t.Fatal("não-membro não resolve ator")
	}
}
