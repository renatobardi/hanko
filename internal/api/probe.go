package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
)

// PersonaProbe de prontidão do harness da persona (ADR-0056): passos nomeados, sem prompt e sem custo de modelo.

type probeBackend interface {
	ProbePersona(ctx context.Context, companyID domain.ID, ref string) (adapter.ProbeResult, error)
}

// PersonaProbeStep é um passo do probe.
type PersonaProbeStep struct {
	Name   string `json:"name" example:"autenticação" doc:"binário, protocolo, autenticação, endpoint, modelo..."`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty" doc:"Motivo da falha ou informação do passo; nunca segredo."`
}

// PersonaProbe é o resultado: ok só se todos os passos passaram.
type PersonaProbe struct {
	OK    bool               `json:"ok"`
	Steps []PersonaProbeStep `json:"steps"`
}

func (s *server) registerProbe(api huma.API) {
	huma.Register(api, op("probe-persona", http.MethodPost, "/companies/{company}/personas/{persona}/probe", "personas",
		"Confere se o harness da persona está pronto (binário, autenticação, endpoint), sem executar trabalho (admin)"),
		func(ctx context.Context, in *PersonaPath) (*ItemOut[PersonaProbe], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			pb, ok := s.svc.(probeBackend)
			if !ok {
				return nil, problem(http.StatusNotImplemented, "probe indisponível neste servidor")
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			r, err := pb.ProbePersona(ctx, sc.Company.ID, in.Persona)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			out := PersonaProbe{OK: r.OK, Steps: make([]PersonaProbeStep, len(r.Steps))}
			for i, st := range r.Steps {
				out.Steps[i] = PersonaProbeStep(st)
			}
			return &ItemOut[PersonaProbe]{Body: out}, nil
		})
}
