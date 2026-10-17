package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/textdiff"
)

// Revisões das instruções da persona (ADR-0052). Ler é de qualquer membro; restaurar é de admin (o service confere).
// A persona (run token) pode propor uma mudança para si mesma, que vira um gate kind=instruction.

type instructionsBackend interface {
	ListInstructionRevisions(ctx context.Context, companyID, personaID domain.ID) ([]domain.InstructionRevision, error)
	GetInstructionRevision(ctx context.Context, companyID, personaID domain.ID, revision int) (domain.InstructionRevision, error)
	DiffInstructionRevisions(ctx context.Context, companyID, personaID domain.ID, from, to int) (textdiff.Result, error)
	RestoreInstructionRevision(ctx context.Context, actor domain.Actor, companyID, personaID domain.ID, revision int, reason string) (domain.Persona, error)
	ProposePersonaInstructions(ctx context.Context, actor domain.Actor, companyID, personaID domain.ID, in service.ProposeInstructionsInput) (service.GateView, error)
}

func (s *server) instructions() (instructionsBackend, error) {
	b, ok := s.svc.(instructionsBackend)
	if !ok {
		return nil, problem(http.StatusNotImplemented, "revisões de instruções indisponíveis neste servidor")
	}
	return b, nil
}

// InstructionRevision é uma revisão imutável das instruções de uma persona.
type InstructionRevision struct {
	Revision     int       `json:"revision" doc:"Número da revisão, a partir de 1."`
	SHA256       string    `json:"sha256" doc:"sha256 (hex) do texto."`
	Author       ActorRef  `json:"author" doc:"Quem escreveu o texto (um humano, ou a persona que propôs)."`
	Actor        ActorRef  `json:"actor" doc:"Quem aplicou a revisão (o humano que editou, restaurou ou aprovou)."`
	Reason       string    `json:"reason,omitempty"`
	Origin       string    `json:"origin" enum:"backfill,hire,edit,restore,proposal,import"`
	RestoredFrom int       `json:"restored_from,omitempty" doc:"Em origin=restore: a revisão cujo texto voltou."`
	GateID       string    `json:"gate_id,omitempty" format:"uuid" doc:"Em origin=proposal: o gate aprovado."`
	BodyBytes    int       `json:"body_bytes"`
	Body         string    `json:"body,omitempty" doc:"O texto; ausente nas listagens."`
	CreatedAt    time.Time `json:"created_at"`
	Current      bool      `json:"current,omitempty" doc:"Verdadeiro na revisão vigente."`
	// Diff só vem com ?against=N: as linhas de N para esta revisão.
	Diff *ArtifactDiff `json:"diff,omitempty"`
}

func instructionRevisionOut(v domain.InstructionRevision) InstructionRevision {
	o := InstructionRevision{Revision: v.Revision, SHA256: v.SHA256, Author: actorRef(v.Author), Actor: actorRef(v.Actor), Reason: v.Reason,
		Origin: string(v.Origin), GateID: idPtr(v.GateID), BodyBytes: v.BodyBytes, Body: v.Body, CreatedAt: v.CreatedAt}
	if v.RestoredFrom != nil {
		o.RestoredFrom = *v.RestoredFrom
	}
	return o
}

type instructionRevisionPath struct {
	PersonaPath
	Revision int `path:"revision" minimum:"1" doc:"Número da revisão."`
	Against  int `query:"against" minimum:"0" doc:"Inclui o diff desta revisão para a pedida."`
}

type restoreInstructionsIn struct {
	PersonaPath
	Body struct {
		Revision int    `json:"revision" minimum:"1" doc:"Revisão cujo texto volta; vira uma revisão nova."`
		Reason   string `json:"reason,omitempty" maxLength:"500"`
	}
}

type proposeInstructionsIn struct {
	PersonaPath
	Body struct {
		Instructions string `json:"instructions" minLength:"1" doc:"O texto novo e completo das instruções."`
		Reason       string `json:"reason,omitempty" maxLength:"500" doc:"Por que mudar."`
		EventID      string `json:"event_id,omitempty" doc:"Chave de idempotência: repetir devolve o gate existente."`
	}
}

func (s *server) registerInstructions(api huma.API) {
	type ctxPersona struct {
		ib instructionsBackend
		sc scope
		p  domain.Persona
	}
	with := func(ctx context.Context, company, persona string, write bool) (ctxPersona, error) {
		if write {
			if err := forbidPersona(ctx); err != nil {
				return ctxPersona{}, err
			}
		}
		ib, err := s.instructions()
		if err != nil {
			return ctxPersona{}, err
		}
		sc, err := s.scope(ctx, company)
		if err != nil {
			return ctxPersona{}, err
		}
		p, err := s.svc.GetPersona(ctx, sc.Company.ID, persona)
		if err != nil {
			return ctxPersona{}, s.fail(ctx, err)
		}
		return ctxPersona{ib: ib, sc: sc, p: p}, nil
	}

	huma.Register(api, op("list-instruction-revisions", http.MethodGet, "/companies/{company}/personas/{persona}/instructions/revisions", "personas",
		"Lista as revisões das instruções da persona (mais nova primeiro)"),
		func(ctx context.Context, in *PersonaPath) (*ListOut[InstructionRevision], error) {
			c, err := with(ctx, in.Company, in.Persona, false)
			if err != nil {
				return nil, err
			}
			vs, err := c.ib.ListInstructionRevisions(ctx, c.sc.Company.ID, c.p.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			out := listAll(vs, Page{Limit: len(vs)}, instructionRevisionOut)
			for i := range out.Body.Items {
				out.Body.Items[i].Current = out.Body.Items[i].Revision == c.p.InstructionRevision
			}
			return out, nil
		})

	huma.Register(api, op("get-instruction-revision", http.MethodGet, "/companies/{company}/personas/{persona}/instructions/revisions/{revision}", "personas",
		"Obtém uma revisão das instruções, com o texto (e o diff com ?against)"),
		func(ctx context.Context, in *instructionRevisionPath) (*ItemOut[InstructionRevision], error) {
			c, err := with(ctx, in.Company, in.Persona, false)
			if err != nil {
				return nil, err
			}
			v, err := c.ib.GetInstructionRevision(ctx, c.sc.Company.ID, c.p.ID, in.Revision)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			out := instructionRevisionOut(v)
			out.Current = v.Revision == c.p.InstructionRevision
			if in.Against > 0 {
				d, err := c.ib.DiffInstructionRevisions(ctx, c.sc.Company.ID, c.p.ID, in.Against, in.Revision)
				if err != nil {
					return nil, s.fail(ctx, err)
				}
				out.Diff = diffOut(in.Against, in.Revision, d)
			}
			return &ItemOut[InstructionRevision]{Body: out}, nil
		})

	huma.Register(api, op("restore-instruction-revision", http.MethodPost, "/companies/{company}/personas/{persona}/instructions/restore", "personas",
		"Restaura o texto de uma revisão (cria uma revisão nova; admin)"),
		func(ctx context.Context, in *restoreInstructionsIn) (*ItemOut[Persona], error) {
			c, err := with(ctx, in.Company, in.Persona, true)
			if err != nil {
				return nil, err
			}
			p, err := c.ib.RestoreInstructionRevision(ctx, c.sc.Actor, c.sc.Company.ID, c.p.ID, in.Body.Revision, in.Body.Reason)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Persona]{Body: personaOut(p)}, nil
		})

	huma.Register(api, created(op("propose-persona-instructions", http.MethodPost, "/companies/{company}/personas/{persona}/instructions/proposals", "personas",
		"A própria persona propõe mudar as instruções dela (abre um gate kind=instruction; run token)")),
		func(ctx context.Context, in *proposeInstructionsIn) (*ItemOut[Gate], error) {
			c, err := with(ctx, in.Company, in.Persona, false)
			if err != nil {
				return nil, err
			}
			g, err := c.ib.ProposePersonaInstructions(ctx, c.sc.Actor, c.sc.Company.ID, c.p.ID, service.ProposeInstructionsInput{
				Body: in.Body.Instructions, Reason: in.Body.Reason, EventID: in.Body.EventID})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Gate]{Body: gateOut(g)}, nil
		})
}
