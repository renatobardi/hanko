//go:build integration

package api_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Cards de status pela API (ADR-0032).
func TestAPIStatusCardsLifecycle(t *testing.T) {
	e := newAPIEnv(t)
	ctx := context.Background()
	base := e.base() + "/status-cards"
	sum := e.persona(e.co, "Resumidora", nil)
	tk := e.task(e.co, "Deploy travado", nil, func(in *service.CreateTaskInput) { in.Status = domain.TaskBlocked })

	var c api.StatusCard
	e.want(201, "POST", base, fmt.Sprintf(`{"interest":"tudo que está bloqueado","summarizer":%q,"refresh_policy":{"mode":"manual","triggers":{"status":true}}}`, sum.Slug), &c)
	if c.State != "compiling" || c.ViewState != "compiling" || c.GeneratingTaskID == "" || c.SummarizerSlug != sum.Slug || c.Policy.Mode != "manual" ||
		c.Policy.DailyTokenCap != 100000 || c.CreatedBy.Type != "human" {
		t.Fatalf("criado: %+v", c)
	}
	e.want(422, "POST", base, `{"interest":"x","refresh_policy":{"mode":"sempre","triggers":{"status":true}}}`, nil)
	e.want(422, "POST", base, `{"interest":""}`, nil)

	// a resumidora monta pelo service (o MCP é o caminho dela) e o resumo sai
	id, _ := domain.ParseID(c.ID)
	gen, _ := domain.ParseID(c.GeneratingTaskID)
	run, err := e.svc.CheckoutRun(ctx, service.Wake{CompanyID: e.co.ID, PersonaID: sum.ID, TaskID: gen, Trigger: domain.TriggerStatusCard})
	if err != nil {
		t.Fatal(err)
	}
	ref := service.CardRunRef{PersonaID: sum.ID, TaskID: gen, RunID: &run.ID}
	v, err := e.svc.SetStatusCardQuery(ctx, domain.PersonaActor(sum.ID), e.co.ID, ref, id,
		service.SetCardQueryInput{Title: "Bloqueios", Queries: []service.CardQueryInput{{Statuses: []string{"blocked"}}}})
	if err != nil {
		t.Fatal(err)
	}
	gen2 := *v.GeneratingTaskID
	run2, err := e.svc.CheckoutRun(ctx, service.Wake{CompanyID: e.co.ID, PersonaID: sum.ID, TaskID: gen2, Trigger: domain.TriggerStatusCard})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.SetStatusCardSummary(ctx, domain.PersonaActor(sum.ID), e.co.ID, service.CardRunRef{PersonaID: sum.ID, TaskID: gen2, RunID: &run2.ID}, id,
		service.SetCardSummaryInput{Markdown: "# Bloqueios\n\n" + tk.Key, Mentioned: []string{tk.Key}}); err != nil {
		t.Fatal(err)
	}

	e.want(200, "GET", base+"/"+c.ID, "", &c)
	if c.Title != "Bloqueios" || c.SummaryRevision != 1 || c.ViewState != "fresh" || len(c.Mentioned) != 1 || c.Mentioned[0].Key != tk.Key ||
		len(c.Queries) != 1 || c.Queries[0].Statuses[0] != "blocked" {
		t.Fatalf("detalhe: %+v", c)
	}
	var list api.ListBody[api.StatusCard]
	e.want(200, "GET", base, "", &list)
	if len(list.Items) != 1 || list.Items[0].ID != c.ID {
		t.Fatalf("lista: %+v", list.Items)
	}
	var ups api.ListBody[api.StatusCardUpdate]
	e.want(200, "GET", base+"/"+c.ID+"/updates", "", &ups)
	if len(ups.Items) != 2 || ups.Items[0].Kind != "full" || ups.Items[0].Status != "ok" || ups.Items[1].Kind != "compile" || ups.Items[0].TaskKey == "" {
		t.Fatalf("rodadas: %+v", ups.Items)
	}
	var revs api.ListBody[api.StatusCardRevision]
	e.want(200, "GET", base+"/"+c.ID+"/revisions", "", &revs)
	if len(revs.Items) != 1 || !strings.Contains(revs.Items[0].Markdown, tk.Key) {
		t.Fatalf("revisões: %+v", revs.Items)
	}
	var pv api.StatusCardPreview
	e.want(200, "GET", base+"/"+c.ID+"/preview", "", &pv)
	if len(pv.Tasks) != 1 || pv.Tasks[0].Key != tk.Key || len(pv.Queries) != 1 || pv.Queries[0].Matches != 1 || pv.QueryVersion != 1 {
		t.Fatalf("preview: %+v", pv)
	}

	// atualizar agora (full) e conflito com a geração aberta
	e.want(200, "POST", base+"/"+c.ID+"/refresh", `{"full":true}`, &c)
	if c.ViewState != "updating" {
		t.Fatalf("refresh: %+v", c)
	}
	e.want(409, "POST", base+"/"+c.ID+"/refresh", `{}`, nil)
	// remontar
	e.want(200, "POST", base+"/"+c.ID+"/recompile", "", &c)
	if c.State != "compiling" {
		t.Fatalf("recompile: %+v", c)
	}
	// editar, arquivar, listar arquivados, restaurar
	e.want(200, "PATCH", base+"/"+c.ID, `{"title":"Meu título","title_pinned":true}`, &c)
	if c.Title != "Meu título" || !c.TitlePinned {
		t.Fatalf("patch: %+v", c)
	}
	e.want(200, "PATCH", base+"/"+c.ID, `{"archived":true}`, &c)
	if c.ViewState != "archived" || c.GeneratingTaskID != "" {
		t.Fatalf("arquivado: %+v", c)
	}
	e.want(200, "GET", base, "", &list)
	if len(list.Items) != 0 {
		t.Fatalf("ativos: %+v", list.Items)
	}
	e.want(200, "GET", base+"?archived=true", "", &list)
	if len(list.Items) != 1 {
		t.Fatalf("arquivados: %+v", list.Items)
	}

	// run token: lê, não escreve
	hdr := e.personaToken(e.co, sum)
	if code, b := e.call("GET", base+"/"+c.ID, "", hdr); code != 200 {
		t.Fatalf("leitura com run token: %d %s", code, b)
	}
	if code, _ := e.call("POST", base, `{"interest":"x"}`, hdr); code != 403 {
		t.Fatalf("escrita com run token: %d", code)
	}
	if code, _ := e.call("PATCH", base+"/"+c.ID, `{"archived":false}`, hdr); code != 403 {
		t.Fatalf("patch com run token: %d", code)
	}

	// apagar; depois 404
	e.want(204, "DELETE", base+"/"+c.ID, "", nil)
	e.want(404, "GET", base+"/"+c.ID, "", nil)
	e.want(404, "GET", base+"/"+domain.NewID().String()+"/updates", "", nil)
	e.want(422, "GET", base+"/nao-uuid", "", nil)
}
