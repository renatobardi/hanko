package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Conversation é uma conversa direta de um humano com uma persona, fora de uma task (chat).
type Conversation struct {
	ID              string    `json:"id" format:"uuid"`
	CompanyID       string    `json:"company_id" format:"uuid"`
	PersonaID       string    `json:"persona_id" format:"uuid"`
	PersonaName     string    `json:"persona_name"`
	PersonaSlug     string    `json:"persona_slug"`
	Title           string    `json:"title"`
	Status          string    `json:"status" enum:"open,archived"`
	CreatedBy       ActorRef  `json:"created_by"`
	ActiveRunID     string    `json:"active_run_id,omitempty" format:"uuid" doc:"Execução que está respondendo agora; ausente quando a persona está ociosa."`
	PendingMessages int       `json:"pending_messages" doc:"Mensagens humanas que ainda esperam uma execução."`
	CostUSD         float64   `json:"cost_usd" doc:"Custo somado das execuções da conversa."`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	LastMessageAt   time.Time `json:"last_message_at"`
}

// ConversationMessage é uma mensagem de uma conversa.
type ConversationMessage struct {
	ID             string    `json:"id" format:"uuid"`
	ConversationID string    `json:"conversation_id" format:"uuid"`
	Author         ActorRef  `json:"author" doc:"Humano, persona (resposta de uma execução) ou sistema (aviso)."`
	Body           string    `json:"body" doc:"Markdown."`
	RunID          string    `json:"run_id,omitempty" format:"uuid" doc:"Na resposta da persona (ou no aviso), a execução que a produziu; na mensagem humana, a que a recolheu (ausente enquanto espera)."`
	CreatedAt      time.Time `json:"created_at"`
}

func conversationOut(v service.ConversationView) Conversation {
	return Conversation{ID: idStr(v.ID), CompanyID: idStr(v.CompanyID), PersonaID: idStr(v.PersonaID), PersonaName: v.PersonaName,
		PersonaSlug: v.PersonaSlug, Title: v.Title, Status: string(v.Status), CreatedBy: actorRef(v.CreatedBy),
		ActiveRunID: idPtr(v.ActiveRunID), PendingMessages: v.PendingMessages, CostUSD: v.CostUSD,
		CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, LastMessageAt: v.LastMessageAt}
}

func conversationMessageOut(m domain.ConversationMessage) ConversationMessage {
	return ConversationMessage{ID: idStr(m.ID), ConversationID: idStr(m.ConversationID), Author: actorRef(m.Author),
		Body: m.Body, RunID: idPtr(m.RunID), CreatedAt: m.CreatedAt}
}

type ConversationPath struct {
	CompanyPath
	Conversation string `path:"conversation" format:"uuid" doc:"Id da conversa."`
}

type listConversationsIn struct {
	CompanyPath
	Page
	Persona string `query:"persona" doc:"Id ou slug da persona."`
	Status  string `query:"status" enum:"open,archived" doc:"Só conversas neste estado; omitido = todas."`
}

type createConversationIn struct {
	CompanyPath
	Body struct {
		Persona string `json:"persona" minLength:"1" doc:"Id ou slug da persona com quem conversar." example:"cto"`
		Title   string `json:"title,omitempty" maxLength:"200" doc:"Derivado da primeira mensagem quando omitido."`
		Message string `json:"message,omitempty" maxLength:"20000" doc:"Primeira mensagem (markdown); a persona responde por uma execução."`
	}
}

type listConversationMessagesIn struct {
	ConversationPath
	Limit int `query:"limit" minimum:"1" maximum:"500" default:"200" doc:"Quantas mensagens mais recentes (em ordem cronológica)."`
}

type sendMessageIn struct {
	ConversationPath
	Body struct {
		Message string `json:"message" minLength:"1" maxLength:"20000" doc:"Mensagem em markdown. Enquanto uma execução da conversa roda, a mensagem espera e a execução seguinte responde a todas juntas."`
	}
}

func (s *server) conversationID(raw string) (domain.ID, error) {
	id, err := domain.ParseID(raw)
	if err != nil {
		return id, domain.Invalid("conversation", "não é um uuid")
	}
	return id, nil
}

func (s *server) registerChat(api huma.API) {
	huma.Register(api, op("list-conversations", http.MethodGet, "/companies/{company}/conversations", "chat", "Lista conversas"),
		func(ctx context.Context, in *listConversationsIn) (*ListOut[Conversation], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			persona, err := s.personaRef(ctx, sc.Company.ID, in.Persona)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			vs, err := s.svc.ListConversations(ctx, sc.Company.ID, service.ConversationFilter{PersonaID: persona,
				Status: domain.ConversationStatus(in.Status), Limit: min(in.Limit+in.Offset, 200)})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listOf(vs, in.Page, conversationOut), nil
		})

	huma.Register(api, created(op("create-conversation", http.MethodPost, "/companies/{company}/conversations", "chat", "Abre uma conversa com uma persona")),
		func(ctx context.Context, in *createConversationIn) (*ItemOut[Conversation], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			p, err := s.svc.GetPersona(ctx, sc.Company.ID, in.Body.Persona)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			v, err := s.svc.CreateConversation(ctx, sc.Actor, sc.Company.ID, service.CreateConversationInput{
				PersonaID: p.ID, Title: in.Body.Title, Message: in.Body.Message})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Conversation]{Body: conversationOut(v)}, nil
		})

	huma.Register(api, op("get-conversation", http.MethodGet, "/companies/{company}/conversations/{conversation}", "chat", "Obtém uma conversa"),
		func(ctx context.Context, in *ConversationPath) (*ItemOut[Conversation], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			id, err := s.conversationID(in.Conversation)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			v, err := s.svc.GetConversation(ctx, sc.Company.ID, id)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Conversation]{Body: conversationOut(v)}, nil
		})

	huma.Register(api, op("list-conversation-messages", http.MethodGet, "/companies/{company}/conversations/{conversation}/messages", "chat", "Lista as mensagens de uma conversa"),
		func(ctx context.Context, in *listConversationMessagesIn) (*ListOut[ConversationMessage], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			id, err := s.conversationID(in.Conversation)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			ms, err := s.svc.ListConversationMessages(ctx, sc.Company.ID, id, in.Limit)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(ms, Page{Limit: in.Limit}, conversationMessageOut), nil
		})

	huma.Register(api, created(op("send-conversation-message", http.MethodPost, "/companies/{company}/conversations/{conversation}/messages", "chat", "Envia uma mensagem à persona")),
		func(ctx context.Context, in *sendMessageIn) (*ItemOut[ConversationMessage], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			id, err := s.conversationID(in.Conversation)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			m, err := s.svc.SendMessage(ctx, sc.Actor, sc.Company.ID, id, in.Body.Message)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[ConversationMessage]{Body: conversationMessageOut(m)}, nil
		})

	for _, st := range []struct {
		id, path, summary string
		status            domain.ConversationStatus
	}{
		{"archive-conversation", "archive", "Arquiva uma conversa", domain.ConversationArchived},
		{"reopen-conversation", "reopen", "Reabre uma conversa arquivada", domain.ConversationOpen},
	} {
		huma.Register(api, op(st.id, http.MethodPost, "/companies/{company}/conversations/{conversation}/"+st.path, "chat", st.summary),
			func(ctx context.Context, in *ConversationPath) (*ItemOut[Conversation], error) {
				if err := forbidPersona(ctx); err != nil {
					return nil, err
				}
				sc, err := s.scope(ctx, in.Company)
				if err != nil {
					return nil, err
				}
				id, err := s.conversationID(in.Conversation)
				if err != nil {
					return nil, s.fail(ctx, err)
				}
				v, err := s.svc.SetConversationStatus(ctx, sc.Actor, sc.Company.ID, id, st.status)
				if err != nil {
					return nil, s.fail(ctx, err)
				}
				return &ItemOut[Conversation]{Body: conversationOut(v)}, nil
			})
	}
}
