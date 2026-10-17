package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/export"
	"github.com/renatobardi/hanko/internal/service"
)

// sourceDoc monta uma empresa de origem com configuração suficiente para exercitar o merge e a exporta.
func sourceDoc(t *testing.T, e *env) export.Document {
	t.Helper()
	ctx := context.Background()
	src, err := e.svc.CreateCompany(ctx, domain.HumanActor, service.CreateCompanyInput{Name: "Origem", Slug: "origem"})
	if err != nil {
		t.Fatal(err)
	}
	sk, err := e.svc.CreateSkill(ctx, domain.HumanActor, src.ID, service.CreateSkillInput{Name: "Revisar", Slug: "revisar", Body: "# Revisar\ncorpo novo"})
	if err != nil {
		t.Fatal(err)
	}
	dev, err := e.svc.HirePersona(ctx, domain.HumanActor, src.ID, service.HirePersonaInput{Name: "Dev", Slug: "dev", Agent: "fake", Model: "m1", Instructions: "do arquivo"})
	if err != nil {
		t.Fatal(err)
	}
	qa, err := e.svc.HirePersona(ctx, domain.HumanActor, src.ID, service.HirePersonaInput{Name: "QA", Slug: "qa", Agent: "fake", Model: "m1", ReportsTo: &dev.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetPersonaSkill(ctx, domain.HumanActor, src.ID, qa.ID, sk.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateProject(ctx, domain.HumanActor, src.ID, service.CreateProjectInput{Name: "Lançamento", Slug: "lancamento"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateSubscription(ctx, domain.HumanActor, src.ID, service.CreateSubscriptionInput{Name: "Plano Pro", Agent: "fake", MonthlyUSD: 20}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateFlow(ctx, domain.HumanActor, src.ID, service.CreateFlowInput{Name: "Diário", Slug: "diario", Cron: "0 9 * * *", PersonaID: dev.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateTask(ctx, domain.HumanActor, src.ID, service.CreateTaskInput{Title: "histórico"}); err != nil {
		t.Fatal(err)
	}
	doc, err := e.svc.ExportCompany(ctx, src.ID, service.ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func itemOf(r service.ImportResult, kind, key string) (service.ImportItem, bool) {
	for _, it := range r.Items {
		if it.Kind == kind && it.Key == key {
			return it, true
		}
	}
	return service.ImportItem{}, false
}

func TestImportDryRunNovaEmpresa(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	doc := sourceDoc(t, e)
	before, _ := e.svc.ListCompanies(ctx)

	res, err := e.svc.ImportCompany(ctx, domain.HumanActor, doc, service.ImportOptions{Slug: "destino", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.DryRun || res.Company.Slug != "destino" || res.Counts["personas"] != 2 || res.Counts["tasks"] != 1 {
		t.Fatalf("prévia: %+v", res)
	}
	if it, ok := itemOf(res, "personas", "dev"); !ok || it.Action != domain.ItemCreate {
		t.Errorf("item dev: %+v", it)
	}
	hasWarn := false
	for _, w := range res.Warnings {
		hasWarn = hasWarn || w.Code == "personas_paused"
	}
	if !hasWarn {
		t.Errorf("faltou aviso de personas pausadas: %+v", res.Warnings)
	}
	after, _ := e.svc.ListCompanies(ctx)
	if len(after) != len(before) {
		t.Fatalf("dry-run gravou empresa: %d -> %d", len(before), len(after))
	}
	// slug em uso responde como o import real
	if _, err := e.svc.ImportCompany(ctx, domain.HumanActor, doc, service.ImportOptions{Slug: "acme-corp", DryRun: true}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("slug em uso: %v", err)
	}
	// arquivo inválido falha igual
	bad := doc
	bad.Format = "x"
	if _, err := e.svc.ImportCompany(ctx, domain.HumanActor, bad, service.ImportOptions{DryRun: true}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("formato: %v", err)
	}
}

func TestImportMergeEmpresaExistente(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	doc := sourceDoc(t, e)

	// destino já tem dev (com instruções próprias), qa, a skill e a assinatura
	dev := e.persona(t, "Dev Antigo", func(in *service.HirePersonaInput) { in.Slug = "dev"; in.Instructions = "do destino" })
	e.persona(t, "QA", func(in *service.HirePersonaInput) { in.Slug = "qa" })
	if _, err := e.svc.CreateSkill(ctx, domain.HumanActor, e.co.ID, service.CreateSkillInput{Name: "Revisar", Slug: "revisar", Body: "corpo antigo"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateSubscription(ctx, domain.HumanActor, e.co.ID, service.CreateSubscriptionInput{Name: "Plano Pro", Agent: "fake", MonthlyUSD: 5}); err != nil {
		t.Fatal(err)
	}
	into := e.co.ID
	opts := service.ImportOptions{Into: &into, DryRun: true, OnConflict: domain.ConflictSkip,
		Resolve: map[string]domain.ConflictAction{"personas/dev": domain.ConflictReplace, "personas/qa": domain.ConflictRename,
			"skills/revisar": domain.ConflictReplace, "subscriptions/Plano Pro": domain.ConflictRename}}

	// prévia
	pre, err := e.svc.ImportCompany(ctx, domain.HumanActor, doc, opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []struct {
		kind, key string
		act       domain.ItemAction
		target    string
	}{
		{"personas", "dev", domain.ItemReplace, "dev"}, {"personas", "qa", domain.ItemRename, "qa-2"},
		{"skills", "revisar", domain.ItemReplace, "revisar"}, {"subscriptions", "Plano Pro", domain.ItemRename, "Plano Pro (2)"},
		{"projects", "lancamento", domain.ItemCreate, "lancamento"}, {"flows", "diario", domain.ItemCreate, "diario"},
	} {
		it, ok := itemOf(pre, w.kind, w.key)
		if !ok || it.Action != w.act || it.Target != w.target {
			t.Errorf("prévia %s/%s = %+v, quer %s -> %s", w.kind, w.key, it, w.act, w.target)
		}
	}
	if ps, _ := e.svc.ListPersonas(ctx, e.co.ID); len(ps) != 2 {
		t.Fatalf("dry-run gravou personas: %d", len(ps))
	}
	if pre.Counts["tasks"] != 0 {
		t.Errorf("histórico não entra: %v", pre.Counts)
	}
	hist := false
	for _, w := range pre.Warnings {
		hist = hist || w.Code == "history_not_merged"
	}
	if !hist {
		t.Error("faltou o aviso de histórico")
	}

	// merge de verdade
	opts.DryRun = false
	res, err := e.svc.ImportCompany(ctx, domain.HumanActor, doc, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Merged || res.Counts["personas"] != 2 {
		t.Fatalf("resultado: %+v", res)
	}
	ps, _ := e.svc.ListPersonas(ctx, e.co.ID)
	bySlug := map[string]domain.Persona{}
	for _, p := range ps {
		bySlug[p.Slug] = p
	}
	if len(ps) != 3 || bySlug["qa-2"].ID == (domain.ID{}) {
		t.Fatalf("personas: %d %v", len(ps), bySlug)
	}
	if got := bySlug["dev"]; got.ID != dev.ID || got.Instructions != "do arquivo" || got.Name != "Dev" {
		t.Errorf("dev deveria ter sido substituído no lugar: %+v", got)
	}
	if bySlug["qa-2"].ReportsTo == nil || *bySlug["qa-2"].ReportsTo != dev.ID {
		t.Errorf("qa-2 deveria reportar ao dev existente: %+v", bySlug["qa-2"])
	}
	if bySlug["qa-2"].Status != domain.PersonaPaused {
		t.Errorf("persona nova entra pausada: %s", bySlug["qa-2"].Status)
	}
	sks, _ := e.svc.ListSkills(ctx, e.co.ID)
	if len(sks) != 1 || sks[0].Body != "# Revisar\ncorpo novo" || sks[0].Version != 2 {
		t.Errorf("skill: %+v", sks)
	}
	subs, _ := e.svc.ListSubscriptions(ctx, e.co.ID)
	if len(subs) != 2 {
		t.Errorf("assinaturas: %d", len(subs))
	}
	flows, _ := e.svc.ListFlows(ctx, e.co.ID)
	if len(flows) != 1 || flows[0].PersonaID != dev.ID || flows[0].Enabled {
		t.Errorf("fluxo importado deveria apontar para o dev e entrar desligado: %+v", flows)
	}
	if tasks, _ := e.svc.ListTasks(ctx, e.co.ID, service.TaskFilter{}); len(tasks) != 0 {
		t.Errorf("tasks não entram: %d", len(tasks))
	}

	// de novo, com o padrão pular: nada muda
	res2, err := e.svc.ImportCompany(ctx, domain.HumanActor, doc, service.ImportOptions{Into: &into})
	if err != nil {
		t.Fatal(err)
	}
	if res2.Counts["personas"] != 0 || res2.Counts["flows"] != 0 {
		t.Errorf("segunda rodada deveria só pular: %v", res2.Counts)
	}
	if ps2, _ := e.svc.ListPersonas(ctx, e.co.ID); len(ps2) != 3 {
		t.Errorf("personas após pular: %d", len(ps2))
	}

	// atividade
	acts, _ := e.svc.ListActivity(ctx, e.co.ID, service.ActivityFilter{})
	merged := 0
	for _, a := range acts {
		if a.Verb == "company.import_merged" {
			merged++
		}
	}
	if merged != 2 {
		t.Errorf("company.import_merged = %d, quer 2", merged)
	}

	// resolução com chave que o arquivo não tem
	if _, err := e.svc.ImportCompany(ctx, domain.HumanActor, doc, service.ImportOptions{Into: &into, DryRun: true,
		Resolve: map[string]domain.ConflictAction{"personas/fantasma": domain.ConflictSkip}}); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("resolução inválida: %v", err)
	}

	// arquivada: nem prévia
	if _, err := e.svc.ArchiveCompany(ctx, domain.HumanActor, e.co.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ImportCompany(ctx, domain.HumanActor, doc, service.ImportOptions{Into: &into}); !errors.Is(err, domain.ErrCompanyArchived) {
		t.Errorf("import em arquivada: %v", err)
	}
}

func TestImportMergeCicloNoOrganograma(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	// destino: dev reporta ao qa. Arquivo: qa reporta ao dev. Substituir o qa fecharia dev→qa→dev.
	qa := e.persona(t, "QA", func(in *service.HirePersonaInput) { in.Slug = "qa" })
	e.persona(t, "Dev", func(in *service.HirePersonaInput) { in.Slug = "dev"; in.ReportsTo = &qa.ID })
	doc := export.Document{Format: export.Format, Company: export.Company{Name: "X"},
		Personas: []export.Persona{{Slug: "dev", Name: "Dev", Agent: "fake", Status: "idle"}, {Slug: "qa", Name: "QA", Agent: "fake", Status: "idle", ReportsTo: "dev"}}}
	into := e.co.ID
	opts := service.ImportOptions{Into: &into, Resolve: map[string]domain.ConflictAction{"personas/qa": domain.ConflictReplace}}
	opts.DryRun = true
	if _, err := e.svc.ImportCompany(ctx, domain.HumanActor, doc, opts); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("prévia deveria acusar o ciclo: %v", err)
	}
	opts.DryRun = false
	if _, err := e.svc.ImportCompany(ctx, domain.HumanActor, doc, opts); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("merge deveria recusar o ciclo: %v", err)
	}
	if got, _ := e.svc.GetPersona(ctx, e.co.ID, "qa"); got.ReportsTo != nil {
		t.Errorf("rollback: qa não deveria ter pai: %+v", got.ReportsTo)
	}
}
