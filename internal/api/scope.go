package api

import (
	"context"
	"fmt"
	"net/http"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// scope é o contexto resolvido de uma requisição dentro de uma empresa.
type scope struct {
	Company domain.Company
	Actor   domain.Actor
	Claims  *auth.RunClaims
}

// scope resolve {company} (id ou slug) e o ator. Com run token, a empresa precisa
// ser a do token, senão 403.
func (s *server) scope(ctx context.Context, ref string) (scope, error) {
	c, err := s.svc.GetCompany(ctx, ref)
	if err != nil {
		return scope{}, s.fail(ctx, err)
	}
	p := principalFrom(ctx)
	if p.Claims != nil && p.Claims.CompanyID != c.ID.String() {
		return scope{}, problem(http.StatusForbidden, "o token não pertence a esta empresa")
	}
	return scope{Company: c, Actor: p.Actor, Claims: p.Claims}, nil
}

// forbidPersona rejeita escritas globais (fora de uma empresa) feitas por persona.
func forbidPersona(ctx context.Context) error {
	if principalFrom(ctx).Claims != nil {
		return problem(http.StatusForbidden, "run tokens não podem executar esta operação")
	}
	return nil
}

// optID resolve uma referência opcional ("" = ausente) usando get.
func optID[T any](ctx context.Context, s *server, ref string, get func(context.Context, string) (T, error), id func(T) domain.ID) (*domain.ID, error) {
	if ref == "" {
		return nil, nil
	}
	v, err := get(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("resolver %q: %w", ref, err)
	}
	i := id(v)
	return &i, nil
}

func (s *server) personaRef(ctx context.Context, cid domain.ID, ref string) (*domain.ID, error) {
	return optID(ctx, s, ref, func(ctx context.Context, r string) (domain.Persona, error) {
		return s.svc.GetPersona(ctx, cid, r)
	}, func(p domain.Persona) domain.ID { return p.ID })
}

func (s *server) projectRef(ctx context.Context, cid domain.ID, ref string) (*domain.ID, error) {
	return optID(ctx, s, ref, func(ctx context.Context, r string) (domain.Project, error) {
		return s.svc.GetProject(ctx, cid, r)
	}, func(p domain.Project) domain.ID { return p.ID })
}

func (s *server) workspaceRef(ctx context.Context, cid domain.ID, ref string) (*domain.ID, error) {
	return optID(ctx, s, ref, func(ctx context.Context, r string) (domain.Workspace, error) {
		return s.svc.GetWorkspace(ctx, cid, r)
	}, func(w domain.Workspace) domain.ID { return w.ID })
}

func (s *server) taskRef(ctx context.Context, cid domain.ID, ref string) (*domain.ID, error) {
	return optID(ctx, s, ref, func(ctx context.Context, r string) (service.TaskView, error) {
		return s.svc.GetTask(ctx, cid, r)
	}, func(t service.TaskView) domain.ID { return t.ID })
}
