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

// Conversation é uma conversa fora de uma task: direta com uma persona (chat) ou com várias (sala, ADR-0033).
type Conversation struct {
	ID              string    `json:"id" format:"uuid"`
	CompanyID       string    `json:"company_id" format:"uuid"`
	Kind            string    `json:"kind" enum:"direct,room" doc:"direct = chat com uma persona; room = sala com várias."`
	PersonaID       string    `json:"persona_id,omitempty" format:"uuid" doc:"Persona do chat direto; ausente numa sala (veja room.participants)."`
	PersonaName     string    `json:"persona_name,omitempty"`
	PersonaSlug     string    `json:"persona_slug,omitempty"`
	Title           string    `json:"title"`
	Status          string    `json:"status" enum:"open,archived"`
	CreatedBy       ActorRef  `json:"created_by"`
	ActiveRunID     string    `json:"active_run_id,omitempty" format:"uuid" doc:"Execução que está respondendo agora; ausente quando a persona está ociosa."`
	PendingMessages int       `json:"pending_messages" doc:"Mensagens humanas que ainda esperam uma execução."`
	CostUSD         float64   `json:"cost_usd" doc:"Custo somado das execuções da conversa."`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	LastMessageAt   time.Time `json:"last_message_at"`
	Room            *Room     `json:"room,omitempty" doc:"Configuração e participantes de uma sala."`
}

// ConversationMessage é uma mensagem de uma conversa.
type ConversationMessage struct {
	ID             string    `json:"id" format:"uuid"`
	ConversationID string    `json:"conversation_id" format:"uuid"`
	Author         ActorRef  `json:"author" doc:"Humano, persona (resposta de uma execução) ou sistema (aviso)."`
	Body           string    `json:"body" doc:"Markdown."`
	RunID          string    `json:"run_id,omitempty" format:"uuid" doc:"Na resposta da persona (ou no aviso), a execução que a produziu; na mensagem humana do chat direto, a que a recolheu (ausente enquanto espera)."`
	CreatedAt      time.Time `json:"created_at"`
	Marker         string    `json:"marker,omitempty" enum:"context_reset" doc:"context_reset: aviso de sistema que /new insere; o contexto da conversa recomeça depois dele (ADR-0052)."`
	// sala (ADR-0033)
	PersonaID   string            `json:"persona_id,omitempty" format:"uuid" doc:"Sala: a persona que falou (ou de quem o aviso trata)."`
	PersonaName string            `json:"persona_name,omitempty"`
	ReplyToID   string            `json:"reply_to_id,omitempty" format:"uuid" doc:"Sala: a mensagem que esta responde."`
	Mentions    []string          `json:"mentions,omitempty" doc:"Sala: ids das personas citadas com @slug."`
	Deliveries  []MessageDelivery `json:"deliveries,omitempty" doc:"Sala: a quem a mensagem foi entregue e a execução de cada persona (ausente enquanto espera a persona terminar a anterior)."`
}

func conversationOut(v service.ConversationView) Conversation {
	return Conversation{ID: idStr(v.ID), CompanyID: idStr(v.CompanyID), Kind: kindOut(v.Kind), PersonaID: zeroIDStr(v.PersonaID), PersonaName: v.PersonaName,
		PersonaSlug: v.PersonaSlug, Title: v.Title, Status: string(v.Status), CreatedBy: actorRef(v.CreatedBy),
		ActiveRunID: idPtr(v.ActiveRunID), PendingMessages: v.PendingMessages, CostUSD: v.CostUSD,
		CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, LastMessageAt: v.LastMessageAt, Room: roomOut(v.Room)}
}

func conversationMessageOut(m domain.ConversationMessage) ConversationMessage {
	return ConversationMessage{ID: idStr(m.ID), ConversationID: idStr(m.ConversationID), Author: actorRef(m.Author),
		Body: m.Body, RunID: idPtr(m.RunID), CreatedAt: m.CreatedAt, Marker: m.Marker, PersonaID: idPtr(m.PersonaID), PersonaName: m.PersonaName,
		ReplyToID: idPtr(m.ReplyToID), Mentions: idStrs(m.Mentions), Deliveries: deliveriesOut(m.Deliveries)}
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
	Kind    string `query:"kind" enum:"direct,room" doc:"Só chats diretos ou só salas; omitido = os dois. Com persona, numa sala filtra por participante."`
}

type createConversationIn struct {
	CompanyPath
	Body struct {
		Kind    string `json:"kind,omitempty" enum:"direct,room" default:"direct" doc:"direct (padrão) = chat com uma persona; room = sala com várias (ADR-0033)."`
		Persona string `json:"persona,omitempty" doc:"Chat direto: id ou slug da persona com quem conversar." example:"cto"`
		Title   string `json:"title,omitempty" maxLength:"200" doc:"Derivado da primeira mensagem quando omitido."`
		Message string `json:"message,omitempty" maxLength:"20000" doc:"Primeira mensagem (markdown); a persona responde por uma execução."`
		RoomCreateFields
	}
}

type listConversationMessagesIn struct {
	ConversationPath
	Limit int `query:"limit" minimum:"1" maximum:"500" default:"200" doc:"Quantas mensagens mais recentes (em ordem cronológica)."`
}

type sendMessageIn struct {
	ConversationPath
	Body struct {
		Message string `json:"message" minLength:"1" maxLength:"20000" doc:"Mensagem em markdown. Enquanto uma execução da conversa roda, a mensagem espera e a execução seguinte responde a todas juntas. \"/new\" sozinha reinicia o contexto da conversa (insere um marcador; nada é apagado); \"/new texto\" reinicia e envia o texto."`
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
				Status: domain.ConversationStatus(in.Status), Kind: domain.ConversationKind(in.Kind), Limit: min(in.Limit+in.Offset, 200)})
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
			if in.Body.Kind == string(domain.ConversationRoom) {
				v, err := s.createRoom(ctx, sc.Actor, sc.Company.ID, in.Body.Title, in.Body.Message, in.Body.RoomCreateFields)
				if err != nil {
					return nil, s.fail(ctx, err)
				}
				return &ItemOut[Conversation]{Body: conversationOut(v)}, nil
			}
			if strings.TrimSpace(in.Body.Persona) == "" {
				return nil, s.fail(ctx, domain.Invalid("persona", "informe a persona do chat (ou kind=room para uma sala)"))
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
