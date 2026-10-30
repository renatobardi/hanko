package api

import (
	"context"
	"math"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Agentes e hosts externos (ADR-0075 item 2; ADR-0077 itens 3 e 4): listar, ver, alterar e juntar. Leitura é de
// qualquer membro; alteração e junção são de owner ou admin (o serviço confere).

func (s *server) registerExternalEntities(api huma.API) {
	const tag = "external-source"
	b := "/companies/{company}"

	huma.Register(api, op("list-external-agents", http.MethodGet, b+"/external-agents", tag, "Lista os agentes externos"),
		s.listExternalAgents)
	huma.Register(api, op("get-external-agent", http.MethodGet, b+"/external-agents/{agent}", tag, "Mostra um agente externo"),
		s.getExternalAgent)
	huma.Register(api, op("update-external-agent", http.MethodPatch, b+"/external-agents/{agent}", tag,
		"Renomeia, arquiva ou volta um agente externo (owner ou admin)"), s.updateExternalAgent)
	huma.Register(api, op("merge-external-agent", http.MethodPost, b+"/external-agents/{agent}/merge", tag,
		"Junta este agente a outro; o passado dele passa para o que fica (owner ou admin)"), s.mergeExternalAgent)

	huma.Register(api, op("list-external-hosts", http.MethodGet, b+"/external-hosts", tag,
		"Lista os hosts externos, com o último dado e há quanto tempo"), s.listExternalHosts)
	huma.Register(api, op("get-external-host", http.MethodGet, b+"/external-hosts/{host}", tag, "Mostra um host externo"),
		s.getExternalHost)
	huma.Register(api, op("update-external-host", http.MethodPatch, b+"/external-hosts/{host}", tag,
		"Renomeia, marca \"sempre ligado\", arquiva ou volta um host externo (owner ou admin)"), s.updateExternalHost)
	huma.Register(api, op("merge-external-host", http.MethodPost, b+"/external-hosts/{host}/merge", tag,
		"Junta este host a outro; o passado dele passa para o que fica (owner ou admin)"), s.mergeExternalHost)
}

// externalScope é a empresa e o ator da rota; persona não usa a fonte externa.
func (s *server) externalScope(ctx context.Context, company string) (scope, error) {
	if err := forbidPersona(ctx); err != nil {
		return scope{}, err
	}
	return s.scope(ctx, company)
}

// pageInt32 limita a paginação ao alcance de int32 (o banco recebe int32).
func pageInt32(n int) int32 {
	if n > math.MaxInt32 {
		return math.MaxInt32
	}
	if n < 0 {
		return 0
	}
	return int32(n)
}

func agentOut(v service.ExternalAgentView) ExternalAgent {
	aliases := v.Aliases
	if aliases == nil {
		aliases = []string{}
	}
	return ExternalAgent{ID: v.ID.String(), Name: v.Name, Aliases: aliases, SourceKind: "external", ArchivedAt: v.ArchivedAt,
		CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
}

func hostOut(v service.ExternalHostView) ExternalHost {
	aliases := v.Aliases
	if aliases == nil {
		aliases = []string{}
	}
	return ExternalHost{ID: v.ID.String(), Name: v.Name, Aliases: aliases, SourceKind: "external", AlwaysOn: v.AlwaysOn,
		LastSeenAt: v.LastSeenAt, ArchivedAt: v.ArchivedAt, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
}

func (s *server) listExternalAgents(ctx context.Context, in *listExternalNamedIn) (*ListOut[ExternalAgent], error) {
	sc, err := s.externalScope(ctx, in.Company)
	if err != nil {
		return nil, err
	}
	rows, err := s.svc.ListExternalAgents(ctx, sc.Company.ID, in.IncludeArchived, pageInt32(in.Limit), pageInt32(in.Offset))
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	items := make([]ExternalAgent, 0, len(rows))
	for _, r := range rows {
		items = append(items, agentOut(r))
	}
	return &ListOut[ExternalAgent]{Body: ListBody[ExternalAgent]{Items: items, Limit: in.Limit, Offset: in.Offset}}, nil
}

func (s *server) getExternalAgent(ctx context.Context, in *ExternalAgentPath) (*ItemOut[ExternalAgent], error) {
	sc, err := s.externalScope(ctx, in.Company)
	if err != nil {
		return nil, err
	}
	id, perr := domain.ParseID(in.Agent)
	if perr != nil {
		return nil, problem(http.StatusNotFound, "agente não encontrado")
	}
	v, err := s.svc.GetExternalAgent(ctx, sc.Company.ID, id)
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	return &ItemOut[ExternalAgent]{Body: agentOut(v)}, nil
}

func (s *server) updateExternalAgent(ctx context.Context, in *updateExternalAgentIn) (*ItemOut[ExternalAgent], error) {
	sc, err := s.externalScope(ctx, in.Company)
	if err != nil {
		return nil, err
	}
	id, perr := domain.ParseID(in.Agent)
	if perr != nil {
		return nil, problem(http.StatusNotFound, "agente não encontrado")
	}
	v, err := s.svc.UpdateExternalAgent(ctx, sc.Actor, sc.Company.ID, id, service.ExternalAgentPatch{Name: in.Body.Name, Archived: in.Body.Archived})
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	return &ItemOut[ExternalAgent]{Body: agentOut(v)}, nil
}

func (s *server) mergeExternalAgent(ctx context.Context, in *mergeExternalAgentIn) (*ItemOut[ExternalAgent], error) {
	sc, err := s.externalScope(ctx, in.Company)
	if err != nil {
		return nil, err
	}
	from, perr := domain.ParseID(in.Agent)
	into, ierr := domain.ParseID(in.Body.Into)
	if perr != nil {
		return nil, problem(http.StatusNotFound, "agente não encontrado")
	}
	if ierr != nil {
		return nil, problem(http.StatusUnprocessableEntity, "into: um id de agente")
	}
	v, err := s.svc.MergeExternalAgent(ctx, sc.Actor, sc.Company.ID, from, into)
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	return &ItemOut[ExternalAgent]{Body: agentOut(v)}, nil
}

func (s *server) listExternalHosts(ctx context.Context, in *listExternalNamedIn) (*ListOut[ExternalHost], error) {
	sc, err := s.externalScope(ctx, in.Company)
	if err != nil {
		return nil, err
	}
	rows, err := s.svc.ListExternalHosts(ctx, sc.Company.ID, in.IncludeArchived, pageInt32(in.Limit), pageInt32(in.Offset))
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	items := make([]ExternalHost, 0, len(rows))
	for _, r := range rows {
		items = append(items, hostOut(r))
	}
	return &ListOut[ExternalHost]{Body: ListBody[ExternalHost]{Items: items, Limit: in.Limit, Offset: in.Offset}}, nil
}

func (s *server) getExternalHost(ctx context.Context, in *ExternalHostPath) (*ItemOut[ExternalHost], error) {
	sc, err := s.externalScope(ctx, in.Company)
	if err != nil {
		return nil, err
	}
	id, perr := domain.ParseID(in.Host)
	if perr != nil {
		return nil, problem(http.StatusNotFound, "host não encontrado")
	}
	v, err := s.svc.GetExternalHost(ctx, sc.Company.ID, id)
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	return &ItemOut[ExternalHost]{Body: hostOut(v)}, nil
}

func (s *server) updateExternalHost(ctx context.Context, in *updateExternalHostIn) (*ItemOut[ExternalHost], error) {
	sc, err := s.externalScope(ctx, in.Company)
	if err != nil {
		return nil, err
	}
	id, perr := domain.ParseID(in.Host)
	if perr != nil {
		return nil, problem(http.StatusNotFound, "host não encontrado")
	}
	v, err := s.svc.UpdateExternalHost(ctx, sc.Actor, sc.Company.ID, id,
		service.ExternalHostPatch{Name: in.Body.Name, AlwaysOn: in.Body.AlwaysOn, Archived: in.Body.Archived})
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	return &ItemOut[ExternalHost]{Body: hostOut(v)}, nil
}

func (s *server) mergeExternalHost(ctx context.Context, in *mergeExternalHostIn) (*ItemOut[ExternalHost], error) {
	sc, err := s.externalScope(ctx, in.Company)
	if err != nil {
		return nil, err
	}
	from, perr := domain.ParseID(in.Host)
	into, ierr := domain.ParseID(in.Body.Into)
	if perr != nil {
		return nil, problem(http.StatusNotFound, "host não encontrado")
	}
	if ierr != nil {
		return nil, problem(http.StatusUnprocessableEntity, "into: um id de host")
	}
	v, err := s.svc.MergeExternalHost(ctx, sc.Actor, sc.Company.ID, from, into)
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	return &ItemOut[ExternalHost]{Body: hostOut(v)}, nil
}
