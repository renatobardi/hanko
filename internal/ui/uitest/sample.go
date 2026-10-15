package uitest

import (
	"encoding/json"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Now is the clock of the sample data and of the UI tests: noon of the current day in the company
// time zone. Anchoring at noon keeps "today", "this week" and "this month" stable whatever the hour
// the tests run (near midnight, UTC and the company day used to disagree).
func Now() time.Time {
	if !Fixed.IsZero() {
		return Fixed
	}
	l, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		l = time.UTC
	}
	t := time.Now().In(l)
	return time.Date(t.Year(), t.Month(), t.Day(), 12, 0, 0, 0, l).UTC()
}

// Fixed, when set, replaces Now: the UI tests pin the date because the sample month-to-date spend
// grows with the day of the month.
var Fixed time.Time

// Empty returns a backend with one company and nothing else (fresh install).
func Empty() *Fake {
	now := Now()
	return &Fake{Companies: []domain.Company{{ID: ID("company"), Slug: "hanko", Name: "Hanko", TaskPrefix: "HNK", Timezone: "America/Sao_Paulo", CreatedAt: now, UpdatedAt: now}}}
}

// None returns a backend with no company at all (onboarding).
func None() *Fake { return &Fake{} }

func rid(n string) domain.ID {
	// run ids end in recognisable hex so the "#xxxxxx" handle looks like the design
	return domain.ID(mustUUID("0199a1f2-0000-7000-8000-000000" + n))
}

// Sample returns a populated backend: the Hanko company, five personas, tasks
// HNK-2xx, runs, a thread and an activity feed, relative to now.
func Sample() *Fake {
	f := Empty()
	co := f.Companies[0]
	now := Now()
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	mk := func(slug, name, title, agent, model string, st domain.PersonaStatus, hb int, instr string) domain.Persona {
		return domain.Persona{ID: ID("persona:" + slug), CompanyID: co.ID, Slug: slug, Name: name, Title: title, Agent: agent, Model: model,
			Status: st, HeartbeatIntervalSec: hb, Instructions: instr, CreatedAt: ago(9 * 24 * time.Hour), UpdatedAt: ago(time.Hour)}
	}
	ada := mk("ada", "Ada", "Chief of staff", "claude", "claude-sonnet", domain.PersonaRunning, 900,
		"Você é a Ada, Chief of staff da Hanko. Receba a meta ligada à task, delegue para a persona certa e só conclua com evidência (diff, screenshot ou teste). Abra um gate para o humano quando houver decisão irreversível.")
	hana := mk("hana", "Hana", "Pesquisa e proveniência", "codex", "gpt-5", domain.PersonaRunning, 900, "Você é a Hana. Pesquise com fonte citada e rastreável.")
	kenji := mk("kenji", "Kenji", "Engenheiro de plataforma", "claude", "claude-opus", domain.PersonaAwaitingGate, 1800, "Você é o Kenji. Plataforma, infraestrutura e migrações.")
	mika := mk("mika", "Mika", "Operações de conhecimento", "claude", "claude-haiku", domain.PersonaIdle, 3600, "Você é a Mika. Coleta, limpeza e curadoria de fontes.")
	sato := mk("sato", "Sato", "Conteúdo e distribuição", "fake", "", domain.PersonaPaused, 0, "Você é o Sato. Conteúdo para LinkedIn.")
	// organograma: Ada no topo; Hana, Kenji e Sato respondem a ela; Mika a Kenji
	adaID, kenjiID := ada.ID, kenji.ID
	hana.ReportsTo, kenji.ReportsTo, sato.ReportsTo, mika.ReportsTo = &adaID, &adaID, &adaID, &kenjiID
	kenji.RequireReview = true
	f.Personas = []domain.Persona{ada, hana, kenji, mika, sato}

	linkedin := domain.Project{ID: ID("project:linkedin"), CompanyID: co.ID, Slug: "linkedin", Name: "LinkedIn", Status: domain.ProjectActive, CreatedAt: now, UpdatedAt: now}
	kubo := domain.Project{ID: ID("project:kubo"), CompanyID: co.ID, Slug: "kubo", Name: "Kubo", Status: domain.ProjectActive, CreatedAt: now, UpdatedAt: now}
	nas := domain.Project{ID: ID("project:nas"), CompanyID: co.ID, Slug: "nas-3d", Name: "NAS 3D", Status: domain.ProjectActive, CreatedAt: now, UpdatedAt: now}
	site := domain.Workspace{ID: ID("workspace:site"), CompanyID: co.ID, Slug: "site", Name: "Site institucional", Path: f.WorkspacesRootPath() + "/hanko/site",
		RepoURL: "https://github.com/hanko/site", DefaultBranch: "main", CreatedAt: now, UpdatedAt: now}
	f.Workspaces = []domain.Workspace{site}
	kubo.WorkspaceID = &site.ID
	kubo.BudgetMonthlyCents = func() *int64 { c := int64(5000); return &c }()
	f.Projects = []domain.Project{linkedin, kubo, nas}

	run1842, run1841, run1840, run1839, run1838 := rid("7c1842"), rid("7c1841"), rid("7c1840"), rid("7c1839"), rid("7c1838")
	type spec struct {
		n      int64
		title  string
		st     domain.TaskStatus
		pr     domain.Priority
		who    *domain.Persona
		proj   *domain.Project
		age    time.Duration
		active *domain.ID
		desc   string
	}
	tasks := []spec{
		{221, "Docslide: AI-first SDLC no banco", domain.TaskInProgress, domain.PriorityCritical, &ada, &linkedin, 4 * time.Minute, &run1842,
			"Criar um docslide de **5 slides** sobre a modernização do AI-first SDLC no banco, no meu tom de LinkedIn: tese forte no slide 1, dados do piloto no 3, provocação no 5.\n\nUsar a skill `linkedin-docslide`."},
		{219, "Extrair entidades das fontes de setembro", domain.TaskInProgress, domain.PriorityHigh, &hana, &kubo, 11 * time.Minute, &run1841, "Extrair pessoas, organizações e conceitos das fontes coletadas em setembro e gravar no grafo."},
		{217, "Templates de e-mail do digest", domain.TaskInProgress, domain.PriorityMedium, &mika, &kubo, time.Hour, nil, "Três templates: diário, semanal e alerta."},
		{220, "Revisar arquitetura do Kubo v0.4", domain.TaskInReview, domain.PriorityHigh, &kenji, &kubo, 2 * time.Hour, nil, "Revisão de arquitetura antes do corte da v0.4."},
		{203, "Refatorar coletor de fontes RSS", domain.TaskInReview, domain.PriorityMedium, &kenji, &kubo, 5 * time.Hour, nil, ""},
		{218, "Plano de migração do NAS 3D", domain.TaskBlocked, domain.PriorityCritical, &kenji, &nas, 19 * time.Minute, nil, "Mover os volumes do NAS 3D para o pool nvme com snapshot ZFS antes. Etapa 3 é irreversível."},
		{209, "Benchmark de inferência local", domain.TaskBlocked, domain.PriorityMedium, &mika, &nas, 3 * time.Hour, nil, "Rodar o conjunto de benchmark em modelos locais."},
		{222, "Post: por que gates humanos escalam melhor que revisão de código", domain.TaskTodo, domain.PriorityHigh, &sato, &linkedin, 26 * time.Hour, nil, ""},
		{211, "Limpar fontes duplicadas no grafo", domain.TaskTodo, domain.PriorityLow, nil, &kubo, 3 * 24 * time.Hour, nil, ""},
		{224, "Agendar digest de sexta às 7h", domain.TaskBacklog, domain.PriorityLow, &ada, &kubo, 2 * 24 * time.Hour, nil, ""},
		{214, "Digest semanal de fontes", domain.TaskDone, domain.PriorityMedium, &ada, &kubo, 41 * time.Minute, nil, "Digest enviado a 3 destinos."},
	}
	for _, s := range tasks {
		t := domain.Task{ID: ID("task:" + domain.TaskKey("HNK", s.n)), CompanyID: co.ID, Number: s.n, Key: domain.TaskKey("HNK", s.n), Title: s.title, Description: s.desc,
			Status: s.st, Priority: s.pr, CreatedBy: domain.HumanActor, CreatedAt: now.Add(-s.age - 48*time.Hour), UpdatedAt: now.Add(-s.age)}
		tv := service.TaskView{Task: t}
		if s.who != nil {
			id := s.who.ID
			tv.PersonaID, tv.PersonaName = &id, s.who.Name
		}
		if s.proj != nil {
			id := s.proj.ID
			tv.ProjectID, tv.ProjectName = &id, s.proj.Name
		}
		tv.ActiveRunID = s.active
		f.Tasks = append(f.Tasks, tv)
	}
	f.nextTask = 224

	t221 := f.Tasks[0]
	f.Comments = []domain.Comment{
		{ID: ID("c1"), CompanyID: co.ID, TaskID: t221.ID, Author: domain.PersonaActor(ada.ID), Body: "Li o pedido e a skill. Vou estruturar os 5 slides e delegar os dados do piloto para a Hana (ela tem acesso ao relatório de setembro). Antes de escrever, uma pergunta sobre o escopo.", CreatedAt: ago(50 * time.Minute)},
		{ID: ID("c2"), CompanyID: co.ID, TaskID: t221.ID, Author: domain.HumanActor, Body: "Pode delegar. Mas o slide 3 precisa citar a fonte dos números — quero proveniência rastreável.", CreatedAt: ago(47 * time.Minute)},
		{ID: ID("c3"), CompanyID: co.ID, TaskID: t221.ID, Author: domain.PersonaActor(ada.ID), RunID: &run1842, Body: "Combinado. Abri **HNK-223** para a Hana com a exigência de citação. Enquanto isso, rascunhei o slide 1:\n\n- tese: *gate humano escala melhor que revisão*\n- gancho: o número do piloto\n\n> Rascunho em `work/slide-01-tese.md`", CreatedAt: ago(45 * time.Minute)},
	}

	cost := func(v float64) *float64 { return &v }
	st := func(d time.Duration) *time.Time { t := now.Add(-d); return &t }
	run := func(id domain.ID, tk string, who domain.Persona, task string, stt domain.RunStatus, trig domain.RunTrigger, started, finished time.Duration, c *float64, in, out int64, errMsg, summary string) service.RunView {
		r := domain.Run{ID: id, Name: domain.RunName(id, 0), CompanyID: co.ID, TaskID: ID("task:" + tk), PersonaID: who.ID, Status: stt, Trigger: trig, Agent: who.Agent, Model: who.Model,
			CreatedAt: now.Add(-started), StartedAt: st(started), CostUSD: c, Usage: domain.Usage{InputTokens: in, OutputTokens: out}, Error: errMsg, Summary: summary, SessionRef: "sess-" + tk}
		if finished >= 0 {
			r.FinishedAt = st(finished)
		}
		var title string
		for _, t := range f.Tasks {
			if t.Key == tk {
				title = t.Title
			}
		}
		return service.RunView{Run: r, TaskKey: tk, TaskTitle: title, PersonaName: who.Name}
	}
	f.Gates = sampleGates(co.ID, f, now, ada, kenji, run1840)
	f.Runs = []service.RunView{
		run(run1842, "HNK-221", ada, "", domain.RunRunning, domain.TriggerAssign, 4*time.Minute+12*time.Second, -1, cost(0.06), 84200, 11900, "", ""),
		run(run1841, "HNK-219", hana, "", domain.RunRunning, domain.TriggerHeartbeat, 11*time.Minute, -1, cost(0.14), 51300, 6200, "", ""),
		run(run1840, "HNK-218", kenji, "", domain.RunDone, domain.TriggerHeartbeat, 19*time.Minute+30*time.Second, 17*time.Minute, cost(0.42), 142000, 18800, "", "Plano de migração reenviado com snapshot ZFS antes de mover os volumes; etapa 3 marcada como **irreversível**."),
		run(run1839, "HNK-214", ada, "", domain.RunDone, domain.TriggerHeartbeat, 41*time.Minute+13*time.Second, 41*time.Minute, cost(0.08), 22400, 1900, "", "Digest enviado a 3 destinos."),
		run(run1838, "HNK-222", sato, "", domain.RunFailed, domain.TriggerManual, time.Hour+2*time.Minute, time.Hour, cost(0), 0, 0, "bloqueada por orçamento do projeto LinkedIn", ""),
	}

	f.Events = map[domain.ID][]domain.RunEvent{}
	add := func(rid domain.ID, ago time.Duration, k domain.RunEventKind, text string) {
		seq := int64(len(f.Events[rid]) + 1)
		ts := now.Add(-ago)
		f.Events[rid] = append(f.Events[rid], domain.RunEvent{ID: domain.NewID(), CompanyID: co.ID, RunID: rid, Seq: seq, Kind: k, Text: text, OccurredAt: ts, ReceivedAt: ts})
	}
	add(run1842, 4*time.Minute+12*time.Second, domain.EventSystem, "run.start agent=ada adapter=claude model=claude-sonnet")
	add(run1842, 4*time.Minute+9*time.Second, domain.EventSystem, "context.load task=HNK-221 skills=[linkedin-docslide]")
	add(run1842, 4*time.Minute+7*time.Second, domain.EventToolUse, "Read skills/linkedin-docslide/SKILL.md")
	add(run1842, 4*time.Minute+6*time.Second, domain.EventToolResult, "4.1 kB")
	add(run1842, 3*time.Minute+50*time.Second, domain.EventThinking, "O pedido tem 5 slides; o slide 3 depende dos dados do piloto, que a Hana pode extrair.")
	add(run1842, 3*time.Minute+40*time.Second, domain.EventToolUse, "Read docs/piloto-set-2026.md")
	add(run1842, 3*time.Minute+30*time.Second, domain.EventAssistant, "Vou estruturar os 5 slides e delegar os dados do piloto para a Hana.")
	add(run1842, 3*time.Minute, domain.EventToolUse, "Write work/slide-01-tese.md (v1)")
	add(run1842, 2*time.Minute+50*time.Second, domain.EventStdout, "slide-01-tese.md escrito (412 palavras)")
	add(run1842, 2*time.Minute, domain.EventStderr, "aviso: fonte do número do slide 3 ainda sem citação")
	add(run1842, time.Minute+30*time.Second, domain.EventToolUse, "task.create parent=HNK-221 assignee=hana")
	add(run1842, time.Minute, domain.EventStatus, "aguardando resposta humana · composer liberado")
	add(run1841, 11*time.Minute, domain.EventSystem, "run.start agent=hana adapter=codex model=gpt-5")
	add(run1841, 10*time.Minute, domain.EventAssistant, "Extraindo entidades de 214 fontes.")
	add(run1840, 19*time.Minute, domain.EventSystem, "run.start agent=kenji")
	add(run1840, 17*time.Minute, domain.EventResult, "Plano reenviado com snapshot ZFS.")

	acts := []struct {
		ago  time.Duration
		who  domain.Actor
		verb string
		typ  string
		id   domain.ID
		data string
	}{
		{4 * time.Minute, domain.PersonaActor(ada.ID), "run.finished", "run", run1839, ""},
		{19 * time.Minute, domain.PersonaActor(kenji.ID), "task.status_changed", "task", t221.ID, ""},
		{41 * time.Minute, domain.PersonaActor(ada.ID), "task.created", "task", f.Tasks[1].ID, ""},
		{47 * time.Minute, domain.HumanActor, "comment.added", "comment", ID("c2"), `{"task_id":"` + t221.ID.String() + `"}`},
		{time.Hour, domain.HumanActor, "persona.hired", "persona", hana.ID, ""},
		{2 * time.Hour, domain.SystemActor, "task.updated", "task", f.Tasks[5].ID, ""},
		{27 * time.Hour, domain.HumanActor, "task.created", "task", f.Tasks[7].ID, ""},
		{19*time.Minute + 30*time.Second, domain.PersonaActor(kenji.ID), "gate.requested", "gate", f.Gates[0].ID, `{"title":"Aprovar o plano de migração do NAS 3D"}`},
		{26 * time.Hour, domain.HumanActor, "gate.decided", "gate", f.Gates[3].ID, `{"title":"Publicar o digest semanal","decision":"approved"}`},
	}
	for i, a := range acts {
		ev := domain.ActivityEvent{ID: ID("act:" + string(rune('a'+i))), CompanyID: co.ID, Actor: a.who, Verb: a.verb, SubjectType: a.typ, SubjectID: a.id, OccurredAt: now.Add(-a.ago), ReceivedAt: now.Add(-a.ago)}
		if a.data != "" {
			ev.Data = json.RawMessage(a.data)
		}
		f.Activity = append(f.Activity, ev)
	}
	sampleCostsAndFlows(f, now)
	sampleArtefMetas(f, now)
	return f
}

func sampleGates(co domain.ID, f *Fake, now time.Time, ada, kenji domain.Persona, run domain.ID) []service.GateView {
	taskOf := func(key string) service.TaskView {
		for _, t := range f.Tasks {
			if t.Key == key {
				return t
			}
		}
		return service.TaskView{}
	}
	mk := func(label string, kind domain.GateKind, st domain.GateStatus, who domain.Persona, taskKey, title, body string, age time.Duration) service.GateView {
		g := domain.Gate{ID: ID("gate:" + label), CompanyID: co, Kind: kind, Status: st, Title: title, Body: body, PersonaID: &who.ID,
			RequestedBy: domain.PersonaActor(who.ID), CreatedAt: now.Add(-age)}
		v := service.GateView{Gate: g, PersonaName: who.Name}
		if taskKey != "" {
			t := taskOf(taskKey)
			id := t.ID
			v.TaskID, v.TaskKey, v.TaskTitle = &id, t.Key, t.Title
		}
		return v
	}
	decide := func(g service.GateView, ago time.Duration, note string) service.GateView {
		at := now.Add(-ago)
		by := domain.HumanActor
		g.DecidedAt, g.DecidedBy, g.DecisionNote = &at, &by, note
		return g
	}
	g1 := mk("plan", domain.GateApproval, domain.GatePending, kenji, "HNK-218", "Aprovar o plano de migração do NAS 3D",
		"Plano em **4 etapas**:\n\n1. Snapshot ZFS dos volumes\n2. Copiar os volumes para o pool nvme\n3. Trocar os pontos de montagem (**irreversível**)\n4. Validar leitura e escrita\n\nEstimativa de custo: US$ 2,40. Janela sugerida: hoje às 22:00.", 19*time.Minute)
	g1.RunID = &run
	g2 := mk("review", domain.GateTaskReview, domain.GatePending, kenji, "HNK-220", "Revisar HNK-220 — Revisar arquitetura do Kubo v0.4",
		"Kenji deu a task como pronta e exige revisão antes de concluir. Aprove para concluir, peça mudanças ou rejeite.", 2*time.Hour)
	g3 := mk("hire", domain.GateHire, domain.GatePending, ada, "", "Contratar Yuki (Dados)",
		"Ada propõe contratar **Yuki** para o projeto Kubo: harness claude, modelo sonnet, reporta a Ada. Orçamento sugerido: US$ 60 por mês.", time.Hour)
	g4 := decide(mk("digest", domain.GateApproval, domain.GateApproved, ada, "HNK-214", "Publicar o digest semanal nos 3 destinos",
		"Publicação externa nos 3 destinos configurados. Pede aprovação antes de enviar.", 27*time.Hour), 26*time.Hour, "Pode publicar. Confira o link do slide 3 antes.")
	g4.Status = domain.GateApproved
	g5 := decide(mk("merge", domain.GateApproval, domain.GateRejected, kenji, "HNK-203", "Fazer merge do coletor refatorado na main",
		"A refatoração mexe no coletor de fontes. Pede aprovação para o merge.", 3*24*time.Hour), 3*24*time.Hour-time.Hour, "Ainda sem testes de carga. Volte com os números.")
	g5.Status = domain.GateRejected
	g6 := decide(mk("slides", domain.GateApproval, domain.GateChangesRequested, ada, "HNK-221", "Enviar o rascunho dos slides para revisão externa",
		"Rascunho do slide 1 e 2 pronto para sair da empresa.", 2*24*time.Hour), 2*24*time.Hour-30*time.Minute, "Troque o dado do slide 2 por um com fonte citada.")
	g6.Status = domain.GateChangesRequested
	return []service.GateView{g1, g2, g3, g4, g5, g6}
}

// sampleCostsAndFlows adds a month of ledger entries, budgets and a few flows.
func sampleCostsAndFlows(f *Fake, now time.Time) {
	co := &f.Companies[0]
	byName := map[string]*domain.Persona{}
	for i := range f.Personas {
		byName[f.Personas[i].Slug] = &f.Personas[i]
	}
	cents := func(usd int64) *int64 { v := usd * 100; return &v }
	co.BudgetMonthlyCents = cents(400)
	byName["ada"].BudgetMonthlyCents = cents(120)
	byName["kenji"].BudgetMonthlyCents = cents(150)
	byName["sato"].BudgetMonthlyCents = cents(60)
	byName["hana"].BudgetMonthlyCents = cents(80)

	loc := f.companyTZ()
	type src struct {
		persona, project string
		base             float64
		estEvery         int
	}
	sources := []src{{"ada", "Kubo", 5.2, 0}, {"kenji", "NAS 3D", 8.6, 3}, {"sato", "LinkedIn", 4.4, 0}, {"hana", "Kubo", 3.1, 4}, {"mika", "Kubo", 0.6, 0}}
	pat := []float64{0.9, 1.25, 0.7, 1.1, 0.4, 1.6, 1.0, 0.8, 1.35, 0.6, 1.2, 0.95, 1.5, 0.5}
	start := time.Date(now.In(loc).Year(), now.In(loc).Month(), 1, 0, 0, 0, 0, loc)
	for d := 0; ; d++ {
		day := start.AddDate(0, 0, d)
		if day.After(now) {
			break
		}
		for si, sc := range sources {
			p := byName[sc.persona]
			factor := pat[(d+si*3)%len(pat)]
			model := p.Model
			e := LedgerEntry{At: day.Add(time.Duration(9+si) * time.Hour), PersonaID: p.ID, Project: sc.project, Model: model, Harness: p.Agent,
				CostUSD: sc.base * factor, In: int64(180000 * factor), Out: int64(24000 * factor)}
			if sc.estEvery > 0 && d%sc.estEvery == 0 {
				e.Estimated = true
			}
			if e.At.After(now) {
				e.At = now.Add(-time.Minute)
			}
			f.Ledger = append(f.Ledger, e)
		}
	}

	proj := func(slug string) *domain.ID {
		for _, p := range f.Projects {
			if p.Slug == slug {
				id := p.ID
				return &id
			}
		}
		return nil
	}
	mkFlow := func(slug, name, desc, cron string, enabled bool, who string, project string, title string) domain.Flow {
		next, _ := service.NextCron(cron, co.Timezone, now)
		fl := domain.Flow{ID: ID("flow:" + slug), CompanyID: co.ID, Slug: slug, Name: name, Description: desc, Cron: cron, Timezone: co.Timezone, Enabled: enabled,
			PersonaID: byName[who].ID, ProjectID: proj(project), Priority: domain.PriorityMedium, TaskTitle: title,
			CreatedAt: now.Add(-20 * 24 * time.Hour), UpdatedAt: now.Add(-time.Hour)}
		if enabled {
			fl.NextFireAt = &next
		}
		return fl
	}
	flows := []domain.Flow{
		mkFlow("digest-semanal", "Digest semanal de fontes", "Lê fontes novas, destila e envia aos 3 destinos", "0 7 * * 5", true, "ada", "kubo", "Digest semanal de fontes · {{data}}"),
		mkFlow("conteudo-linkedin", "Conteúdo semanal LinkedIn", "Gera 1 post + 1 docslide a partir dos destilados da semana", "0 8 * * 1", true, "sato", "linkedin", "Conteúdo da semana · {{data}}"),
		mkFlow("coleta-rss", "Coleta de fontes RSS", "Ingere os feeds e abre task só quando há item novo relevante", "0 */2 * * *", true, "mika", "kubo", "Coleta RSS #{{n}}"),
		mkFlow("metricas-alcance", "Métricas de alcance", "Coleta impressões e salva no destilado mensal", "0 23 * * *", false, "sato", "linkedin", "Métricas · {{data}}"),
	}
	for i := range flows {
		fl := flows[i]
		if fl.Slug == "digest-semanal" || fl.Slug == "coleta-rss" {
			t := now.Add(-26 * time.Hour)
			fl.LastFiredAt = &t
		}
		f.Flows = append(f.Flows, f.flowView(fl))
	}
	limit := 60.0
	f.Subs = append(f.Subs, service.SubscriptionStatus{Subscription: domain.Subscription{ID: ID("sub:Claude Max"), CompanyID: f.Companies[0].ID,
		Name: "Claude Max", Agent: "claude", MonthlyUSD: 200, ModelPrefixes: []string{"claude-opus", "claude-sonnet"}, WeeklyLimitUSD: &limit,
		WeeklyGuardPct: 90, Active: true, CreatedAt: now.AddDate(0, -2, 0), UpdatedAt: now}, WeekUsageUSD: 22.5, WeekPercent: 37.5})
	digest := f.Flows[0]
	for i, k := range []struct {
		ago time.Duration
		key string
		st  string
	}{{2 * 24 * time.Hour, "HNK-214", "done"}, {9 * 24 * time.Hour, "HNK-198", "done"}, {16 * 24 * time.Hour, "HNK-181", "done"}} {
		at := now.Add(-k.ago).Truncate(time.Minute)
		tid := ID("task:" + k.key)
		f.Fires = append(f.Fires, service.FlowFireView{
			FlowFire: domain.FlowFire{ID: ID("fire:" + k.key), FlowID: digest.ID, CompanyID: co.ID, ScheduledFor: at, TaskID: &tid, CreatedAt: at},
			TaskKey:  k.key, TaskTitle: "Digest semanal de fontes", TaskStatus: k.st})
		_ = i
	}
}
