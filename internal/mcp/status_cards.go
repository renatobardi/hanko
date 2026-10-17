package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Cards de status pelo MCP (ADR-0032). As escritas da resumidora (filtros e resumo) só existem aqui e só valem
// com o token da run de geração do card.

// statusCardBackend é o que as tools de card de status pedem ao service. Fica fora de Backend para que fakes
// que não usam cards continuem compilando; sem ele as tools respondem que o recurso está indisponível.
type statusCardBackend interface {
	CreateStatusCard(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateStatusCardInput) (service.StatusCardView, error)
	ListPersonaStatusCards(ctx context.Context, companyID, personaID domain.ID, taskID *domain.ID) ([]service.StatusCardView, error)
	SetStatusCardQuery(ctx context.Context, actor domain.Actor, companyID domain.ID, ref service.CardRunRef, cardID domain.ID, in service.SetCardQueryInput) (service.StatusCardView, error)
	SetStatusCardSummary(ctx context.Context, actor domain.Actor, companyID domain.ID, ref service.CardRunRef, cardID domain.ID, in service.SetCardSummaryInput) (service.StatusCardView, []string, error)
}

type cardQueryIn struct {
	Label    string   `json:"label,omitempty" jsonschema:"descrição curta do filtro (ex. bloqueadas do Site)"`
	Status   []string `json:"status,omitempty" jsonschema:"status aceitos: backlog, todo, in_progress, in_review, blocked, done, cancelled"`
	Priority []string `json:"priority,omitempty" jsonschema:"prioridades aceitas: critical, high, medium, low"`
	Persona  string   `json:"persona,omitempty" jsonschema:"slug da persona responsável"`
	Project  string   `json:"project,omitempty" jsonschema:"slug do projeto"`
	Goal     string   `json:"goal,omitempty" jsonschema:"slug da meta"`
	Text     string   `json:"text,omitempty" jsonschema:"texto no título ou na descrição (até 200 caracteres)"`
	Open     bool     `json:"open,omitempty" jsonschema:"só tasks abertas (nem done nem cancelled)"`
}

type cardSetQueryIn struct {
	Company       string        `json:"company,omitempty" jsonschema:"slug ou id da empresa (omita com token de run)"`
	Card          string        `json:"card,omitempty" jsonschema:"id do card (está na descrição da task); vazio = o card da sua execução"`
	Queries       []cardQueryIn `json:"queries" jsonschema:"1 a 10 filtros; dentro de um filtro os campos valem juntos (E), entre filtros vale qualquer um (OU)"`
	Title         string        `json:"title,omitempty" jsonschema:"título curto do card (ignorado se o humano fixou o título)"`
	ChangeSummary string        `json:"change_summary,omitempty" jsonschema:"uma frase sobre como você leu o interesse"`
}

type cardSetSummaryIn struct {
	Company       string   `json:"company,omitempty" jsonschema:"slug ou id da empresa (omita com token de run)"`
	Card          string   `json:"card,omitempty" jsonschema:"id do card (está na descrição da task); vazio = o card da sua execução"`
	Markdown      string   `json:"markdown" jsonschema:"o resumo em markdown, até 64 KiB"`
	Mentioned     []string `json:"mentioned,omitempty" jsonschema:"chaves das tasks citadas no resumo (ex. HNK-12), até 200"`
	ChangeSummary string   `json:"change_summary,omitempty" jsonschema:"o que mudou nesta versão, em uma ou duas frases"`
	Title         string   `json:"title,omitempty" jsonschema:"título novo (opcional; ignorado se o humano fixou o título)"`
}

type cardListIn struct {
	Company string `json:"company,omitempty" jsonschema:"slug ou id da empresa (omita com token de run)"`
}

type cardCreateIn struct {
	Company    string `json:"company,omitempty" jsonschema:"slug ou id da empresa (omita com token de run)"`
	Interest   string `json:"interest" jsonschema:"o que acompanhar, em linguagem natural (até 4000 caracteres)"`
	Title      string `json:"title,omitempty" jsonschema:"título do card; vazio = a resumidora propõe"`
	Summarizer string `json:"summarizer,omitempty" jsonschema:"slug da persona resumidora; vazio = você"`
	Mode       string `json:"mode,omitempty" jsonschema:"manual, interval ou reactive (padrão reactive: assim que algo mudar, com espera)"`
}

type cardOut struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	State         string `json:"state" jsonschema:"compiling, active, error, paused_budget ou paused_hours"`
	ViewState     string `json:"view_state" jsonschema:"archived, compiling, error, paused_budget, paused_hours, updating, stale ou fresh"`
	Interest      string `json:"interest"`
	QueryVersion  int    `json:"query_version"`
	Revision      int    `json:"summary_revision"`
	Pending       int    `json:"pending_changes"`
	Summarizer    string `json:"summarizer,omitempty"`
	Generation    bool   `json:"generation_for_you,omitempty" jsonschema:"true quando a sua execução é a geração ativa deste card"`
	FailureReason string `json:"failure_reason,omitempty"`
}

type cardWriteOut struct {
	Card cardOut `json:"card"`
	// Unknown são chaves citadas que não existem na empresa (ignoradas).
	Unknown []string `json:"unknown_mentions,omitempty"`
}

type cardListOut struct {
	Cards []cardOut `json:"cards"`
}

func registerStatusCardTools(s *sdk.Server, t *tools) {
	sdk.AddTool(s, &sdk.Tool{
		Name: "hanko_status_card_set_query",
		Description: "Etapa de montagem de um card de status: grava os filtros de task que traduzem o interesse do humano (1 a 10 filtros) e o título. " +
			"Só vale para a persona resumidora, na task \"Montar card de status\" da sua execução. Em seguida o Hanko abre a primeira atualização do resumo.",
	}, t.cardSetQuery)
	sdk.AddTool(s, &sdk.Tool{
		Name: "hanko_status_card_set_summary",
		Description: "Etapa de atualização de um card de status: grava o resumo em markdown (até 64 KiB) com as chaves das tasks citadas. " +
			"Só vale para a persona resumidora, na task \"Atualizar card de status\" da sua execução. Sem esta chamada a atualização conta como falha.",
	}, t.cardSetSummary)
	sdk.AddTool(s, &sdk.Tool{
		Name:        "hanko_status_card_list",
		Description: "Lista os cards de status que você criou e o card da sua execução atual (se ela for uma montagem ou atualização de card).",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true},
	}, t.cardList)
	sdk.AddTool(s, &sdk.Tool{
		Name: "hanko_status_card_create",
		Description: "Cria um card de status para alguém do time acompanhar um conjunto de tasks (ex. tudo que está bloqueado no projeto Site). " +
			"Você cria até 20 cards; a resumidora (padrão: você) monta os filtros e mantém o resumo.",
	}, t.cardCreate)
}

// cardScope resolve a empresa, a persona e a run da chamada: cards pelo MCP exigem token de run.
func (t *tools) cardScope(ctx context.Context, company string) (statusCardBackend, identity, domain.ID, error) {
	cb, ok := t.b.(statusCardBackend)
	if !ok {
		return nil, identity{}, domain.ID{}, errors.New("cards de status indisponíveis neste servidor")
	}
	id, cid, err := t.scope(ctx, company)
	if err != nil {
		return nil, identity{}, domain.ID{}, err
	}
	if id.PersonaID == nil {
		return nil, identity{}, domain.ID{}, errors.New("cards de status pelo MCP são da persona: use uma conexão com token de run")
	}
	return cb, id, cid, nil
}

// cardRef monta a referência da run e resolve o card (vazio = o card cuja geração é a task da execução).
func (t *tools) cardRef(ctx context.Context, cb statusCardBackend, id identity, cid domain.ID, card string) (service.CardRunRef, domain.ID, error) {
	if id.TaskID == nil {
		return service.CardRunRef{}, domain.ID{}, errors.New("esta execução não tem task: os filtros e o resumo só são gravados na task de geração do card")
	}
	ref := service.CardRunRef{PersonaID: *id.PersonaID, TaskID: *id.TaskID, RunID: id.RunID}
	if c := strings.TrimSpace(card); c != "" {
		cardID, err := domain.ParseID(c)
		if err != nil {
			return ref, domain.ID{}, fmt.Errorf("card %q: use o id (UUID) que está na descrição da task", c)
		}
		return ref, cardID, nil
	}
	cs, err := cb.ListPersonaStatusCards(ctx, cid, *id.PersonaID, id.TaskID)
	if err != nil {
		return ref, domain.ID{}, opErr("achar o card", err)
	}
	for _, c := range cs {
		if c.GeneratingTaskID != nil && *c.GeneratingTaskID == *id.TaskID {
			return ref, c.ID, nil
		}
	}
	return ref, domain.ID{}, errors.New("a sua execução não é a geração ativa de nenhum card; informe card")
}

func toCardOut(v service.StatusCardView, taskID *domain.ID) cardOut {
	o := cardOut{ID: v.ID.String(), Title: v.Title, State: string(v.State), ViewState: string(v.ViewState), Interest: v.Interest,
		QueryVersion: v.QueryVersion, Revision: v.SummaryRevision, Pending: v.PendingChangeCount, Summarizer: v.SummarizerSlug,
		FailureReason: v.FailureReason}
	if taskID != nil && v.GeneratingTaskID != nil && *v.GeneratingTaskID == *taskID {
		o.Generation = true
	}
	if r := []rune(o.Interest); len(r) > 500 {
		o.Interest = string(r[:500]) + "…"
	}
	return o
}

func (t *tools) cardSetQuery(ctx context.Context, _ *sdk.CallToolRequest, in cardSetQueryIn) (*sdk.CallToolResult, cardWriteOut, error) {
	cb, id, cid, err := t.cardScope(ctx, in.Company)
	if err != nil {
		return nil, cardWriteOut{}, err
	}
	ref, cardID, err := t.cardRef(ctx, cb, id, cid, in.Card)
	if err != nil {
		return nil, cardWriteOut{}, err
	}
	qs := make([]service.CardQueryInput, len(in.Queries))
	for i, q := range in.Queries {
		qs[i] = service.CardQueryInput{Label: q.Label, Statuses: q.Status, Priorities: q.Priority, Persona: q.Persona, Project: q.Project,
			Goal: q.Goal, Text: q.Text, OpenOnly: q.Open}
	}
	v, err := cb.SetStatusCardQuery(ctx, id.Actor, cid, ref, cardID, service.SetCardQueryInput{Queries: qs, Title: in.Title, ChangeSummary: in.ChangeSummary})
	if err != nil {
		return nil, cardWriteOut{}, opErr("gravar os filtros do card", err)
	}
	return textResult("Filtros gravados (versão %d, %d filtro(s)). O Hanko abriu a primeira atualização do resumo; encerre esta execução.",
		v.QueryVersion, len(v.Queries)), cardWriteOut{Card: toCardOut(v, id.TaskID)}, nil
}

func (t *tools) cardSetSummary(ctx context.Context, _ *sdk.CallToolRequest, in cardSetSummaryIn) (*sdk.CallToolResult, cardWriteOut, error) {
	cb, id, cid, err := t.cardScope(ctx, in.Company)
	if err != nil {
		return nil, cardWriteOut{}, err
	}
	ref, cardID, err := t.cardRef(ctx, cb, id, cid, in.Card)
	if err != nil {
		return nil, cardWriteOut{}, err
	}
	v, unknown, err := cb.SetStatusCardSummary(ctx, id.Actor, cid, ref, cardID, service.SetCardSummaryInput{Markdown: in.Markdown, Title: in.Title,
		ChangeSummary: in.ChangeSummary, Mentioned: in.Mentioned})
	if err != nil {
		return nil, cardWriteOut{}, opErr("gravar o resumo do card", err)
	}
	msg := fmt.Sprintf("Resumo gravado (revisão %d, %d task(s) citada(s)).", v.SummaryRevision, len(v.MentionedTaskIDs))
	if len(unknown) > 0 {
		msg += " Chaves ignoradas (não existem na empresa): " + strings.Join(unknown, ", ") + "."
	}
	return textResult("%s", msg), cardWriteOut{Card: toCardOut(v, id.TaskID), Unknown: unknown}, nil
}

func (t *tools) cardList(ctx context.Context, _ *sdk.CallToolRequest, in cardListIn) (*sdk.CallToolResult, cardListOut, error) {
	cb, id, cid, err := t.cardScope(ctx, in.Company)
	if err != nil {
		return nil, cardListOut{}, err
	}
	cs, err := cb.ListPersonaStatusCards(ctx, cid, *id.PersonaID, id.TaskID)
	if err != nil {
		return nil, cardListOut{}, opErr("listar cards", err)
	}
	out := cardListOut{Cards: make([]cardOut, len(cs))}
	var b strings.Builder
	for i, c := range cs {
		out.Cards[i] = toCardOut(c, id.TaskID)
		title := c.Title
		if title == "" {
			title = "(sem título)"
		}
		fmt.Fprintf(&b, "- %s %s [%s]", c.ID, title, c.ViewState)
		if out.Cards[i].Generation {
			b.WriteString(" ← geração da sua execução")
		}
		b.WriteString("\n")
	}
	if len(cs) == 0 {
		b.WriteString("Nenhum card de status seu.")
	}
	return textResult("%s", strings.TrimRight(b.String(), "\n")), out, nil
}

func (t *tools) cardCreate(ctx context.Context, _ *sdk.CallToolRequest, in cardCreateIn) (*sdk.CallToolResult, cardWriteOut, error) {
	cb, id, cid, err := t.cardScope(ctx, in.Company)
	if err != nil {
		return nil, cardWriteOut{}, err
	}
	ci := service.CreateStatusCardInput{Interest: in.Interest, Title: in.Title}
	if s := strings.TrimSpace(in.Summarizer); s != "" {
		p, err := t.b.GetPersona(ctx, cid, s)
		if err != nil {
			return nil, cardWriteOut{}, toolErr("persona", s, err)
		}
		ci.SummarizerPersonaID = &p.ID
	}
	if m := strings.TrimSpace(in.Mode); m != "" {
		pol := domain.DefaultCardPolicy()
		pol.Mode = m
		ci.Policy = &pol
	}
	v, err := cb.CreateStatusCard(ctx, id.Actor, cid, ci)
	if err != nil {
		return nil, cardWriteOut{}, opErr("criar card", err)
	}
	return textResult("Card de status criado: %s. A resumidora vai montar os filtros.", v.ID), cardWriteOut{Card: toCardOut(v, id.TaskID)}, nil
}
