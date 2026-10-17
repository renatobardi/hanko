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

// Sala (ADR-0033): conversa com várias personas, pelas mesmas rotas de conversas (kind=room) e mais as de
// participantes e de configuração.

// Room é a configuração e os participantes de uma sala.
type Room struct {
	Mode               string            `json:"mode" enum:"mentions,all" doc:"mentions = só responde quem é citado com @slug (sem citação, a moderadora); all = todas respondem a cada mensagem humana."`
	ModeratorPersonaID string            `json:"moderator_persona_id,omitempty" format:"uuid" doc:"Moderadora efetiva (responde quando ninguém é citado)."`
	MaxHops            int               `json:"max_hops" doc:"Saltos encadeados (resposta que cita outra persona) a partir de uma mensagem humana."`
	MaxRunsPerMessage  int               `json:"max_runs_per_message" doc:"Teto efetivo de execuções por mensagem humana, contando os saltos."`
	MaxRunsSetting     int               `json:"max_runs_per_message_setting" doc:"O teto configurado; 0 = número de participantes ativos."`
	Participants       []RoomParticipant `json:"participants"`
}

// RoomParticipant é uma persona da sala (as que saíram vêm com removed_at).
type RoomParticipant struct {
	PersonaID   string     `json:"persona_id" format:"uuid"`
	Name        string     `json:"name"`
	Slug        string     `json:"slug"`
	Title       string     `json:"title,omitempty"`
	Icon        string     `json:"icon,omitempty"`
	Status      string     `json:"status" doc:"Status da persona."`
	Active      bool       `json:"active" doc:"Está na sala."`
	AddedAt     time.Time  `json:"added_at"`
	RemovedAt   *time.Time `json:"removed_at,omitempty"`
	ActiveRunID string     `json:"active_run_id,omitempty" format:"uuid" doc:"Execução da persona nesta sala (na fila ou rodando)."`
	Pending     int        `json:"pending" doc:"Mensagens esperando a persona terminar a execução em andamento."`
}

// MessageDelivery é a entrega de uma mensagem da sala a uma persona.
type MessageDelivery struct {
	PersonaID string `json:"persona_id" format:"uuid"`
	RunID     string `json:"run_id,omitempty" format:"uuid" doc:"Execução que recolheu a mensagem; ausente enquanto espera."`
}

// RoomCreateFields são os campos do POST de conversas usados com kind=room.
type RoomCreateFields struct {
	Personas          []string `json:"personas,omitempty" maxItems:"12" doc:"Sala: ids ou slugs das personas (2 a 12)." example:"[\"cto\",\"pm\"]"`
	Moderator         string   `json:"moderator,omitempty" doc:"Sala: id ou slug da moderadora (padrão: a primeira persona)."`
	Mode              string   `json:"mode,omitempty" enum:"mentions,all" doc:"Sala: quem responde (padrão mentions)."`
	MaxHops           int      `json:"max_hops,omitempty" minimum:"0" maximum:"5" doc:"Sala: saltos entre personas (1 a 5; 0 = padrão 2)."`
	MaxRunsPerMessage int      `json:"max_runs_per_message,omitempty" minimum:"0" maximum:"24" doc:"Sala: teto de execuções por mensagem humana (0 = número de participantes)."`
}

func kindOut(k domain.ConversationKind) string {
	if k == "" {
		return string(domain.ConversationDirect)
	}
	return string(k)
}

func zeroIDStr(id domain.ID) string {
	if id == (domain.ID{}) {
		return ""
	}
	return id.String()
}

func idStrs(ids []domain.ID) []string {
	if len(ids) == 0 {
		return nil
	}
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	return out
}

func deliveriesOut(ds []domain.MessageDelivery) []MessageDelivery {
	if len(ds) == 0 {
		return nil
	}
	out := make([]MessageDelivery, len(ds))
	for i, d := range ds {
		out[i] = MessageDelivery{PersonaID: idStr(d.PersonaID), RunID: idPtr(d.RunID)}
	}
	return out
}

func roomOut(r *service.RoomView) *Room {
	if r == nil {
		return nil
	}
	out := &Room{Mode: string(r.Settings.Mode), ModeratorPersonaID: idPtr(r.ModeratorPersonaID), MaxHops: r.Settings.MaxHops,
		MaxRunsPerMessage: r.MaxRunsPerMessage, MaxRunsSetting: r.Settings.MaxRunsPerMessage, Participants: []RoomParticipant{}}
	for _, p := range r.Participants {
		out.Participants = append(out.Participants, RoomParticipant{PersonaID: idStr(p.PersonaID), Name: p.Name, Slug: p.Slug, Title: p.Title,
			Icon: p.Icon, Status: string(p.Status), Active: p.Active(), AddedAt: p.AddedAt, RemovedAt: p.RemovedAt,
			ActiveRunID: idPtr(p.ActiveRunID), Pending: p.Pending})
	}
	return out
}

// createRoom resolve as personas (id ou slug) e abre a sala.
func (s *server) createRoom(ctx context.Context, actor domain.Actor, companyID domain.ID, title, message string, f RoomCreateFields) (service.ConversationView, error) {
	in := service.CreateRoomInput{Title: title, Message: message, Mode: domain.RoomMode(f.Mode), MaxHops: f.MaxHops, MaxRunsPerMessage: f.MaxRunsPerMessage}
	for _, ref := range f.Personas {
		p, err := s.svc.GetPersona(ctx, companyID, strings.TrimSpace(ref))
		if err != nil {
			return service.ConversationView{}, err
		}
		in.PersonaIDs = append(in.PersonaIDs, p.ID)
	}
	mod, err := s.personaRef(ctx, companyID, strings.TrimSpace(f.Moderator))
	if err != nil {
		return service.ConversationView{}, err
	}
	in.ModeratorID = mod
	return s.svc.CreateRoom(ctx, actor, companyID, in)
}

type roomParticipantIn struct {
	ConversationPath
	Body struct {
		Persona string `json:"persona" minLength:"1" doc:"Id ou slug da persona a pôr na sala." example:"cfo"`
	}
}

type roomParticipantPath struct {
	ConversationPath
	Persona string `path:"persona" doc:"Id ou slug da persona a tirar da sala."`
}

type updateRoomIn struct {
	ConversationPath
	Body struct {
		Mode              *string `json:"mode,omitempty" enum:"mentions,all" doc:"Quem responde a uma mensagem humana sem citação."`
		Moderator         *string `json:"moderator,omitempty" doc:"Id ou slug da moderadora (precisa estar na sala)."`
		MaxHops           *int    `json:"max_hops,omitempty" minimum:"1" maximum:"5" doc:"Saltos entre personas a partir de uma mensagem humana."`
		MaxRunsPerMessage *int    `json:"max_runs_per_message,omitempty" minimum:"0" maximum:"24" doc:"Teto de execuções por mensagem humana (0 = número de participantes)."`
	}
}

func (s *server) registerRoom(api huma.API) {
	huma.Register(api, op("add-room-participant", http.MethodPost, "/companies/{company}/conversations/{conversation}/participants", "chat", "Põe uma persona na sala"),
		func(ctx context.Context, in *roomParticipantIn) (*ItemOut[Conversation], error) {
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
			p, err := s.svc.GetPersona(ctx, sc.Company.ID, in.Body.Persona)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			v, err := s.svc.AddRoomParticipant(ctx, sc.Actor, sc.Company.ID, id, p.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Conversation]{Body: conversationOut(v)}, nil
		})

	huma.Register(api, op("remove-room-participant", http.MethodDelete, "/companies/{company}/conversations/{conversation}/participants/{persona}", "chat", "Tira uma persona da sala"),
		func(ctx context.Context, in *roomParticipantPath) (*ItemOut[Conversation], error) {
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
			p, err := s.svc.GetPersona(ctx, sc.Company.ID, in.Persona)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			v, err := s.svc.RemoveRoomParticipant(ctx, sc.Actor, sc.Company.ID, id, p.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Conversation]{Body: conversationOut(v)}, nil
		})

	huma.Register(api, op("update-room", http.MethodPatch, "/companies/{company}/conversations/{conversation}/room", "chat", "Muda o modo, a moderadora ou os limites da sala"),
		func(ctx context.Context, in *updateRoomIn) (*ItemOut[Conversation], error) {
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
			patch := service.RoomSettingsPatch{MaxHops: in.Body.MaxHops, MaxRunsPerMessage: in.Body.MaxRunsPerMessage}
			if in.Body.Mode != nil {
				m := domain.RoomMode(*in.Body.Mode)
				patch.Mode = &m
			}
			if in.Body.Moderator != nil {
				p, err := s.svc.GetPersona(ctx, sc.Company.ID, *in.Body.Moderator)
				if err != nil {
					return nil, s.fail(ctx, err)
				}
				patch.ModeratorID = &p.ID
			}
			v, err := s.svc.UpdateRoomSettings(ctx, sc.Actor, sc.Company.ID, id, patch)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Conversation]{Body: conversationOut(v)}, nil
		})
}
