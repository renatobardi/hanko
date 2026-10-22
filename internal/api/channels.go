package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Canais externos (ADR-0046): Telegram e GitHub.

// channelsBackend é a parte do service que cuida dos canais; opcional como secretsBackend.
type channelsBackend interface {
	ListChannels(ctx context.Context, companyID domain.ID) ([]service.ChannelView, error)
	GetChannel(ctx context.Context, companyID domain.ID, ref string) (service.ChannelView, error)
	CreateChannel(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateChannelInput) (service.ChannelView, error)
	UpdateChannel(ctx context.Context, actor domain.Actor, companyID, channelID domain.ID, in service.UpdateChannelInput) (service.ChannelView, error)
	DeleteChannel(ctx context.Context, actor domain.Actor, companyID, channelID domain.ID) error
	ConnectChannel(ctx context.Context, actor domain.Actor, companyID, channelID domain.ID) (service.ChannelConnection, error)
	CreateChannelLink(ctx context.Context, actor domain.Actor, companyID, channelID domain.ID) (service.ChannelLink, error)
	ListChannelIdentities(ctx context.Context, companyID, channelID domain.ID) ([]domain.ChannelIdentity, error)
	RevokeChannelIdentity(ctx context.Context, actor domain.Actor, companyID, channelID, identityID domain.ID) error
	ListChannelMessages(ctx context.Context, companyID, channelID domain.ID, limit int) ([]domain.ChannelMessage, error)
	SendChannelMessage(ctx context.Context, actor domain.Actor, companyID domain.ID, channelRef, peer, text string, artifactIDs []domain.ID) (domain.ChannelMessage, error)
	ResendChannelMessage(ctx context.Context, actor domain.Actor, companyID, messageID domain.ID) (domain.ChannelMessage, error)
	PublishChannelReview(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.PublishReviewInput) (domain.ChannelMessage, error)
}

func (s *server) channels() (channelsBackend, error) {
	b, ok := s.svc.(channelsBackend)
	if !ok {
		return nil, problem(http.StatusNotImplemented, "canais indisponíveis neste servidor")
	}
	return b, nil
}

// ChannelConfig é a configuração sem segredo de um canal.
type ChannelConfig struct {
	APIBase string   `json:"api_base,omitempty" doc:"Raiz da API do provedor (espelho, GitHub Enterprise). Vazio = a pública."`
	Repos   []string `json:"repos,omitempty" doc:"GitHub: repositórios (dono/repo ou dono/*) cujos comentários acordam a persona."`
	Mention string   `json:"mention,omitempty" doc:"GitHub: a citação que acorda a persona. Padrão @hanko."`
}

// Channel é um canal externo.
type Channel struct {
	ID               string        `json:"id" format:"uuid"`
	Kind             string        `json:"kind" enum:"telegram,github"`
	Name             string        `json:"name"`
	Enabled          bool          `json:"enabled"`
	PersonaID        string        `json:"persona_id,omitempty" format:"uuid"`
	PersonaName      string        `json:"persona_name,omitempty"`
	ProjectID        string        `json:"project_id,omitempty" format:"uuid"`
	TokenRef         string        `json:"token_ref,omitempty" doc:"Referência ${secret:NOME} do token do bot/API."`
	WebhookSecretRef string        `json:"webhook_secret_ref,omitempty" doc:"Referência ${secret:NOME} que autentica a entrada."`
	Config           ChannelConfig `json:"config"`
	HookPath         string        `json:"hook_path" doc:"Caminho de entrada (relativo à URL pública): o provedor chama POST aqui."`
	Linked           int           `json:"linked" doc:"Pessoas vinculadas."`
	Pending          int           `json:"pending" doc:"Vínculos esperando o código."`
	FailedOut        int           `json:"failed_out" doc:"Mensagens de saída que falharam."`
	CreatedAt        time.Time     `json:"created_at"`
	UpdatedAt        time.Time     `json:"updated_at"`
}

func channelOut(c service.ChannelView) Channel {
	return Channel{ID: idStr(c.ID), Kind: string(c.Kind), Name: c.Name, Enabled: c.Enabled, PersonaID: idPtr(c.PersonaID), PersonaName: c.PersonaName,
		ProjectID: idPtr(c.ProjectID), TokenRef: c.TokenRef, WebhookSecretRef: c.WebhookSecretRef,
		Config:   ChannelConfig{APIBase: c.Config.APIBase, Repos: c.Config.Repos, Mention: c.Config.Mention},
		HookPath: c.HookPath, Linked: c.Linked, Pending: c.Pending, FailedOut: c.FailedOut, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
}

// ChannelIdentity é o vínculo entre uma pessoa do canal e um usuário do Hanko.
type ChannelIdentity struct {
	ID            string     `json:"id" format:"uuid"`
	UserID        string     `json:"user_id" format:"uuid"`
	UserEmail     string     `json:"user_email,omitempty"`
	UserName      string     `json:"user_name,omitempty"`
	ExternalID    string     `json:"external_id,omitempty"`
	ExternalLabel string     `json:"external_label,omitempty"`
	Status        string     `json:"status" enum:"pending,linked,revoked"`
	CodeExpiresAt *time.Time `json:"code_expires_at,omitempty"`
	LinkedAt      *time.Time `json:"linked_at,omitempty"`
}

func identityOut(i domain.ChannelIdentity) ChannelIdentity {
	return ChannelIdentity{ID: idStr(i.ID), UserID: idStr(i.UserID), UserEmail: i.UserEmail, UserName: i.UserName, ExternalID: i.ExternalID,
		ExternalLabel: i.ExternalLabel, Status: string(i.Status), CodeExpiresAt: i.CodeExpiresAt, LinkedAt: i.LinkedAt}
}

// ChannelMessage é uma mensagem de canal com o estado de entrega.
type ChannelMessage struct {
	ID            string     `json:"id" format:"uuid"`
	Direction     string     `json:"direction" enum:"in,out"`
	Kind          string     `json:"kind"`
	Peer          string     `json:"peer,omitempty"`
	Body          string     `json:"body"`
	Status        string     `json:"status" enum:"received,processed,ignored,pending,sent,failed" doc:"Entrada: received → processed | ignored | failed. Saída: pending → sent | failed."`
	StatusReason  string     `json:"status_reason,omitempty"`
	Attempts      int        `json:"attempts"`
	LastError     string     `json:"last_error,omitempty"`
	ProviderMsgID string     `json:"provider_msg_id,omitempty"`
	SubjectType   string     `json:"subject_type,omitempty"`
	SubjectID     string     `json:"subject_id,omitempty" format:"uuid"`
	CreatedAt     time.Time  `json:"created_at"`
	SentAt        *time.Time `json:"sent_at,omitempty"`
}

func channelMessageOut(m domain.ChannelMessage) ChannelMessage {
	return ChannelMessage{ID: idStr(m.ID), Direction: string(m.Direction), Kind: m.Kind, Peer: m.Peer, Body: m.Body, Status: string(m.Status),
		StatusReason: m.StatusReason, Attempts: m.Attempts, LastError: m.LastError, ProviderMsgID: m.ProviderMsgID, SubjectType: m.SubjectType,
		SubjectID: idPtr(m.SubjectID), CreatedAt: m.CreatedAt, SentAt: m.SentAt}
}

// ChannelLink é o código de vínculo recém-gerado (aparece uma vez).
type ChannelLink struct {
	Code      string    `json:"code" example:"K7QM-2XPA"`
	ExpiresAt time.Time `json:"expires_at"`
	Hint      string    `json:"hint" doc:"Onde digitar o código no canal."`
}

// ChannelConnection é o resultado de conectar o canal ao provedor.
type ChannelConnection struct {
	URL        string `json:"url"`
	Registered bool   `json:"registered" doc:"true quando o Hanko registrou o webhook no provedor (Telegram)."`
	Note       string `json:"note,omitempty"`
}

type ChannelPath struct {
	CompanyPath
	Channel string `path:"channel" doc:"Id ou nome do canal."`
}

type createChannelIn struct {
	CompanyPath
	Body struct {
		Kind             string        `json:"kind" enum:"telegram,github"`
		Name             string        `json:"name" minLength:"1" maxLength:"80" example:"Bot do time"`
		Enabled          bool          `json:"enabled,omitempty" doc:"Ligar já na criação (exige token e segredo)."`
		Persona          string        `json:"persona,omitempty" doc:"Id ou slug da persona que conversa (Telegram) ou é acordada (GitHub; obrigatória para ligar)."`
		Project          string        `json:"project,omitempty" doc:"Projeto das tasks criadas por comentários do GitHub."`
		TokenRef         string        `json:"token_ref,omitempty" example:"${secret:telegram-token}"`
		WebhookSecretRef string        `json:"webhook_secret_ref,omitempty" example:"${secret:telegram-webhook}"`
		Config           ChannelConfig `json:"config,omitempty"`
	}
}

type updateChannelIn struct {
	ChannelPath
	Body struct {
		Name             *string        `json:"name,omitempty"`
		Enabled          *bool          `json:"enabled,omitempty"`
		Persona          string         `json:"persona,omitempty"`
		ClearPersona     bool           `json:"clear_persona,omitempty"`
		Project          string         `json:"project,omitempty"`
		ClearProject     bool           `json:"clear_project,omitempty"`
		TokenRef         *string        `json:"token_ref,omitempty"`
		WebhookSecretRef *string        `json:"webhook_secret_ref,omitempty"`
		Config           *ChannelConfig `json:"config,omitempty"`
	}
}

type channelIdentityPath struct {
	ChannelPath
	Identity string `path:"identity" format:"uuid"`
}

type channelMessagePath struct {
	ChannelPath
	Message string `path:"message" format:"uuid"`
}

type listChannelMessagesIn struct {
	ChannelPath
	Limit int `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

type sendChannelMessageIn struct {
	ChannelPath
	Body struct {
		Peer      string   `json:"peer" minLength:"1" doc:"Telegram: id do chat. GitHub: dono/repo#N."`
		Text      string   `json:"text,omitempty"`
		Artifacts []string `json:"artifacts,omitempty" doc:"Ids de artefatos enviados como documento (Telegram)."`
	}
}

type publishReviewIn struct {
	ChannelPath
	Body struct {
		Peer  string `json:"peer,omitempty" doc:"dono/repo#N; vazio resolve pela task."`
		Task  string `json:"task,omitempty" doc:"Chave ou id da task nascida do comentário do GitHub."`
		Event string `json:"event" enum:"COMMENT,REQUEST_CHANGES,APPROVE"`
		Body  string `json:"body" minLength:"1"`
	}
}

func (s *server) registerChannels(api huma.API) {
	type chanCtx struct {
		cb channelsBackend
		sc scope
	}
	prep := func(ctx context.Context, company string, write bool) (chanCtx, error) {
		if write {
			if err := forbidPersona(ctx); err != nil {
				return chanCtx{}, err
			}
		}
		cb, err := s.channels()
		if err != nil {
			return chanCtx{}, err
		}
		sc, err := s.scope(ctx, company)
		if err != nil {
			return chanCtx{}, err
		}
		return chanCtx{cb: cb, sc: sc}, nil
	}
	channelOf := func(ctx context.Context, c chanCtx, ref string) (service.ChannelView, error) {
		v, err := c.cb.GetChannel(ctx, c.sc.Company.ID, ref)
		if err != nil {
			return v, s.fail(ctx, err)
		}
		return v, nil
	}

	huma.Register(api, op("list-channels", http.MethodGet, "/companies/{company}/channels", "channels", "Lista os canais externos"),
		func(ctx context.Context, in *listCompanyIn) (*ListOut[Channel], error) {
			c, err := prep(ctx, in.Company, false)
			if err != nil {
				return nil, err
			}
			cs, err := c.cb.ListChannels(ctx, c.sc.Company.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listOf(cs, in.Page, channelOut), nil
		})

	huma.Register(api, created(op("create-channel", http.MethodPost, "/companies/{company}/channels", "channels", "Cadastra um canal (Telegram ou GitHub)")),
		func(ctx context.Context, in *createChannelIn) (*ItemOut[Channel], error) {
			c, err := prep(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			p, err := s.personaRef(ctx, c.sc.Company.ID, in.Body.Persona)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			pr, err := s.projectRef(ctx, c.sc.Company.ID, in.Body.Project)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			b := in.Body
			v, err := c.cb.CreateChannel(ctx, c.sc.Actor, c.sc.Company.ID, service.CreateChannelInput{Kind: domain.ChannelKind(b.Kind), Name: b.Name,
				Enabled: b.Enabled, PersonaID: p, ProjectID: pr, TokenRef: b.TokenRef, WebhookSecretRef: b.WebhookSecretRef,
				Config: domain.ChannelConfig{APIBase: b.Config.APIBase, Repos: b.Config.Repos, Mention: b.Config.Mention}})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Channel]{Body: channelOut(v)}, nil
		})

	huma.Register(api, op("get-channel", http.MethodGet, "/companies/{company}/channels/{channel}", "channels", "Obtém um canal"),
		func(ctx context.Context, in *ChannelPath) (*ItemOut[Channel], error) {
			c, err := prep(ctx, in.Company, false)
			if err != nil {
				return nil, err
			}
			v, err := channelOf(ctx, c, in.Channel)
			if err != nil {
				return nil, err
			}
			return &ItemOut[Channel]{Body: channelOut(v)}, nil
		})

	huma.Register(api, op("update-channel", http.MethodPatch, "/companies/{company}/channels/{channel}", "channels", "Atualiza um canal (liga, desliga, persona, segredos, configuração)"),
		func(ctx context.Context, in *updateChannelIn) (*ItemOut[Channel], error) {
			c, err := prep(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			cur, err := channelOf(ctx, c, in.Channel)
			if err != nil {
				return nil, err
			}
			p, err := s.personaRef(ctx, c.sc.Company.ID, in.Body.Persona)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			pr, err := s.projectRef(ctx, c.sc.Company.ID, in.Body.Project)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			b := in.Body
			up := service.UpdateChannelInput{Name: b.Name, Enabled: b.Enabled, PersonaID: p, ClearPersona: b.ClearPersona, ProjectID: pr,
				ClearProject: b.ClearProject, TokenRef: b.TokenRef, WebhookSecretRef: b.WebhookSecretRef}
			if b.Config != nil {
				up.Config = &domain.ChannelConfig{APIBase: b.Config.APIBase, Repos: b.Config.Repos, Mention: b.Config.Mention}
			}
			v, err := c.cb.UpdateChannel(ctx, c.sc.Actor, c.sc.Company.ID, cur.ID, up)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Channel]{Body: channelOut(v)}, nil
		})

	huma.Register(api, noContent(op("delete-channel", http.MethodDelete, "/companies/{company}/channels/{channel}", "channels", "Apaga um canal com vínculos e mensagens")),
		func(ctx context.Context, in *ChannelPath) (*struct{}, error) {
			c, err := prep(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			cur, err := channelOf(ctx, c, in.Channel)
			if err != nil {
				return nil, err
			}
			if err := c.cb.DeleteChannel(ctx, c.sc.Actor, c.sc.Company.ID, cur.ID); err != nil {
				return nil, s.fail(ctx, err)
			}
			return &struct{}{}, nil
		})

	huma.Register(api, op("connect-channel", http.MethodPost, "/companies/{company}/channels/{channel}/connect", "channels", "Registra o webhook no provedor (Telegram) ou devolve a URL a configurar (GitHub)"),
		func(ctx context.Context, in *ChannelPath) (*ItemOut[ChannelConnection], error) {
			c, err := prep(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			cur, err := channelOf(ctx, c, in.Channel)
			if err != nil {
				return nil, err
			}
			r, err := c.cb.ConnectChannel(ctx, c.sc.Actor, c.sc.Company.ID, cur.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[ChannelConnection]{Body: ChannelConnection{URL: r.URL, Registered: r.Registered, Note: r.Note}}, nil
		})

	huma.Register(api, created(op("create-channel-link", http.MethodPost, "/companies/{company}/channels/{channel}/link", "channels", "Gera o código de vínculo da pessoa autenticada")),
		func(ctx context.Context, in *ChannelPath) (*ItemOut[ChannelLink], error) {
			c, err := prep(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			cur, err := channelOf(ctx, c, in.Channel)
			if err != nil {
				return nil, err
			}
			l, err := c.cb.CreateChannelLink(ctx, c.sc.Actor, c.sc.Company.ID, cur.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[ChannelLink]{Body: ChannelLink{Code: l.Code, ExpiresAt: l.ExpiresAt, Hint: l.Hint}}, nil
		})

	huma.Register(api, op("list-channel-identities", http.MethodGet, "/companies/{company}/channels/{channel}/identities", "channels", "Vínculos de identidade do canal"),
		func(ctx context.Context, in *ChannelPath) (*ListOut[ChannelIdentity], error) {
			c, err := prep(ctx, in.Company, false)
			if err != nil {
				return nil, err
			}
			cur, err := channelOf(ctx, c, in.Channel)
			if err != nil {
				return nil, err
			}
			is, err := c.cb.ListChannelIdentities(ctx, c.sc.Company.ID, cur.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(is, Page{Limit: len(is)}, identityOut), nil
		})

	huma.Register(api, noContent(op("revoke-channel-identity", http.MethodDelete, "/companies/{company}/channels/{channel}/identities/{identity}", "channels", "Revoga um vínculo (a própria pessoa ou um admin)")),
		func(ctx context.Context, in *channelIdentityPath) (*struct{}, error) {
			c, err := prep(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			cur, err := channelOf(ctx, c, in.Channel)
			if err != nil {
				return nil, err
			}
			iid, err := domain.ParseID(in.Identity)
			if err != nil {
				return nil, s.fail(ctx, domain.Invalid("identity", "id inválido"))
			}
			if err := c.cb.RevokeChannelIdentity(ctx, c.sc.Actor, c.sc.Company.ID, cur.ID, iid); err != nil {
				return nil, s.fail(ctx, err)
			}
			return &struct{}{}, nil
		})

	huma.Register(api, op("list-channel-messages", http.MethodGet, "/companies/{company}/channels/{channel}/messages", "channels", "Mensagens recentes do canal com o estado de entrega"),
		func(ctx context.Context, in *listChannelMessagesIn) (*ListOut[ChannelMessage], error) {
			c, err := prep(ctx, in.Company, false)
			if err != nil {
				return nil, err
			}
			cur, err := channelOf(ctx, c, in.Channel)
			if err != nil {
				return nil, err
			}
			ms, err := c.cb.ListChannelMessages(ctx, c.sc.Company.ID, cur.ID, in.Limit)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(ms, Page{Limit: in.Limit}, channelMessageOut), nil
		})

	huma.Register(api, created(op("send-channel-message", http.MethodPost, "/companies/{company}/channels/{channel}/messages", "channels", "Envia uma mensagem avulsa por um canal")),
		func(ctx context.Context, in *sendChannelMessageIn) (*ItemOut[ChannelMessage], error) {
			c, err := prep(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			cur, err := channelOf(ctx, c, in.Channel)
			if err != nil {
				return nil, err
			}
			ids := make([]domain.ID, 0, len(in.Body.Artifacts))
			for _, a := range in.Body.Artifacts {
				id, err := domain.ParseID(a)
				if err != nil {
					return nil, s.fail(ctx, domain.Invalid("artifacts", "id inválido"))
				}
				ids = append(ids, id)
			}
			m, err := c.cb.SendChannelMessage(ctx, c.sc.Actor, c.sc.Company.ID, cur.ID.String(), in.Body.Peer, in.Body.Text, ids)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[ChannelMessage]{Body: channelMessageOut(m)}, nil
		})

	huma.Register(api, op("resend-channel-message", http.MethodPost, "/companies/{company}/channels/{channel}/messages/{message}/resend", "channels", "Reenvia uma mensagem de saída que falhou"),
		func(ctx context.Context, in *channelMessagePath) (*ItemOut[ChannelMessage], error) {
			c, err := prep(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			if _, err := channelOf(ctx, c, in.Channel); err != nil {
				return nil, err
			}
			mid, err := domain.ParseID(in.Message)
			if err != nil {
				return nil, s.fail(ctx, domain.Invalid("message", "id inválido"))
			}
			m, err := c.cb.ResendChannelMessage(ctx, c.sc.Actor, c.sc.Company.ID, mid)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[ChannelMessage]{Body: channelMessageOut(m)}, nil
		})

	huma.Register(api, created(op("publish-channel-review", http.MethodPost, "/companies/{company}/channels/{channel}/reviews", "channels", "Publica uma review (ou comentário de review) num pull request do GitHub")),
		func(ctx context.Context, in *publishReviewIn) (*ItemOut[ChannelMessage], error) {
			c, err := prep(ctx, in.Company, false) // a persona também publica (run token); a regra por ator é do service
			if err != nil {
				return nil, err
			}
			cur, err := channelOf(ctx, c, in.Channel)
			if err != nil {
				return nil, err
			}
			pin := service.PublishReviewInput{Channel: cur.ID.String(), Peer: in.Body.Peer, Event: domain.GitHubReviewEvent(in.Body.Event), Body: in.Body.Body}
			if in.Body.Task != "" {
				tv, err := s.svc.GetTask(ctx, c.sc.Company.ID, in.Body.Task)
				if err != nil {
					return nil, s.fail(ctx, err)
				}
				pin.TaskID = &tv.ID
			}
			m, err := c.cb.PublishChannelReview(ctx, c.sc.Actor, c.sc.Company.ID, pin)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[ChannelMessage]{Body: channelMessageOut(m)}, nil
		})
}
