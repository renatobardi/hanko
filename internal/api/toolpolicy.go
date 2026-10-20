package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Política de tools (ADR-0053). Leitura de qualquer membro; gravar política e testar uma tool "como a persona X" são de
// admin. O hook do claude chama as duas rotas de tool-permission com o run token da própria execução.

type toolPolicyBackend interface {
	SetToolPolicy(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.ToolPolicyInput) (domain.ToolPolicy, error)
	DeleteToolPolicy(ctx context.Context, actor domain.Actor, companyID, policyID domain.ID) error
	ListToolPolicies(ctx context.Context, companyID domain.ID, personaID *domain.ID) ([]domain.ToolPolicy, error)
	TestToolCall(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.ToolTestInput) (service.ToolTestResult, error)
	RunToolCaller(ctx context.Context, companyID, runID domain.ID) (service.ToolCaller, error)
	NativeToolCheck(ctx context.Context, c service.ToolCaller, req service.NativeToolRequest) (service.NativeToolDecision, error)
	NativeToolGateState(ctx context.Context, c service.ToolCaller, gateID domain.ID) (service.NativeGateState, error)
}

func (s *server) toolPolicies() (toolPolicyBackend, error) {
	b, ok := s.svc.(toolPolicyBackend)
	if !ok {
		return nil, problem(http.StatusNotImplemented, "política de tools indisponível neste servidor")
	}
	return b, nil
}

// ToolPolicy é uma política off/ask/allow.
type ToolPolicy struct {
	ID            string    `json:"id" format:"uuid"`
	Scope         string    `json:"scope" enum:"company,persona" doc:"company: padrão da empresa; persona: exceção de uma persona (vence a da empresa)."`
	PersonaID     string    `json:"persona_id,omitempty" format:"uuid"`
	Persona       string    `json:"persona,omitempty"`
	Target        string    `json:"target" enum:"all,integration,tool,native" doc:"all: qualquer tool de integração e do harness; integration: a integração inteira; tool: uma tool de integração; native: tool do harness (Bash, Edit...) ou de plugin, pelo nome."`
	IntegrationID string    `json:"integration_id,omitempty" format:"uuid"`
	Integration   string    `json:"integration,omitempty"`
	Tool          string    `json:"tool,omitempty"`
	Mode          string    `json:"mode" enum:"off,ask,allow" doc:"off: recusa; ask: pergunta antes (gate tool); allow: passa direto."`
	Origin        string    `json:"origin" enum:"manual,always" doc:"always: nasceu de \"sempre permitir\" na decisão de um gate."`
	CreatedBy     ActorRef  `json:"created_by"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func policyOut(p domain.ToolPolicy) ToolPolicy {
	return ToolPolicy{ID: idStr(p.ID), Scope: string(p.Scope), PersonaID: optIDStr(p.PersonaID), Persona: p.PersonaName, Target: string(p.Target),
		IntegrationID: optIDStr(p.IntegrationID), Integration: p.IntegrationSlug, Tool: p.ToolName, Mode: string(p.Mode), Origin: string(p.Origin),
		CreatedBy: actorRef(p.CreatedBy), CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt}
}

// ToolTestResult é o que uma persona veria ao chamar a tool.
type ToolTestResult struct {
	Persona     string      `json:"persona"`
	Integration string      `json:"integration"`
	Tool        string      `json:"tool"`
	Status      string      `json:"status,omitempty" enum:"active,quarantined,disabled,removed" doc:"Estado da tool no catálogo."`
	Risk        string      `json:"risk,omitempty" enum:"read,write,destructive,unknown"`
	Verdict     ToolVerdict `json:"verdict" doc:"A decisão do perfil e das políticas, e de onde veio."`
	Decision    string      `json:"decision" enum:"allow,deny,ask,quarantined,error"`
	Message     string      `json:"message,omitempty"`
	GateID      string      `json:"gate_id,omitempty" format:"uuid"`
	Executed    bool        `json:"executed" doc:"A chamada foi repassada ao servidor (execute=true)."`
	IsError     bool        `json:"is_error,omitempty"`
	Output      string      `json:"output,omitempty" doc:"Trecho do resultado, sem segredos."`
}

func testOut(r service.ToolTestResult) ToolTestResult {
	return ToolTestResult{Persona: r.Persona, Integration: r.Integration, Tool: r.Tool, Status: string(r.Status), Risk: string(r.Risk),
		Verdict: verdictOut(r.Verdict), Decision: string(r.Decision), Message: r.Message, GateID: optIDStr(r.GateID), Executed: r.Executed,
		IsError: r.IsError, Output: r.Output}
}

// ToolPermissionDecision é a resposta do hook de permissão.
type ToolPermissionDecision struct {
	Decision string `json:"decision" enum:"allow,deny,ask,none" doc:"none: nenhuma política alcança a chamada; ask: espere o gate (GET tool-permission/{gate})."`
	GateID   string `json:"gate_id,omitempty" format:"uuid"`
	Reason   string `json:"reason,omitempty"`
}

// ToolPermissionState é o estado do gate de uma chamada nativa.
type ToolPermissionState struct {
	State  string `json:"state" enum:"pending,allow,deny"`
	Reason string `json:"reason,omitempty"`
}

type toolPolicyPath struct {
	CompanyPath
	Policy string `path:"policy" format:"uuid" doc:"Id da política."`
}

type listToolPoliciesIn struct {
	CompanyPath
	Persona string `query:"persona" doc:"Id ou slug da persona: as políticas dela."`
	Page
}

type setToolPolicyIn struct {
	CompanyPath
	Body struct {
		Scope       string `json:"scope,omitempty" enum:"company,persona" doc:"Padrão: persona, quando há persona; senão company."`
		Persona     string `json:"persona,omitempty" doc:"Id ou slug da persona (escopo persona)."`
		Target      string `json:"target" enum:"all,integration,tool,native"`
		Integration string `json:"integration,omitempty" doc:"Id ou slug da integração (alvos integration e tool)."`
		Tool        string `json:"tool,omitempty" maxLength:"128" doc:"Nome da tool (alvos tool e native)."`
		Mode        string `json:"mode" enum:"off,ask,allow"`
	}
}

type testToolIn struct {
	IntegrationToolPath
	Body struct {
		Persona   string         `json:"persona" doc:"Id ou slug da persona cuja visão será usada."`
		Arguments map[string]any `json:"arguments,omitempty" doc:"Argumentos da chamada."`
		Execute   bool           `json:"execute,omitempty" doc:"Chama de verdade (passa pelo gate e entra na auditoria com origem test). Sem isso, só simula a decisão."`
	}
}

type toolPermissionIn struct {
	CompanyPath
	Body struct {
		Tool  string         `json:"tool" minLength:"1" maxLength:"128" doc:"Nome da tool nativa (Bash, Edit...)."`
		Input map[string]any `json:"input,omitempty"`
	}
}

type toolPermissionStateIn struct {
	CompanyPath
	Gate string `path:"gate" format:"uuid" doc:"Id do gate aberto pela chamada."`
}

func (s *server) registerToolPolicies(api huma.API) {
	prelude := func(ctx context.Context, company string, write bool) (toolPolicyBackend, scope, error) {
		if write {
			if err := forbidPersona(ctx); err != nil {
				return nil, scope{}, err
			}
		}
		tb, err := s.toolPolicies()
		if err != nil {
			return nil, scope{}, err
		}
		sc, err := s.scope(ctx, company)
		return tb, sc, err
	}

	huma.Register(api, op("list-tool-policies", http.MethodGet, "/companies/{company}/tool-policies", "tools",
		"Políticas de tools (off/ask/allow por persona e padrão da empresa)"),
		func(ctx context.Context, in *listToolPoliciesIn) (*ListOut[ToolPolicy], error) {
			tb, sc, err := prelude(ctx, in.Company, false)
			if err != nil {
				return nil, err
			}
			pid, err := s.personaRef(ctx, sc.Company.ID, in.Persona)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			list, err := tb.ListToolPolicies(ctx, sc.Company.ID, pid)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listOf(list, in.Page, policyOut), nil
		})

	huma.Register(api, op("set-tool-policy", http.MethodPut, "/companies/{company}/tool-policies", "tools",
		"Grava (ou troca) uma política de tools; admin"),
		func(ctx context.Context, in *setToolPolicyIn) (*ItemOut[ToolPolicy], error) {
			tb, sc, err := prelude(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			pid, err := s.personaRef(ctx, sc.Company.ID, in.Body.Persona)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			scopeName := domain.ToolPolicyScope(in.Body.Scope)
			if scopeName == "" {
				scopeName = domain.PolicyScopeCompany
				if pid != nil {
					scopeName = domain.PolicyScopePersona
				}
			}
			var iid *domain.ID
			if in.Body.Integration != "" {
				id, err := s.integrationID(ctx, sc.Company.ID, in.Body.Integration)
				if err != nil {
					return nil, err
				}
				iid = &id
			}
			p, err := tb.SetToolPolicy(ctx, sc.Actor, sc.Company.ID, service.ToolPolicyInput{Scope: scopeName, PersonaID: pid,
				Target: domain.ToolPolicyTarget(in.Body.Target), IntegrationID: iid, ToolName: in.Body.Tool, Mode: domain.ToolPolicyMode(in.Body.Mode)})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[ToolPolicy]{Body: policyOut(p)}, nil
		})

	huma.Register(api, noContent(op("delete-tool-policy", http.MethodDelete, "/companies/{company}/tool-policies/{policy}", "tools",
		"Apaga uma política de tools; admin")),
		func(ctx context.Context, in *toolPolicyPath) (*struct{}, error) {
			tb, sc, err := prelude(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			id, err := domain.ParseID(in.Policy)
			if err != nil {
				return nil, problem(http.StatusNotFound, "política não encontrada")
			}
			if err := tb.DeleteToolPolicy(ctx, sc.Actor, sc.Company.ID, id); err != nil {
				return nil, s.fail(ctx, err)
			}
			return nil, nil
		})

	huma.Register(api, op("test-integration-tool", http.MethodPost, "/companies/{company}/integrations/{integration}/tools/{tool}/test", "tools",
		"Testa uma tool como uma persona (simula a decisão ou chama de verdade); admin"),
		func(ctx context.Context, in *testToolIn) (*ItemOut[ToolTestResult], error) {
			tb, sc, err := prelude(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			iid, err := s.integrationID(ctx, sc.Company.ID, in.Integration)
			if err != nil {
				return nil, err
			}
			pid, err := s.personaRef(ctx, sc.Company.ID, in.Body.Persona)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			if pid == nil {
				return nil, problem(http.StatusUnprocessableEntity, "persona é obrigatória")
			}
			var args json.RawMessage
			if in.Body.Arguments != nil {
				if args, err = json.Marshal(in.Body.Arguments); err != nil {
					return nil, problem(http.StatusUnprocessableEntity, "arguments inválidos")
				}
			}
			r, err := tb.TestToolCall(ctx, sc.Actor, sc.Company.ID, service.ToolTestInput{IntegrationID: iid, Tool: in.Tool, PersonaID: *pid,
				Arguments: args, Execute: in.Body.Execute})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[ToolTestResult]{Body: testOut(r)}, nil
		})

	// run token only: o hook PreToolUse do claude
	runCaller := func(ctx context.Context, company string) (toolPolicyBackend, scope, service.ToolCaller, error) {
		tb, err := s.toolPolicies()
		if err != nil {
			return nil, scope{}, service.ToolCaller{}, err
		}
		sc, err := s.scope(ctx, company)
		if err != nil {
			return nil, scope{}, service.ToolCaller{}, err
		}
		if sc.Claims == nil {
			return nil, scope{}, service.ToolCaller{}, problem(http.StatusForbidden, "só a execução da persona (run token) consulta a política de tools")
		}
		rid, err := domain.ParseID(sc.Claims.RunID)
		if err != nil {
			return nil, scope{}, service.ToolCaller{}, problem(http.StatusUnauthorized, "run token inválido")
		}
		c, err := tb.RunToolCaller(ctx, sc.Company.ID, rid)
		if err != nil {
			return nil, scope{}, service.ToolCaller{}, s.fail(ctx, err)
		}
		c.Source = domain.SourceHook
		return tb, sc, c, nil
	}

	huma.Register(api, op("tool-permission", http.MethodPost, "/companies/{company}/tool-permission", "tools",
		"Consulta a política de tools para uma tool nativa do harness (hook PreToolUse); run token"),
		func(ctx context.Context, in *toolPermissionIn) (*ItemOut[ToolPermissionDecision], error) {
			tb, _, c, err := runCaller(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			var input json.RawMessage
			if in.Body.Input != nil {
				input, _ = json.Marshal(in.Body.Input)
			}
			d, err := tb.NativeToolCheck(ctx, c, service.NativeToolRequest{Name: in.Body.Tool, Input: input})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[ToolPermissionDecision]{Body: ToolPermissionDecision{Decision: d.Decision, GateID: optIDStr(d.GateID), Reason: d.Reason}}, nil
		})

	huma.Register(api, op("tool-permission-state", http.MethodGet, "/companies/{company}/tool-permission/{gate}", "tools",
		"Estado do gate de uma chamada nativa (o hook consulta até sair de pending); run token"),
		func(ctx context.Context, in *toolPermissionStateIn) (*ItemOut[ToolPermissionState], error) {
			tb, _, c, err := runCaller(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			gid, err := domain.ParseID(in.Gate)
			if err != nil {
				return nil, problem(http.StatusNotFound, "gate não encontrado")
			}
			st, err := tb.NativeToolGateState(ctx, c, gid)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[ToolPermissionState]{Body: ToolPermissionState{State: st.State, Reason: st.Reason}}, nil
		})
}
