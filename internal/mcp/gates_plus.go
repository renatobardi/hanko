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

// Gates estendidos (ADR-0041): perguntas, planos, reenvio e pedido de contratação. A fase de plano recusa, por
// middleware, toda tool de escrita fora da lista abaixo (mesmo padrão do teste de skill, ADR-0035).

type gatesPlusBackend interface {
	ResubmitGate(ctx context.Context, actor domain.Actor, companyID, gateID domain.ID, in service.ResubmitGateInput) (service.GateView, error)
}

type planPhaseBackend interface {
	PlanPhaseFor(ctx context.Context, companyID, taskID, personaID domain.ID) (domain.PlanPhase, error)
}

// planAllowed são as tools que um run na fase de planejamento pode chamar: ler, comentar, publicar o plano e pedir o gate.
var planAllowed = map[string]bool{
	"hanko_whoami": true, "hanko_task_list": true, "hanko_task_get": true, "hanko_task_comment": true,
	"hanko_gate_request": true, "hanko_gate_status": true, "hanko_gate_resubmit": true, "hanko_hire_request": true,
	"hanko_persona_list": true, "hanko_project_list": true, "hanko_search": true, "hanko_run_get": true,
	"hanko_artifact_put": true, "hanko_attachment_list": true, "hanko_attachment_get": true,
	"hanko_goal_list": true, "hanko_skill_list": true, "hanko_skill_get": true,
	"hanko_case_get": true, "hanko_case_list": true, "hanko_pipeline_list": true, "hanko_status_card_list": true,
}

// planGuard recusa, numa run na fase de plano (política de plan-review sem plano aprovado), toda tool fora de planAllowed.
func planGuard(b any) sdk.Middleware {
	pb, ok := b.(planPhaseBackend)
	return func(next sdk.MethodHandler) sdk.MethodHandler {
		if !ok {
			return next
		}
		return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
			if method != "tools/call" {
				return next(ctx, method, req)
			}
			ctr, ok := req.(*sdk.CallToolRequest)
			if !ok || ctr.Params == nil || planAllowed[ctr.Params.Name] {
				return next(ctx, method, req)
			}
			id, ok := identityFrom(ctx)
			if !ok || !id.ViaToken || id.TaskID == nil || id.CompanyID == nil || id.PersonaID == nil {
				return next(ctx, method, req)
			}
			phase, err := pb.PlanPhaseFor(ctx, *id.CompanyID, *id.TaskID, *id.PersonaID)
			if err != nil {
				return nil, fmt.Errorf("fase de plano: %w", err)
			}
			if phase == domain.PlanPhasePlan {
				msg := fmt.Sprintf("esta execução só planeja (plan-review): a tool %s fica indisponível até o plano ser aprovado. "+
					"Escreva o plano (hanko_artifact_put) e peça a aprovação com hanko_gate_request kind=plan", ctr.Params.Name)
				return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: msg}}}, nil
			}
			return next(ctx, method, req)
		}
	}
}

// ---- pergunta ----

type questionOptionIn struct {
	Value string `json:"value" jsonschema:"o que volta na resposta"`
	Label string `json:"label,omitempty" jsonschema:"texto exibido ao humano; vazio = o valor"`
}

type questionFieldIn struct {
	Key      string             `json:"key" jsonschema:"chave curta do campo: minúsculas, dígitos e _, começando por letra"`
	Label    string             `json:"label" jsonschema:"a pergunta, como o humano a lê"`
	Type     string             `json:"type" jsonschema:"single (uma opção), multi (várias) ou text (texto livre)"`
	Options  []questionOptionIn `json:"options,omitempty" jsonschema:"opções de single e multi (até 20)"`
	Required bool               `json:"required,omitempty"`
}

func questionForm(fields []questionFieldIn) *domain.QuestionForm {
	f := &domain.QuestionForm{Fields: make([]domain.QuestionField, 0, len(fields))}
	for _, in := range fields {
		fl := domain.QuestionField{Key: in.Key, Label: in.Label, Type: domain.QuestionFieldType(in.Type), Required: in.Required}
		for _, o := range in.Options {
			fl.Options = append(fl.Options, domain.QuestionOption{Value: o.Value, Label: o.Label})
		}
		f.Fields = append(f.Fields, fl)
	}
	return f
}

// audienceRoles converte a lista de papéis do pedido.
func audienceRoles(roles []string) domain.GateAudience {
	var a domain.GateAudience
	for _, r := range roles {
		a.Roles = append(a.Roles, domain.MemberRole(strings.TrimSpace(r)))
	}
	return a
}

// ---- contratação ----

type hireRequestIn struct {
	Company      string `json:"company,omitempty" jsonschema:"slug ou id da empresa (obrigatório só sem token de run)"`
	Title        string `json:"title,omitempty" jsonschema:"o pedido, em uma frase (padrão: Contratar <nome>)"`
	Body         string `json:"body,omitempty" jsonschema:"markdown: por que contratar, o que a persona fará e o impacto no custo"`
	Task         string `json:"task,omitempty" jsonschema:"chave da task; com token de run o padrão é a task da execução atual"`
	Name         string `json:"name" jsonschema:"nome da persona"`
	Slug         string `json:"slug,omitempty" jsonschema:"identificador curto único (padrão: derivado do nome)"`
	PersonaTitle string `json:"persona_title,omitempty" jsonschema:"cargo, ex. Analista de dados"`
	Instructions string `json:"instructions,omitempty" jsonschema:"instruções permanentes da persona"`
	Agent        string `json:"agent" jsonschema:"harness: claude, codex, acp ou fake"`
	Model        string `json:"model,omitempty"`
	ReportsTo    string `json:"reports_to,omitempty" jsonschema:"slug da persona chefe; vazio = você"`
	// Orçamento mensal em centavos de dólar; vazio = sem teto próprio.
	BudgetMonthlyCents *int64 `json:"budget_monthly_cents,omitempty"`
	RequireReview      bool   `json:"require_review,omitempty" jsonschema:"as entregas da nova persona passam por revisão"`
	EventID            string `json:"event_id,omitempty" jsonschema:"chave de idempotência"`
}

type resubmitIn struct {
	Company  string `json:"company,omitempty" jsonschema:"slug ou id da empresa (obrigatório só sem token de run)"`
	ID       string `json:"id" jsonschema:"id (UUID) do gate que voltou com mudanças pedidas"`
	Title    string `json:"title,omitempty" jsonschema:"novo título; vazio = mantém"`
	Body     string `json:"body,omitempty" jsonschema:"o pedido revisado (markdown)"`
	Artifact string `json:"artifact,omitempty" jsonschema:"gate plan: nome do artefato com a versão nova do plano"`
	Note     string `json:"note,omitempty" jsonschema:"o que você mudou em resposta ao pedido do humano"`
}

func registerGatePlusTools(s *sdk.Server, t *tools) {
	sdk.AddTool(s, &sdk.Tool{
		Name: "hanko_hire_request",
		Description: "Propõe contratar uma persona: abre um gate de contratação com a ficha; se um humano aprovar, a persona é criada na hora, " +
			"reportando a você (ou ao chefe indicado). Não aceita variáveis de ambiente/segredos na ficha — o humano as configura depois. " +
			"Depois de pedir, encerre a execução: você será acordada com a decisão.",
	}, t.hireRequest)
	if _, ok := t.b.(gatesPlusBackend); ok {
		sdk.AddTool(s, &sdk.Tool{
			Name: "hanko_gate_resubmit",
			Description: "Reenvia um gate que voltou com 'mudanças pedidas' no MESMO gate: informe o texto revisado (ou, no plano, o artefato com a versão nova) " +
				"e uma nota do que mudou. O humano vê o diff com a versão anterior. Só vale para approval, plan e hire. Depois, encerre a execução.",
		}, t.gateResubmit)
	}
}

func (t *tools) hireRequest(ctx context.Context, _ *sdk.CallToolRequest, in hireRequestIn) (*sdk.CallToolResult, gateWriteOut, error) {
	id, company, err := t.scope(ctx, in.Company)
	if err != nil {
		return nil, gateWriteOut{}, err
	}
	task, err := t.gateTask(ctx, id, company, in.Task)
	if err != nil {
		return nil, gateWriteOut{}, err
	}
	spec := domain.HireSpec{Name: in.Name, Slug: in.Slug, Title: in.PersonaTitle, Instructions: in.Instructions, Agent: in.Agent, Model: in.Model,
		BudgetMonthlyCents: in.BudgetMonthlyCents, RequireReview: in.RequireReview}
	if ref := strings.TrimSpace(in.ReportsTo); ref != "" {
		p, err := t.b.GetPersona(ctx, company, ref)
		if err != nil {
			return nil, gateWriteOut{}, toolErr("persona", ref, err)
		}
		spec.ReportsTo = &p.ID
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = "Contratar " + strings.TrimSpace(in.Name)
	}
	req := service.RequestGateInput{Kind: domain.GateHire, Title: title, Body: in.Body, TaskID: task, EventID: in.EventID, Hire: &spec}
	if id.ViaToken {
		req.RunID = id.RunID
	}
	g, err := t.b.RequestGate(ctx, id.Actor, company, req)
	if err != nil {
		return nil, gateWriteOut{}, opErr("pedir contratação", err)
	}
	return textResult("Contratação proposta (gate %s). Encerre esta execução: você será acordada quando o humano decidir.\n%s", g.ID, gateLine(g)),
		gateWriteOut{Gate: toGate(g)}, nil
}

func (t *tools) gateResubmit(ctx context.Context, _ *sdk.CallToolRequest, in resubmitIn) (*sdk.CallToolResult, gateWriteOut, error) {
	gb, ok := t.b.(gatesPlusBackend)
	if !ok {
		return nil, gateWriteOut{}, errors.New("reenvio de gates indisponível neste servidor")
	}
	id, company, err := t.scope(ctx, in.Company)
	if err != nil {
		return nil, gateWriteOut{}, err
	}
	gid, err := domain.ParseID(strings.TrimSpace(in.ID))
	if err != nil {
		return nil, gateWriteOut{}, errors.New("id do gate inválido (esperado UUID)")
	}
	g, err := gb.ResubmitGate(ctx, id.Actor, company, gid, service.ResubmitGateInput{Title: in.Title, Body: in.Body, Artifact: in.Artifact, Note: in.Note})
	if err != nil {
		return nil, gateWriteOut{}, opErr("reenviar gate", err)
	}
	return textResult("Gate reenviado (revisão %d). Encerre esta execução: você será acordada quando o humano decidir.\n%s", g.Revision, gateLine(g)),
		gateWriteOut{Gate: toGate(g)}, nil
}
