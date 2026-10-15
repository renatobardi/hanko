package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Consolidação de memória governada (ADR-0030): configuração, rodadas de curadoria, propostas retidas para
// decisão humana e permissões de memória por workspace.

// memoryBackend é a fatia do service que a API de memória usa. Fica fora do backend principal para que os
// fakes dos outros testes não precisem implementá-la; sem ela, as rotas respondem 501.
type memoryBackend interface {
	GetMemoryStatus(ctx context.Context, companyID domain.ID) (service.MemoryStatus, error)
	UpdateMemorySettings(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.UpdateMemorySettingsInput) (service.MemoryStatus, error)
	ConsolidateMemoryNow(ctx context.Context, actor domain.Actor, companyID domain.ID, workspaceRef string) ([]domain.MemoryConsolidation, error)
	ListMemoryConsolidations(ctx context.Context, companyID domain.ID, limit int) ([]service.MemoryConsolidationView, error)
	ListMemoryProposals(ctx context.Context, companyID domain.ID, statuses []domain.MemoryProposalStatus, limit int) ([]service.MemoryProposalView, error)
	GetMemoryProposal(ctx context.Context, companyID, id domain.ID) (service.MemoryProposalView, error)
	DecideMemoryProposal(ctx context.Context, actor domain.Actor, companyID, id domain.ID, in service.DecideGateInput) (service.MemoryProposalView, error)
	RetryMemoryProposal(ctx context.Context, actor domain.Actor, companyID, id domain.ID) (service.MemoryProposalView, error)
	GrantMemoryAccess(ctx context.Context, actor domain.Actor, companyID, personaID, workspaceID domain.ID, access domain.MemoryAccess) (domain.MemoryGrant, error)
	RevokeMemoryAccess(ctx context.Context, actor domain.Actor, companyID, personaID, workspaceID domain.ID) error
	ListMemoryGrants(ctx context.Context, companyID domain.ID) ([]domain.MemoryGrant, error)
}

var _ memoryBackend = (*service.Service)(nil)

func (s *server) mem() (memoryBackend, error) {
	m, ok := s.svc.(memoryBackend)
	if !ok {
		return nil, problem(http.StatusNotImplemented, "consolidação de memória indisponível neste servidor")
	}
	return m, nil
}

// MemorySettingsOut é a configuração da consolidação.
type MemorySettingsOut struct {
	Enabled       bool       `json:"enabled" doc:"Consolidação periódica ligada. Desligada por padrão."`
	IntervalHours int        `json:"interval_hours" minimum:"1" maximum:"720" doc:"Horas entre as rodadas."`
	CuratorID     string     `json:"curator_persona_id,omitempty" format:"uuid"`
	CuratorSlug   string     `json:"curator_slug,omitempty"`
	CuratorName   string     `json:"curator_name,omitempty"`
	LastFiredAt   *time.Time `json:"last_fired_at,omitempty"`
	NextFireAt    *time.Time `json:"next_fire_at,omitempty"`
}

// MemoryStatusOut é a memória da empresa: provedor, configuração e contagens.
type MemoryStatusOut struct {
	Provider         string            `json:"provider" example:"ai-memory" doc:"Provedor de memória; none = desligado (a consolidação não existe)."`
	Available        bool              `json:"available" doc:"Há provedor de memória."`
	Settings         MemorySettingsOut `json:"settings"`
	Workspaces       int               `json:"workspaces" doc:"Quantos workspaces a curadoria percorre."`
	PendingProposals int               `json:"pending_proposals" doc:"Propostas esperando decisão no Inbox."`
}

// MemoryConsolidationOut é uma rodada de curadoria de um workspace.
type MemoryConsolidationOut struct {
	ID            string     `json:"id" format:"uuid"`
	WorkspaceSlug string     `json:"workspace_slug"`
	Origin        string     `json:"origin" enum:"scheduled,manual"`
	Status        string     `json:"status" enum:"open,done,failed,cancelled,expired" doc:"open: task criada, a run não terminou; expired: nunca chegou a rodar."`
	ScheduledFor  time.Time  `json:"scheduled_for"`
	TaskID        string     `json:"task_id,omitempty" format:"uuid"`
	TaskKey       string     `json:"task_key,omitempty" example:"HNK-31"`
	RunID         string     `json:"run_id,omitempty" format:"uuid"`
	RunName       string     `json:"run_name,omitempty"`
	RunStatus     string     `json:"run_status,omitempty"`
	CostUSD       *float64   `json:"cost_usd,omitempty" doc:"Custo da run de curadoria (vai para o ledger como qualquer run)."`
	Proposals     int        `json:"proposals" doc:"Propostas geradas pela run."`
	Summary       string     `json:"summary,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
}

// MemoryProposalOut é uma escrita da curadoria retida para decisão humana.
type MemoryProposalOut struct {
	ID            string     `json:"id" format:"uuid"`
	WorkspaceSlug string     `json:"workspace_slug"`
	RunID         string     `json:"run_id,omitempty" format:"uuid"`
	GateID        string     `json:"gate_id,omitempty" format:"uuid" doc:"Gate do Inbox em que a proposta é decidida."`
	Kind          string     `json:"kind" enum:"merge,delete,rewrite"`
	Status        string     `json:"status" enum:"pending,approved,rejected,applied,failed" doc:"pending: espera decisão; approved: aplicação na fila; applied: feita no ai-memory; failed: a aplicação falhou, nova tentativa agendada."`
	Tool          string     `json:"tool" doc:"Tool do provedor de memória que será repetida ao aprovar."`
	Targets       []string   `json:"targets"`
	Proposed      string     `json:"proposed,omitempty"`
	Current       string     `json:"current,omitempty"`
	Diff          string     `json:"diff,omitempty"`
	SHA256        string     `json:"sha256" doc:"Hash da chamada que será aplicada: o que foi aprovado é exatamente isto."`
	Attempts      int        `json:"attempts"`
	LastError     string     `json:"last_error,omitempty"`
	NextAttemptAt *time.Time `json:"next_attempt_at,omitempty"`
	PersonaName   string     `json:"persona_name,omitempty"`
	RunLabel      string     `json:"run_name,omitempty"`
	DecidedBy     *ActorRef  `json:"decided_by,omitempty"`
	DecisionNote  string     `json:"decision_note,omitempty"`
	DecidedAt     *time.Time `json:"decided_at,omitempty"`
	AppliedAt     *time.Time `json:"applied_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// MemoryGrantOut libera uma persona a ler (ou ler e escrever) a memória de outro workspace.
type MemoryGrantOut struct {
	PersonaID     string    `json:"persona_id" format:"uuid"`
	PersonaName   string    `json:"persona_name"`
	WorkspaceID   string    `json:"workspace_id" format:"uuid"`
	WorkspaceSlug string    `json:"workspace_slug"`
	Access        string    `json:"access" enum:"read,write"`
	CreatedAt     time.Time `json:"created_at"`
}

func memorySettingsOut(st service.MemoryStatus) MemorySettingsOut {
	set := st.Settings
	return MemorySettingsOut{Enabled: set.Enabled, IntervalHours: set.IntervalHours, CuratorID: idPtr(set.CuratorPersonaID),
		CuratorSlug: st.CuratorSlug, CuratorName: st.CuratorName, LastFiredAt: set.LastFiredAt, NextFireAt: set.NextFireAt}
}

func memoryStatusOut(st service.MemoryStatus) MemoryStatusOut {
	return MemoryStatusOut{Provider: st.Provider, Available: st.Available, Settings: memorySettingsOut(st),
		Workspaces: st.Workspaces, PendingProposals: st.PendingProposals}
}

func memoryConsolidationOut(v service.MemoryConsolidationView) MemoryConsolidationOut {
	return MemoryConsolidationOut{ID: idStr(v.ID), WorkspaceSlug: v.WorkspaceSlug, Origin: string(v.Origin), Status: string(v.Status),
		ScheduledFor: v.ScheduledFor, TaskID: idPtr(v.TaskID), TaskKey: v.TaskKey, RunID: idPtr(v.RunID), RunName: v.RunName,
		RunStatus: v.RunStatus, CostUSD: v.CostUSD, Proposals: v.Proposals, Summary: v.Summary, CreatedAt: v.CreatedAt, FinishedAt: v.FinishedAt}
}

func memoryProposalOut(v service.MemoryProposalView) MemoryProposalOut {
	o := MemoryProposalOut{ID: idStr(v.ID), WorkspaceSlug: v.WorkspaceSlug, RunID: idPtr(v.RunID), GateID: idPtr(v.GateID),
		Kind: string(v.Kind), Status: string(v.Status), Tool: v.Tool, Targets: v.Targets, Proposed: v.Proposed, Current: v.Current,
		Diff: v.Diff, SHA256: v.SHA256, Attempts: v.Attempts, LastError: v.LastError, NextAttemptAt: v.NextAttemptAt,
		PersonaName: v.PersonaName, RunLabel: v.RunName, DecisionNote: v.DecisionNote, DecidedAt: v.DecidedAt,
		AppliedAt: v.AppliedAt, CreatedAt: v.CreatedAt}
	if o.Targets == nil {
		o.Targets = []string{}
	}
	if v.DecidedBy != nil {
		a := actorRef(*v.DecidedBy)
		o.DecidedBy = &a
	}
	return o
}

func memoryGrantOut(g domain.MemoryGrant) MemoryGrantOut {
	return MemoryGrantOut{PersonaID: idStr(g.PersonaID), PersonaName: g.PersonaName, WorkspaceID: idStr(g.WorkspaceID),
		WorkspaceSlug: g.WorkspaceSlug, Access: string(g.Access), CreatedAt: g.CreatedAt}
}

type updateMemoryIn struct {
	CompanyPath
	Body struct {
		Enabled       *bool  `json:"enabled,omitempty" doc:"Liga ou desliga a consolidação periódica. Ligar exige provedor de memória e curador."`
		IntervalHours *int   `json:"interval_hours,omitempty" minimum:"1" maximum:"720"`
		Curator       string `json:"curator,omitempty" doc:"Id ou slug da persona curadora (de preferência com um modelo barato)."`
		ClearCurator  bool   `json:"clear_curator,omitempty"`
	}
}

type consolidateMemoryIn struct {
	CompanyPath
	Body struct {
		Workspace string `json:"workspace,omitempty" doc:"Id ou slug do workspace. Vazio = todos."`
	}
}

type listMemoryRowsIn struct {
	CompanyPath
	Limit int `query:"limit" minimum:"1" maximum:"200" default:"30"`
}

type listMemoryProposalsIn struct {
	CompanyPath
	Status string `query:"status" doc:"Estados separados por vírgula: pending,approved,rejected,applied,failed. Vazio = todos."`
	Limit  int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

type MemoryProposalPath struct {
	CompanyPath
	Proposal string `path:"proposal" format:"uuid" doc:"Id da proposta."`
}

type decideMemoryProposalIn struct {
	MemoryProposalPath
	Body struct {
		Note string `json:"note,omitempty" doc:"Comentário da decisão; vai para a trilha de auditoria."`
	}
}

type grantMemoryIn struct {
	CompanyPath
	Body struct {
		Persona   string `json:"persona" minLength:"1" doc:"Id ou slug da persona."`
		Workspace string `json:"workspace" minLength:"1" doc:"Id ou slug do workspace cuja memória ela poderá acessar."`
		Access    string `json:"access" enum:"read,write"`
	}
}

type revokeMemoryIn struct {
	CompanyPath
	Persona   string `path:"persona" doc:"Id ou slug da persona."`
	Workspace string `path:"workspace" doc:"Id ou slug do workspace."`
}

func (s *server) memoryProposalID(ctx context.Context, raw string) (domain.ID, error) {
	id, err := domain.ParseID(raw)
	if err != nil {
		return domain.ID{}, s.fail(ctx, domain.Invalid("proposal", "não é um uuid"))
	}
	return id, nil
}

func (s *server) registerMemory(api huma.API) {
	huma.Register(api, op("get-memory", http.MethodGet, "/companies/{company}/memory", "memory", "Estado da memória e da consolidação"),
		func(ctx context.Context, in *CompanyPath) (*ItemOut[MemoryStatusOut], error) {
			m, err := s.mem()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			st, err := m.GetMemoryStatus(ctx, sc.Company.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[MemoryStatusOut]{Body: memoryStatusOut(st)}, nil
		})

	huma.Register(api, op("update-memory", http.MethodPatch, "/companies/{company}/memory", "memory",
		"Configura a consolidação (ligar, intervalo, curador); só administradores"),
		func(ctx context.Context, in *updateMemoryIn) (*ItemOut[MemoryStatusOut], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			m, err := s.mem()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			cur, err := s.personaRef(ctx, sc.Company.ID, in.Body.Curator)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			st, err := m.UpdateMemorySettings(ctx, sc.Actor, sc.Company.ID, service.UpdateMemorySettingsInput{
				Enabled: in.Body.Enabled, IntervalHours: in.Body.IntervalHours, CuratorPersonaID: cur, ClearCurator: in.Body.ClearCurator})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[MemoryStatusOut]{Body: memoryStatusOut(st)}, nil
		})

	huma.Register(api, op("consolidate-memory", http.MethodPost, "/companies/{company}/memory/consolidate", "memory",
		"Roda a consolidação agora (um workspace ou todos); só administradores"),
		func(ctx context.Context, in *consolidateMemoryIn) (*ListOut[MemoryConsolidationOut], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			m, err := s.mem()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			cs, err := m.ConsolidateMemoryNow(ctx, sc.Actor, sc.Company.ID, strings.TrimSpace(in.Body.Workspace))
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(cs, Page{Limit: len(cs)}, func(c domain.MemoryConsolidation) MemoryConsolidationOut {
				return memoryConsolidationOut(service.MemoryConsolidationView{MemoryConsolidation: c})
			}), nil
		})

	huma.Register(api, op("list-memory-consolidations", http.MethodGet, "/companies/{company}/memory/consolidations", "memory",
		"Histórico de consolidações (mais recentes primeiro)"),
		func(ctx context.Context, in *listMemoryRowsIn) (*ListOut[MemoryConsolidationOut], error) {
			m, err := s.mem()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			cs, err := m.ListMemoryConsolidations(ctx, sc.Company.ID, in.Limit)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(cs, Page{Limit: in.Limit}, memoryConsolidationOut), nil
		})

	huma.Register(api, op("list-memory-proposals", http.MethodGet, "/companies/{company}/memory/proposals", "memory",
		"Propostas de curadoria (pendentes primeiro)"),
		func(ctx context.Context, in *listMemoryProposalsIn) (*ListOut[MemoryProposalOut], error) {
			m, err := s.mem()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			var sts []domain.MemoryProposalStatus
			for _, part := range strings.Split(in.Status, ",") {
				part = strings.TrimSpace(part)
				if part == "" {
					continue
				}
				st := domain.MemoryProposalStatus(part)
				if !st.Valid() {
					return nil, s.fail(ctx, domain.Invalid("status", "valor inválido: "+part))
				}
				sts = append(sts, st)
			}
			ps, err := m.ListMemoryProposals(ctx, sc.Company.ID, sts, in.Limit)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(ps, Page{Limit: in.Limit}, memoryProposalOut), nil
		})

	huma.Register(api, op("get-memory-proposal", http.MethodGet, "/companies/{company}/memory/proposals/{proposal}", "memory",
		"Obtém uma proposta de curadoria, com o diff"),
		func(ctx context.Context, in *MemoryProposalPath) (*ItemOut[MemoryProposalOut], error) {
			m, err := s.mem()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			id, err := s.memoryProposalID(ctx, in.Proposal)
			if err != nil {
				return nil, err
			}
			p, err := m.GetMemoryProposal(ctx, sc.Company.ID, id)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[MemoryProposalOut]{Body: memoryProposalOut(p)}, nil
		})

	decide := func(id, path, summary string, decision domain.GateStatus) {
		huma.Register(api, op(id, http.MethodPost, "/companies/{company}/memory/proposals/{proposal}/"+path, "memory", summary),
			func(ctx context.Context, in *decideMemoryProposalIn) (*ItemOut[MemoryProposalOut], error) {
				if err := forbidPersona(ctx); err != nil {
					return nil, err
				}
				m, err := s.mem()
				if err != nil {
					return nil, err
				}
				sc, err := s.scope(ctx, in.Company)
				if err != nil {
					return nil, err
				}
				pid, err := s.memoryProposalID(ctx, in.Proposal)
				if err != nil {
					return nil, err
				}
				p, err := m.DecideMemoryProposal(ctx, sc.Actor, sc.Company.ID, pid, service.DecideGateInput{Decision: decision, Note: in.Body.Note})
				if err != nil {
					return nil, s.fail(ctx, err)
				}
				return &ItemOut[MemoryProposalOut]{Body: memoryProposalOut(p)}, nil
			})
	}
	decide("approve-memory-proposal", "approve", "Aprova uma proposta: o Hanko aplica a escrita no ai-memory (job com retry); só humanos", domain.GateApproved)
	decide("reject-memory-proposal", "reject", "Rejeita uma proposta: a escrita é descartada; só humanos", domain.GateRejected)

	huma.Register(api, op("retry-memory-proposal", http.MethodPost, "/companies/{company}/memory/proposals/{proposal}/retry", "memory",
		"Recoloca na fila a aplicação de uma proposta aprovada que falhou"),
		func(ctx context.Context, in *MemoryProposalPath) (*ItemOut[MemoryProposalOut], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			m, err := s.mem()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			pid, err := s.memoryProposalID(ctx, in.Proposal)
			if err != nil {
				return nil, err
			}
			p, err := m.RetryMemoryProposal(ctx, sc.Actor, sc.Company.ID, pid)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[MemoryProposalOut]{Body: memoryProposalOut(p)}, nil
		})

	huma.Register(api, op("list-memory-grants", http.MethodGet, "/companies/{company}/memory/grants", "memory",
		"Permissões de memória por workspace (além do workspace da própria run)"),
		func(ctx context.Context, in *CompanyPath) (*ListOut[MemoryGrantOut], error) {
			m, err := s.mem()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			gs, err := m.ListMemoryGrants(ctx, sc.Company.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(gs, Page{Limit: len(gs)}, memoryGrantOut), nil
		})

	huma.Register(api, op("grant-memory-access", http.MethodPut, "/companies/{company}/memory/grants", "memory",
		"Concede a uma persona leitura (ou leitura e escrita) na memória de outro workspace; só administradores"),
		func(ctx context.Context, in *grantMemoryIn) (*ItemOut[MemoryGrantOut], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			m, err := s.mem()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			pid, err := s.personaRef(ctx, sc.Company.ID, in.Body.Persona)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			wid, err := s.workspaceRef(ctx, sc.Company.ID, in.Body.Workspace)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			if pid == nil || wid == nil {
				return nil, s.fail(ctx, domain.Invalid("persona", "persona e workspace são obrigatórios"))
			}
			g, err := m.GrantMemoryAccess(ctx, sc.Actor, sc.Company.ID, *pid, *wid, domain.MemoryAccess(in.Body.Access))
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[MemoryGrantOut]{Body: memoryGrantOut(g)}, nil
		})

	huma.Register(api, func() huma.Operation {
		o := op("revoke-memory-access", http.MethodDelete, "/companies/{company}/memory/grants/{persona}/{workspace}", "memory",
			"Retira a permissão de memória de uma persona sobre um workspace; só administradores")
		o.DefaultStatus = http.StatusNoContent
		return o
	}(),
		func(ctx context.Context, in *revokeMemoryIn) (*struct{}, error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			m, err := s.mem()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			pid, err := s.personaRef(ctx, sc.Company.ID, in.Persona)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			wid, err := s.workspaceRef(ctx, sc.Company.ID, in.Workspace)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			if pid == nil || wid == nil {
				return nil, s.fail(ctx, domain.Invalid("persona", "persona e workspace são obrigatórios"))
			}
			if err := m.RevokeMemoryAccess(ctx, sc.Actor, sc.Company.ID, *pid, *wid); err != nil {
				return nil, s.fail(ctx, err)
			}
			return nil, nil
		})
}
