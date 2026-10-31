package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Leitura da fonte externa (ADR-0075 a 0079; fatia 1 da v0.8, #125): sessões, conversas, entradas da classificação, troca
// manual de classe e uso. Leitura é de qualquer membro; a troca manual é de owner ou admin (o serviço confere).

func (s *server) registerExternalRead(api huma.API) {
	const tag = "external-source"
	b := "/companies/{company}"

	huma.Register(api, op("list-external-sessions", http.MethodGet, b+"/external-sessions", tag, "Lista as sessões externas"), s.listExternalSessions)
	huma.Register(api, op("get-external-session", http.MethodGet, b+"/external-sessions/{session}", tag, "Mostra uma sessão externa"), s.getExternalSession)
	huma.Register(api, op("list-external-conversations", http.MethodGet, b+"/external-conversations", tag,
		"Lista as conversas externas, com filtro por host, agente, workspace, sessão, classe e marca"), s.listExternalConversations)
	huma.Register(api, op("get-external-conversation", http.MethodGet, b+"/external-conversations/{conversation}", tag, "Mostra uma conversa externa"),
		s.getExternalConversation)
	huma.Register(api, op("list-conversation-class-inputs", http.MethodGet, b+"/external-conversations/{conversation}/class-inputs", tag,
		"Lista as entradas da classificação que não se refazem: respostas do decision model e trocas manuais"), s.listClassInputs)
	huma.Register(api, op("classify-external-conversation", http.MethodPost, b+"/external-conversations/{conversation}/class", tag,
		"Troca a classe de uma conversa à mão; vale sobre tudo (owner ou admin)"), s.classifyExternalConversation)
	huma.Register(api, op("get-external-usage", http.MethodGet, b+"/external-usage", tag,
		"Uso e custo de lista das chamadas externas, agrupados; o filtro `round` soma só as chamadas de uma rodada"), s.getExternalUsage)
}

// optionalID lê um id opcional de query (vazio = sem filtro).
func optionalID(field, raw string) (*domain.ID, error) {
	if raw == "" {
		return nil, nil
	}
	id, err := domain.ParseID(raw)
	if err != nil {
		return nil, problem(http.StatusUnprocessableEntity, field+" não é um id")
	}
	return &id, nil
}

func idPtrStr(id *domain.ID) *string {
	if id == nil {
		return nil
	}
	s := id.String()
	return &s
}

func parseIDParam(field, raw string) (domain.ID, error) {
	id, err := domain.ParseID(raw)
	if err != nil {
		return id, problem(http.StatusNotFound, field+" não encontrada")
	}
	return id, nil
}

func externalSessionOut(v service.ExternalSessionView) ExternalSession {
	return ExternalSession{ID: v.ID.String(), SourceKind: "external", TaskID: v.TaskID, OuteRepo: v.OuteRepo, WorkspaceID: idPtrStr(v.WorkspaceID),
		Slug: v.Slug, AgentID: idPtrStr(v.AgentID), State: v.State, Round: v.Round, Worker: v.Worker, Conversations: v.Conversations,
		FirstAt: v.FirstAt, LastAt: v.LastAt}
}

func externalConversationOut(v service.ExternalConversationView) ExternalConversation {
	return ExternalConversation{ID: v.ID.String(), SourceKind: "external", SessionID: v.SessionID, Name: v.Name,
		ExternalSessionID: idPtrStr(v.ExternalSessionID), HostID: idPtrStr(v.HostID), AgentID: idPtrStr(v.AgentID), OuteInstance: v.OuteInstance,
		ServiceName: v.ServiceName, SubscriptionKey: v.SubscriptionKey, OuteRepo: v.OuteRepo, WorkspaceID: idPtrStr(v.WorkspaceID),
		InferredAttribution: v.InferredAttribution, FirstAt: v.FirstAt, LastAt: v.LastAt, Calls: v.Calls, Errors: v.Errors,
		InputTokens: v.InputTokens, OutputTokens: v.OutputTokens, CacheReadTokens: v.CacheReadTokens, CacheCreationTokens: v.CacheCreationTokens,
		CostRealUSD: v.CostRealUSD, CostListUSD: v.CostListUSD, UnpricedCalls: v.UnpricedCalls, ContextAvg: v.ContextAvg, ContextMax: v.ContextMax,
		P95Ms: p95Out(v.P95Ms), Class: v.Class, ClassOrigin: v.ClassOrigin, ClassConfidence: v.ClassConfidence, Classifier: v.Classifier, Tags: v.Tags}
}

func p95Out(v *int32) *int {
	if v == nil {
		return nil
	}
	n := int(*v)
	return &n
}

func (s *server) listExternalSessions(ctx context.Context, in *listExternalSessionsIn) (*ListOut[ExternalSession], error) {
	sc, err := s.externalScope(ctx, in.Company)
	if err != nil {
		return nil, err
	}
	ws, err := optionalID("workspace", in.Workspace)
	if err != nil {
		return nil, err
	}
	agent, err := optionalID("agent", in.Agent)
	if err != nil {
		return nil, err
	}
	rows, err := s.svc.ListExternalSessions(ctx, sc.Company.ID, ws, agent, in.Limit, in.Offset)
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	items := make([]ExternalSession, len(rows))
	for i, r := range rows {
		items[i] = externalSessionOut(r)
	}
	return &ListOut[ExternalSession]{Body: ListBody[ExternalSession]{Items: items, Limit: in.Limit, Offset: in.Offset}}, nil
}

func (s *server) getExternalSession(ctx context.Context, in *ExternalSessionPath) (*ItemOut[ExternalSession], error) {
	sc, err := s.externalScope(ctx, in.Company)
	if err != nil {
		return nil, err
	}
	id, err := parseIDParam("sessão", in.Session)
	if err != nil {
		return nil, err
	}
	v, err := s.svc.GetExternalSession(ctx, sc.Company.ID, id)
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	return &ItemOut[ExternalSession]{Body: externalSessionOut(v)}, nil
}

func (s *server) listExternalConversations(ctx context.Context, in *listExternalConversationsIn) (*ListOut[ExternalConversation], error) {
	sc, err := s.externalScope(ctx, in.Company)
	if err != nil {
		return nil, err
	}
	f := service.ExternalConversationFilter{Class: in.Class, Tag: in.Tag, Since: in.Since, Until: in.Until, ByCost: in.Sort == "cost",
		Limit: in.Limit, Offset: in.Offset}
	for _, p := range []struct {
		field, raw string
		dst        **domain.ID
	}{{"host", in.Host, &f.Host}, {"agent", in.Agent, &f.Agent}, {"workspace", in.Workspace, &f.Workspace}, {"session", in.Session, &f.Session}} {
		if *p.dst, err = optionalID(p.field, p.raw); err != nil {
			return nil, err
		}
	}
	rows, err := s.svc.ListExternalConversations(ctx, sc.Company.ID, f)
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	items := make([]ExternalConversation, len(rows))
	for i, r := range rows {
		items[i] = externalConversationOut(r)
	}
	return &ListOut[ExternalConversation]{Body: ListBody[ExternalConversation]{Items: items, Limit: in.Limit, Offset: in.Offset}}, nil
}

func (s *server) getExternalConversation(ctx context.Context, in *ExternalConversationPath) (*ItemOut[ExternalConversation], error) {
	sc, err := s.externalScope(ctx, in.Company)
	if err != nil {
		return nil, err
	}
	id, err := parseIDParam("conversa", in.Conversation)
	if err != nil {
		return nil, err
	}
	v, err := s.svc.GetExternalConversation(ctx, sc.Company.ID, id)
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	return &ItemOut[ExternalConversation]{Body: externalConversationOut(v)}, nil
}

func (s *server) listClassInputs(ctx context.Context, in *ExternalConversationPath) (*ListOut[ConversationClassInput], error) {
	sc, err := s.externalScope(ctx, in.Company)
	if err != nil {
		return nil, err
	}
	id, err := parseIDParam("conversa", in.Conversation)
	if err != nil {
		return nil, err
	}
	rows, err := s.svc.ListConversationClassInputs(ctx, sc.Company.ID, id)
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	items := make([]ConversationClassInput, len(rows))
	for i, r := range rows {
		items[i] = ConversationClassInput{ID: r.ID.String(), Kind: r.Kind, Class: r.Class, Confidence: r.Confidence, ActorType: r.ActorType,
			ActorID: idPtrStr(r.ActorID), Migrated: r.Migrated, CreatedAt: r.CreatedAt}
	}
	return &ListOut[ConversationClassInput]{Body: ListBody[ConversationClassInput]{Items: items, Limit: len(items)}}, nil
}

func (s *server) classifyExternalConversation(ctx context.Context, in *classifyExternalConversationIn) (*ItemOut[ExternalConversation], error) {
	sc, err := s.externalScope(ctx, in.Company)
	if err != nil {
		return nil, err
	}
	id, err := parseIDParam("conversa", in.Conversation)
	if err != nil {
		return nil, err
	}
	v, err := s.svc.ClassifyExternalConversation(ctx, sc.Actor, sc.Company.ID, id, in.Body.Class)
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	return &ItemOut[ExternalConversation]{Body: externalConversationOut(v)}, nil
}

func (s *server) getExternalUsage(ctx context.Context, in *externalUsageIn) (*ListOut[ExternalUsageRow], error) {
	sc, err := s.externalScope(ctx, in.Company)
	if err != nil {
		return nil, err
	}
	round, err := optionalID("round", in.Round)
	if err != nil {
		return nil, err
	}
	rows, err := s.svc.ExternalUsage(ctx, sc.Company.ID, in.Since, in.Until, in.GroupBy, round)
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	items := make([]ExternalUsageRow, len(rows))
	for i, r := range rows {
		row := ExternalUsageRow{HostID: idPtrStr(r.HostID), AgentID: idPtrStr(r.AgentID), WorkspaceID: idPtrStr(r.WorkspaceID), Model: r.Model,
			SubscriptionKey: r.SubscriptionKey, Calls: r.Calls, Errors: r.Errors, InputTokens: r.InputTokens, OutputTokens: r.OutputTokens,
			CacheReadTokens: r.CacheReadTokens, CacheCreationTokens: r.CacheCreation, CostRealUSD: r.CostRealUSD, CostListUSD: r.CostListUSD,
			UnpricedCalls: r.UnpricedCalls}
		if r.Day != nil {
			d := r.Day.Format("2006-01-02")
			row.Day = &d
		}
		items[i] = row
	}
	return &ListOut[ExternalUsageRow]{Body: ListBody[ExternalUsageRow]{Items: items, Limit: len(items)}}, nil
}
