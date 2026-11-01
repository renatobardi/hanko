package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Modelos de acesso (ADR-0089): dado por empresa, dois semeados na criação, cópia no uso.

func TestAccessTemplatesSeededOnCompanyCreation(t *testing.T) {
	e := newConnectEnv(t)
	list, err := e.svc.ListAccessTemplates(context.Background(), e.co.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("semeados: %d", len(list))
	}
	bySlug := map[string]domain.AccessTemplate{}
	for _, tp := range list {
		bySlug[tp.Slug] = tp
	}
	ro := bySlug["somente-leitura"]
	if ro.ReadMode != domain.PolicyAllow || ro.WriteMode != domain.PolicyOff || ro.DestructiveMode != domain.PolicyOff || ro.Suggested {
		t.Fatalf("somente-leitura: %+v", ro)
	}
	lw := bySlug["leitura-livre-escrita-com-aprovacao"]
	if lw.ReadMode != domain.PolicyAllow || lw.WriteMode != domain.PolicyAsk || lw.DestructiveMode != domain.PolicyAsk || !lw.Suggested {
		t.Fatalf("leitura livre: %+v", lw)
	}
	// risco desconhecido cai no destrutivo (o mais restritivo entre escrita e destruição)
	if got := lw.ModeForRisk(domain.ToolRiskUnknown); got != domain.PolicyAsk {
		t.Fatalf("unknown → %s", got)
	}
	if got := ro.ModeForRisk(domain.ToolRiskUnknown); got != domain.PolicyOff {
		t.Fatalf("unknown → %s", got)
	}
	if got := lw.ModeForRisk(domain.ToolRiskRead); got != domain.PolicyAllow {
		t.Fatalf("read → %s", got)
	}
	// a semeadura deixou activity_event
	if n := e.count(t, `SELECT count(*) FROM activity_events WHERE company_id = $1 AND verb = 'access_template.seeded'`, e.co.ID); n != 2 {
		t.Fatalf("seeded = %d", n)
	}
}

func TestAccessTemplateCRUD(t *testing.T) {
	e := newConnectEnv(t)
	ctx := context.Background()

	tp, err := e.svc.CreateAccessTemplate(ctx, domain.HumanActor, e.co.ID, service.AccessTemplateInput{
		Name: "Escrita livre", Description: "Tudo liberado", ReadMode: domain.PolicyAllow,
		WriteMode: domain.PolicyAllow, DestructiveMode: domain.PolicyAllow})
	if err != nil {
		t.Fatal(err)
	}
	if tp.Slug != "escrita-livre" {
		t.Fatalf("slug: %s", tp.Slug)
	}
	// modo inválido
	if _, err := e.svc.CreateAccessTemplate(ctx, domain.HumanActor, e.co.ID, service.AccessTemplateInput{
		Name: "Quebrado", ReadMode: "talvez"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("modo inválido: %v", err)
	}
	// slug reservado (o assistente usa "Personalizado" na tela como não-modelo)
	if _, err := e.svc.CreateAccessTemplate(ctx, domain.HumanActor, e.co.ID, service.AccessTemplateInput{
		Name: "Personalizado", ReadMode: domain.PolicyAllow, WriteMode: domain.PolicyOff, DestructiveMode: domain.PolicyOff,
		Slug: "personalizado"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("reservado: %v", err)
	}
	// slug duplicado
	if _, err := e.svc.CreateAccessTemplate(ctx, domain.HumanActor, e.co.ID, service.AccessTemplateInput{
		Name: "Escrita livre", ReadMode: domain.PolicyAllow, WriteMode: domain.PolicyAllow, DestructiveMode: domain.PolicyAllow,
		Slug: tp.Slug}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("conflito: %v", err)
	}

	// marcar como sugerido tira o posto do semeado
	up, err := e.svc.UpdateAccessTemplate(ctx, domain.HumanActor, e.co.ID, tp.Slug, service.AccessTemplateInput{
		Name: "Escrita livre", ReadMode: domain.PolicyAllow, WriteMode: domain.PolicyAllow,
		DestructiveMode: domain.PolicyAsk, Suggested: true})
	if err != nil || !up.Suggested {
		t.Fatalf("update: %+v %v", up, err)
	}
	list, _ := e.svc.ListAccessTemplates(ctx, e.co.ID)
	sug := 0
	for _, x := range list {
		if x.Suggested {
			sug++
		}
	}
	if sug != 1 {
		t.Fatalf("sugeridos depois do update: %d", sug)
	}

	// por id e por slug dão no mesmo
	got, err := e.svc.GetAccessTemplate(ctx, e.co.ID, tp.ID.String())
	if err != nil || got.ID != tp.ID {
		t.Fatalf("get por id: %+v %v", got, err)
	}
	// apagar
	if err := e.svc.DeleteAccessTemplate(ctx, domain.HumanActor, e.co.ID, tp.Slug); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.GetAccessTemplate(ctx, e.co.ID, tp.Slug); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("apagado: %v", err)
	}
	for verb, want := range map[string]int{"access_template.created": 1, "access_template.updated": 1, "access_template.deleted": 1} {
		if n := e.count(t, `SELECT count(*) FROM activity_events WHERE company_id = $1 AND verb = $2`, e.co.ID, verb); n != want {
			t.Fatalf("activity %s = %d", verb, n)
		}
	}
}

// Cópia no uso: editar o modelo depois da conclusão não muda a conexão feita.
func TestAccessTemplateCopyNotLink(t *testing.T) {
	e := newConnectEnv(t)
	ctx := context.Background()
	ana := e.persona(t, "Ana", nil)

	v := e.begin(t, "Docs")
	ref := v.Integration.Slug
	e.complete(t, ref, "leitura-livre-escrita-com-aprovacao", ana.ID)

	// o modelo muda de escrita com aprovação para escrita desligada
	if _, err := e.svc.UpdateAccessTemplate(ctx, domain.HumanActor, e.co.ID, "leitura-livre-escrita-com-aprovacao",
		service.AccessTemplateInput{Name: "Leitura livre, escrita com aprovação", ReadMode: domain.PolicyAllow,
			WriteMode: domain.PolicyOff, DestructiveMode: domain.PolicyOff, Suggested: true}); err != nil {
		t.Fatal(err)
	}
	// a persona continua lendo e escrevendo com aprovação: a conexão levou cópia, não link
	tools := e.effTools(t, ana.ID, ref)
	if tl := tools["escrever"]; !tl.Visible || tl.Verdict.Mode != domain.ToolModeAsk {
		t.Fatalf("conexão mudou com o modelo: %+v", tl)
	}
}
