package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Emparelhamento de repositório com workspace e conteúdo completo da conversa externa (ADR-0075 itens 3 a 5; ADR-0076 item 9).

func (s *server) registerExternalPairing(api huma.API) {
	const tag = "external-source"
	b := "/companies/{company}"

	huma.Register(api, op("list-repo-pairings", http.MethodGet, b+"/repo-pairings", tag, "Lista os emparelhamentos de repositório com workspace"),
		s.listRepoPairings)
	huma.Register(api, op("update-repo-pairing", http.MethodPatch, b+"/repo-pairings/{pairing}", tag,
		"Troca o workspace de um emparelhamento; o passado vai junto (owner ou admin)"), s.updateRepoPairing)
	huma.Register(api, op("get-external-conversation-content", http.MethodGet, b+"/external-conversations/{conversation}/content", tag,
		"Mostra o conteúdo completo da conversa (owner ou admin; cada leitura gera activity_event)"), s.getConversationContent)
}

func repoPairingOut(v service.RepoPairingView) RepoPairing {
	return RepoPairing{ID: v.ID.String(), RepoKey: v.RepoKey, KeyKind: v.KeyKind, WorkspaceID: v.WorkspaceID.String(),
		WorkspaceSlug: v.WorkspaceSlug, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
}

func (s *server) listRepoPairings(ctx context.Context, in *listExternalNamedIn) (*ListOut[RepoPairing], error) {
	sc, err := s.externalScope(ctx, in.Company)
	if err != nil {
		return nil, err
	}
	rows, err := s.svc.ListRepoPairings(ctx, sc.Company.ID, in.Limit, in.Offset)
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	items := make([]RepoPairing, len(rows))
	for i, r := range rows {
		items[i] = repoPairingOut(r)
	}
	return &ListOut[RepoPairing]{Body: ListBody[RepoPairing]{Items: items, Limit: in.Limit, Offset: in.Offset}}, nil
}

func (s *server) updateRepoPairing(ctx context.Context, in *updateRepoPairingIn) (*ItemOut[RepoPairing], error) {
	sc, err := s.externalScope(ctx, in.Company)
	if err != nil {
		return nil, err
	}
	id, err := parseIDParam("emparelhamento", in.Pairing)
	if err != nil {
		return nil, err
	}
	ws, perr := domain.ParseID(in.Body.WorkspaceID)
	if perr != nil {
		return nil, problem(http.StatusUnprocessableEntity, "workspace_id não é um id")
	}
	v, err := s.svc.UpdateRepoPairing(ctx, sc.Actor, sc.Company.ID, id, ws)
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	return &ItemOut[RepoPairing]{Body: repoPairingOut(v)}, nil
}

func (s *server) getConversationContent(ctx context.Context, in *ExternalConversationPath) (*ItemOut[ConversationContent], error) {
	sc, err := s.externalScope(ctx, in.Company)
	if err != nil {
		return nil, err
	}
	id, err := parseIDParam("conversa", in.Conversation)
	if err != nil {
		return nil, err
	}
	rows, err := s.svc.GetExternalConversationContent(ctx, sc.Actor, sc.Company.ID, id)
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	items := make([]ConversationContentItem, len(rows))
	for i, r := range rows {
		items[i] = ConversationContentItem{Time: r.Time, Role: r.Role, Text: r.Text}
	}
	return &ItemOut[ConversationContent]{Body: ConversationContent{ConversationID: id.String(), Items: items}}, nil
}
