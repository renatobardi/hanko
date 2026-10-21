package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Gate é um ponto em que um humano decide: aprovar, rejeitar ou pedir mudanças.
type Gate struct {
	ID              string         `json:"id" format:"uuid"`
	CompanyID       string         `json:"company_id" format:"uuid"`
	Kind            string         `json:"kind" enum:"approval,task_review,hire,memory,tool,instruction,question,plan,connection" doc:"approval: ação irreversível, merge, deploy, gasto; task_review: revisão para concluir uma task; hire: contratação (com ficha, aprovar cria a persona); memory: proposta de curadoria de memória que remove, funde ou reescreve conteúdo; tool: chamada de tool que pede aprovação antes (gateway de tools); instruction: a persona propõe mudar as próprias instruções (ADR-0052); question: formulário que o humano responde (ADR-0041); plan: plano a aprovar antes de executar (ADR-0041); connection: a persona pede uma conexão OAuth e um humano precisa autorizar (ADR-0043)."`
	Status          string         `json:"status" enum:"pending,approved,rejected,changes_requested,expired,dismissed"`
	Title           string         `json:"title" example:"Fazer deploy em produção"`
	Body            string         `json:"body" doc:"Pedido em Markdown: o que está sendo decidido, evidência e impacto."`
	TaskID          string         `json:"task_id,omitempty" format:"uuid"`
	TaskKey         string         `json:"task_key,omitempty" example:"HNK-12"`
	TaskTitle       string         `json:"task_title,omitempty"`
	RunID           string         `json:"run_id,omitempty" format:"uuid"`
	PersonaID       string         `json:"persona_id,omitempty" format:"uuid" doc:"Persona que pediu (e espera a decisão)."`
	PersonaName     string         `json:"persona_name,omitempty"`
	RequestedBy     ActorRef       `json:"requested_by"`
	DecidedBy       *ActorRef      `json:"decided_by,omitempty"`
	DecisionNote    string         `json:"decision_note,omitempty"`
	RequestedStatus string         `json:"requested_status,omitempty" enum:"done,cancelled" doc:"Em task_review: o status que a persona pediu; aprovar o aplica."`
	EventID         string         `json:"event_id,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	DecidedAt       *time.Time     `json:"decided_at,omitempty"`
	Review          *GateReviewOut `json:"review,omitempty" doc:"Revisão automática feita por outro modelo antes do gate; a decisão continua humana."`
	// ADR-0041
	Revision   int                 `json:"revision" doc:"Versão do pedido: 1 na criação, +1 a cada reenvio (histórico em /revisions)."`
	Audience   *GateAudience       `json:"audience,omitempty" doc:"Quem pode decidir e ver o gate na Caixa; ausente = qualquer decisor do papel exigido."`
	Form       *GateForm           `json:"form,omitempty" doc:"Gate question: o formulário."`
	Answers    map[string][]string `json:"answers,omitempty" doc:"Gate question respondido: chave do campo → valores."`
	Hire       *GateHireSpec       `json:"hire,omitempty" doc:"Gate hire: a ficha; aprovar cria a persona."`
	ArtifactID string              `json:"artifact_id,omitempty" format:"uuid" doc:"Gate plan: o artefato (versão) de onde veio o plano."`
}

// GateReviewOut é o parecer do revisor automático registrado no gate.
type GateReviewOut struct {
	Reviewer       string `json:"reviewer" doc:"Quem revisou (persona ou harness/modelo)."`
	Verdict        string `json:"verdict,omitempty" enum:"approved,rejected" doc:"Ausente quando a revisão não concluiu; a justificativa diz o motivo."`
	Justification  string `json:"justification,omitempty"`
	ArtifactSHA256 string `json:"artifact_sha256,omitempty" doc:"sha256 do pacote revisado (task + entrega)."`
	RunID          string `json:"run_id,omitempty" format:"uuid"`
}

func gateOut(g service.GateView) Gate {
	o := Gate{ID: idStr(g.ID), CompanyID: idStr(g.CompanyID), Kind: string(g.Kind), Status: string(g.Status), Title: g.Title,
		Body: g.Body, TaskID: idPtr(g.TaskID), TaskKey: g.TaskKey, TaskTitle: g.TaskTitle, RunID: idPtr(g.RunID),
		PersonaID: idPtr(g.PersonaID), PersonaName: g.PersonaName, RequestedBy: actorRef(g.RequestedBy),
		DecisionNote: g.DecisionNote, RequestedStatus: string(g.RequestedStatus), EventID: g.EventID, CreatedAt: g.CreatedAt, DecidedAt: g.DecidedAt}
	if g.DecidedBy != nil {
		a := actorRef(*g.DecidedBy)
		o.DecidedBy = &a
	}
	if r := g.Review; r != nil {
		o.Review = &GateReviewOut{Reviewer: r.Reviewer, Verdict: string(r.Verdict), Justification: r.Justification,
			ArtifactSHA256: r.ArtifactSHA256, RunID: idPtr(r.RunID)}
	}
	gateExtras(&o, g)
	return o
}

// OrgNode é uma persona no organograma, com as que reportam a ela.
type OrgNode struct {
	Persona      Persona   `json:"persona"`
	Depth        int       `json:"depth" doc:"0 = topo do organograma."`
	OpenTasks    int       `json:"open_tasks"`
	PendingGates int       `json:"pending_gates" doc:"Gates que a persona espera."`
	Children     []OrgNode `json:"children" doc:"Personas que reportam a esta."`
}

// OrgChart é a árvore reports_to da empresa.
type OrgChart struct {
	Roots []OrgNode `json:"roots"`
	Total int       `json:"total"`
	Depth int       `json:"depth" doc:"Níveis do organograma."`
}

func orgNodeOut(n *service.OrgNode) OrgNode {
	o := OrgNode{Persona: personaOut(n.Persona), Depth: n.Depth, OpenTasks: n.OpenTasks, PendingGates: n.PendingGates,
		Children: make([]OrgNode, 0, len(n.Children))}
	for _, c := range n.Children {
		o.Children = append(o.Children, orgNodeOut(c))
	}
	return o
}

func orgChartOut(c service.OrgChart) OrgChart {
	o := OrgChart{Roots: make([]OrgNode, 0, len(c.Roots)), Total: c.Total, Depth: c.Depth}
	for _, r := range c.Roots {
		o.Roots = append(o.Roots, orgNodeOut(r))
	}
	return o
}

type GatePath struct {
	CompanyPath
	Gate string `path:"gate" format:"uuid" doc:"Id do gate."`
}

type listGatesIn struct {
	CompanyPath
	Page
	Status  string `query:"status" doc:"Status separados por vírgula: pending,approved,rejected,changes_requested,expired,dismissed." example:"pending"`
	Kind    string `query:"kind" doc:"Tipos separados por vírgula: approval,task_review,hire,memory,tool,instruction,question,plan,connection."`
	Task    string `query:"task" doc:"Id ou chave da task."`
	Persona string `query:"persona" doc:"Id ou slug da persona que pediu."`
	Mine    bool   `query:"mine" doc:"Só os gates da audiência da pessoa autenticada: que pediu ou decidiu, ou de tasks em que participa. Vazio sem pessoa autenticada."`
}

type requestGateIn struct {
	CompanyPath
	Body struct {
		Kind    string `json:"kind,omitempty" enum:"approval,task_review,hire,question,plan" doc:"Padrão approval. Com run token (persona) valem approval, question, plan e hire; qualquer outro vira approval."`
		Title   string `json:"title" minLength:"1" maxLength:"300" example:"Fazer deploy em produção"`
		Body    string `json:"body,omitempty" doc:"Pedido em Markdown: o que está sendo decidido, evidência e impacto."`
		Task    string `json:"task,omitempty" doc:"Id ou chave da task; com run token, padrão é a task da execução."`
		EventID string `json:"event_id,omitempty" maxLength:"200" doc:"Chave de idempotência: repetir devolve o gate existente."`
		// ADR-0041
		Form     *GateForm     `json:"form,omitempty" doc:"Obrigatório em kind=question."`
		Hire     *GateHireSpec `json:"hire,omitempty" doc:"kind=hire: a ficha; aprovar o gate cria a persona. Sem ela o gate é só uma decisão."`
		Artifact string        `json:"artifact,omitempty" doc:"kind=plan: nome ou id de um artefato da task com o plano (ou envie o texto em body)."`
		Audience *GateAudience `json:"audience,omitempty" doc:"Restringe quem decide e quem vê o gate na Caixa."`
	}
}

type decideGateIn struct {
	GatePath
	Body struct {
		Decision string `json:"decision" enum:"approved,rejected,changes_requested" doc:"Decisão do humano."`
		Note     string `json:"note,omitempty" doc:"Comentário da decisão; vai para a thread da task e para a trilha de auditoria. Obrigatório em changes_requested."`
		Always   bool   `json:"always,omitempty" doc:"Só em gate tool aprovado e só admin: grava a política \"permitir\" da persona para esta tool, e as próximas chamadas passam direto."`
	}
}

func parseGateStatuses(csv string) ([]domain.GateStatus, error) {
	var out []domain.GateStatus
	for _, p := range splitCSV(csv) {
		st := domain.GateStatus(p)
		if !st.Valid() {
			return nil, domain.Invalid("status", fmt.Sprintf("valor desconhecido %q", p))
		}
		out = append(out, st)
	}
	return out, nil
}

func parseGateKinds(csv string) ([]domain.GateKind, error) {
	var out []domain.GateKind
	for _, p := range splitCSV(csv) {
		k := domain.GateKind(p)
		if !k.Valid() {
			return nil, domain.Invalid("kind", fmt.Sprintf("valor desconhecido %q", p))
		}
		out = append(out, k)
	}
	return out, nil
}

func (s *server) registerGates(api huma.API) {
	huma.Register(api, op("list-gates", http.MethodGet, "/companies/{company}/gates", "gates", "Lista gates"),
		func(ctx context.Context, in *listGatesIn) (*ListOut[Gate], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			statuses, err := parseGateStatuses(in.Status)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			kinds, err := parseGateKinds(in.Kind)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			task, err := s.taskRef(ctx, sc.Company.ID, in.Task)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			persona, err := s.personaRef(ctx, sc.Company.ID, in.Persona)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			gs, err := s.svc.ListGates(ctx, sc.Company.ID, service.GateFilter{Statuses: statuses, Kinds: kinds, TaskID: task,
				PersonaID: persona, Mine: in.Mine, Limit: in.Limit, Offset: in.Offset})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(gs, in.Page, gateOut), nil
		})

	huma.Register(api, created(op("request-gate", http.MethodPost, "/companies/{company}/gates", "gates", "Pede uma decisão humana (abre um gate)")),
		func(ctx context.Context, in *requestGateIn) (*ItemOut[Gate], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			b := in.Body
			task, err := s.taskRef(ctx, sc.Company.ID, b.Task)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			kind := domain.GateKind(b.Kind)
			var runID *domain.ID
			if sc.Claims != nil { // persona: a execução e (por padrão) a task vêm do token
				switch kind { // persona pede aprovação, pergunta, plano ou contratação; a revisão de task é aberta pelo Hanko
				case domain.GateQuestion, domain.GatePlan:
				case domain.GateHire:
					if b.Hire == nil { // um pedido de contratação de persona sempre traz a ficha
						return nil, s.fail(ctx, domain.Invalid("hire", "uma contratação pedida por persona precisa da ficha (hire)"))
					}
				default:
					kind = domain.GateApproval
				}
				if id, err := domain.ParseID(sc.Claims.RunID); err == nil {
					runID = &id
				}
				if task == nil {
					if id, err := domain.ParseID(sc.Claims.TaskID); err == nil {
						task = &id
					}
				}
			}
			aud, err := audienceIn(b.Audience)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			hire, err := s.hireIn(ctx, sc.Company.ID, b.Hire)
			if err != nil {
				return nil, err
			}
			g, err := s.svc.RequestGate(ctx, sc.Actor, sc.Company.ID, service.RequestGateInput{Kind: kind,
				Title: b.Title, Body: b.Body, TaskID: task, RunID: runID, EventID: b.EventID,
				Form: formIn(b.Form), Hire: hire, Artifact: b.Artifact, Audience: aud})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Gate]{Body: gateOut(g)}, nil
		})

	huma.Register(api, op("get-gate", http.MethodGet, "/companies/{company}/gates/{gate}", "gates", "Obtém um gate"),
		func(ctx context.Context, in *GatePath) (*ItemOut[Gate], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			id, err := domain.ParseID(in.Gate)
			if err != nil {
				return nil, s.fail(ctx, domain.Invalid("gate", "não é um uuid"))
			}
			g, err := s.svc.GetGate(ctx, sc.Company.ID, id)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Gate]{Body: gateOut(g)}, nil
		})

	huma.Register(api, op("decide-gate", http.MethodPost, "/companies/{company}/gates/{gate}/decide", "gates",
		"Decide um gate pendente (aprovar, rejeitar, pedir mudanças); só humanos"),
		func(ctx context.Context, in *decideGateIn) (*ItemOut[Gate], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			id, err := domain.ParseID(in.Gate)
			if err != nil {
				return nil, s.fail(ctx, domain.Invalid("gate", "não é um uuid"))
			}
			g, err := s.svc.DecideGate(ctx, sc.Actor, sc.Company.ID, id, service.DecideGateInput{
				Decision: domain.GateStatus(in.Body.Decision), Note: in.Body.Note, Always: in.Body.Always})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Gate]{Body: gateOut(g)}, nil
		})

	huma.Register(api, op("get-org-chart", http.MethodGet, "/companies/{company}/org-chart", "personas", "Organograma da empresa (árvore reports_to)"),
		func(ctx context.Context, in *CompanyPath) (*ItemOut[OrgChart], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			c, err := s.svc.OrgChart(ctx, sc.Company.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[OrgChart]{Body: orgChartOut(c)}, nil
		})
}
