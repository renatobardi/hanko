package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/webpush"
)

// Caixa por pessoa e avisos (ADR-0042). O estado (lido, arquivado) é da pessoa autenticada; sem usuário (modo trusted)
// a Caixa lista tudo como não lido e marcar é 422. Run tokens (personas) não usam a Caixa pessoal.

// InboxItem é um item da Caixa de entrada com o estado da pessoa.
type InboxItem struct {
	Kind          string    `json:"kind" enum:"gate,task_blocked,task_held,run_failed,persona_error,alert,subscription,mention,goal_achieved,external_agent,external_host" doc:"Tipo do item; o id é o da origem (gate, task, execução, persona, alerta, assinatura, menção, meta, agente ou host externo)."`
	ID            string    `json:"id" format:"uuid"`
	Title         string    `json:"title"`
	Detail        string    `json:"detail,omitempty" doc:"Erro, motivo da pausa ou trecho do comentário."`
	TaskKey       string    `json:"task_key,omitempty" example:"HNK-12"`
	Ref           string    `json:"ref,omitempty" doc:"Chave da task, slug da persona/meta ou id do gate/execução, para navegar."`
	At            time.Time `json:"at"`
	Read          bool      `json:"read"`
	Archived      bool      `json:"archived"`
	Informational bool      `json:"informational,omitempty" doc:"Ninguém precisa agir: fora do contador e do selo."`
}

// InboxList é a Caixa na visão pedida, com os totais da pessoa.
type InboxList struct {
	Items    []InboxItem `json:"items" doc:"Do mais novo para o mais antigo."`
	Count    int         `json:"count" doc:"Itens que pedem ação e não estão arquivados."`
	Unread   int         `json:"unread" doc:"Itens que pedem ação, não lidos e não arquivados (o selo do menu)."`
	Archived int         `json:"archived" doc:"Itens que pedem ação e estão arquivados."`
	Personal bool        `json:"personal" doc:"Falso sem pessoa autenticada (modo trusted): sem lido/arquivado."`
}

type inboxIn struct {
	CompanyPath
	View string `query:"view" enum:"all,unread,archived" default:"all" doc:"all = não arquivados; unread = não arquivados e não lidos; archived."`
}

// InboxRef identifica um item para marcar.
type InboxRef struct {
	Kind string `json:"kind" enum:"gate,task_blocked,task_held,run_failed,persona_error,alert,subscription,mention,goal_achieved,external_agent,external_host"`
	ID   string `json:"id" format:"uuid"`
}

type inboxStateIn struct {
	CompanyPath
	Body struct {
		Action string     `json:"action" enum:"read,unread,archive,unarchive,read_all" doc:"read_all marca como lidos todos os itens visíveis (items é ignorado)."`
		Items  []InboxRef `json:"items,omitempty" maxItems:"500"`
	}
}

// InboxStateResult conta os itens afetados.
type InboxStateResult struct {
	Updated int `json:"updated"`
}

// NotifyPref diz por quais canais a pessoa recebe um tipo de aviso.
type NotifyPref struct {
	Kind  string `json:"kind" enum:"mention,gate,run_failed,alert"`
	Toast bool   `json:"toast" doc:"Aviso ao vivo no canto da tela."`
	Push  bool   `json:"push" doc:"Notificação do navegador/PWA (web push)."`
}

type notifyPrefIn struct {
	CompanyPath
	Kind string `path:"kind" enum:"mention,gate,run_failed,alert"`
	Body struct {
		Toast bool `json:"toast"`
		Push  bool `json:"push"`
	}
}

// VapidKey é a chave pública do servidor para pushManager.subscribe.
type VapidKey struct {
	PublicKey string `json:"public_key" doc:"applicationServerKey (P-256 não comprimida, base64url)."`
}

// PushSubscription é um aparelho com web push.
type PushSubscription struct {
	ID            string     `json:"id" format:"uuid"`
	Endpoint      string     `json:"endpoint"`
	UserAgent     string     `json:"user_agent,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	LastSuccessAt *time.Time `json:"last_success_at,omitempty"`
}

type pushSubscribeIn struct {
	Body struct {
		Endpoint string `json:"endpoint" minLength:"10" maxLength:"2048" doc:"PushSubscription.endpoint (https)."`
		Keys     struct {
			P256dh string `json:"p256dh" minLength:"1" maxLength:"200"`
			Auth   string `json:"auth" minLength:"1" maxLength:"100"`
		} `json:"keys"`
		UserAgent string `json:"user_agent,omitempty" maxLength:"300"`
	}
}

type pushRefIn struct {
	ID string `path:"id" doc:"Id da assinatura ou o endpoint codificado."`
}

func inboxItemOut(it service.InboxItem) InboxItem {
	return InboxItem{Kind: string(it.Kind), ID: idStr(it.ID), Title: it.Title, Detail: it.Detail, TaskKey: it.TaskKey, Ref: it.Ref, At: it.At,
		Read: it.Read, Archived: it.Archived, Informational: it.Informational}
}

func (s *server) registerInbox(api huma.API) {
	huma.Register(api, op("list-inbox", http.MethodGet, "/companies/{company}/inbox", "inbox", "Caixa de entrada da pessoa (lido/arquivado)"),
		func(ctx context.Context, in *inboxIn) (*ItemOut[InboxList], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			view := domain.InboxView(in.View)
			if view == "" {
				view = domain.InboxViewAll
			}
			ib, err := s.svc.InboxView(ctx, sc.Company.ID, view)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			items := ib.Items()
			out := InboxList{Items: make([]InboxItem, 0, len(items)), Count: ib.Count(), Unread: ib.Unread, Archived: ib.ArchivedCount, Personal: ib.Personal}
			for _, it := range items {
				if domain.InboxVisible(view, domain.InboxItemState{Read: it.Read, Archived: it.Archived}) {
					out.Items = append(out.Items, inboxItemOut(it))
				}
			}
			return &ItemOut[InboxList]{Body: out}, nil
		})

	huma.Register(api, op("set-inbox-state", http.MethodPost, "/companies/{company}/inbox/state", "inbox",
		"Marca itens da Caixa como lidos, não lidos, arquivados ou desarquivados (só para a pessoa)"),
		func(ctx context.Context, in *inboxStateIn) (*ItemOut[InboxStateResult], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			refs := make([]service.InboxRef, 0, len(in.Body.Items))
			for _, r := range in.Body.Items {
				id, err := domain.ParseID(r.ID)
				if err != nil {
					return nil, s.fail(ctx, domain.Invalid("items", "id não é um uuid"))
				}
				refs = append(refs, service.InboxRef{Kind: domain.InboxItemKind(r.Kind), ID: id})
			}
			n, err := s.svc.SetInboxState(ctx, sc.Actor, sc.Company.ID, domain.InboxAction(in.Body.Action), refs)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[InboxStateResult]{Body: InboxStateResult{Updated: n}}, nil
		})

	huma.Register(api, op("list-notification-prefs", http.MethodGet, "/companies/{company}/notifications", "inbox",
		"Preferências de aviso da pessoa nesta empresa"),
		func(ctx context.Context, in *CompanyPath) (*ListOut[NotifyPref], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			ps, err := s.svc.NotifyPrefs(ctx, sc.Company.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(ps, Page{Limit: 500}, func(p domain.NotifyPref) NotifyPref {
				return NotifyPref{Kind: string(p.Kind), Toast: p.Toast, Push: p.Push}
			}), nil
		})

	huma.Register(api, op("set-notification-pref", http.MethodPut, "/companies/{company}/notifications/{kind}", "inbox",
		"Define por quais canais a pessoa recebe um tipo de aviso"),
		func(ctx context.Context, in *notifyPrefIn) (*ItemOut[NotifyPref], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			p := domain.NotifyPref{Kind: domain.NotifyKind(in.Kind), Toast: in.Body.Toast, Push: in.Body.Push}
			if err := s.svc.SetNotifyPref(ctx, sc.Company.ID, p); err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[NotifyPref]{Body: NotifyPref{Kind: in.Kind, Toast: p.Toast, Push: p.Push}}, nil
		})

	huma.Register(api, op("test-notification", http.MethodPost, "/companies/{company}/notifications/test", "inbox",
		"Envia um aviso de teste à própria pessoa"),
		func(ctx context.Context, in *CompanyPath) (*struct{}, error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			if err := s.svc.SendTestNotice(ctx, sc.Company.ID); err != nil {
				return nil, s.fail(ctx, err)
			}
			return nil, nil
		})

	huma.Register(api, op("get-vapid-key", http.MethodGet, "/push/vapid-key", "inbox",
		"Chave pública VAPID do servidor (gerada na primeira vez; exige HANKO_SECRET_KEY)"),
		func(ctx context.Context, _ *struct{}) (*ItemOut[VapidKey], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			k, err := s.svc.PushVapidKey(ctx)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[VapidKey]{Body: VapidKey{PublicKey: k}}, nil
		})

	huma.Register(api, created(op("add-push-subscription", http.MethodPost, "/me/push-subscriptions", "inbox",
		"Registra o web push deste aparelho (a assinatura que o navegador entregou)")),
		func(ctx context.Context, in *pushSubscribeIn) (*ItemOut[PushSubscription], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			v, err := s.svc.AddPushSubscription(ctx, webpush.Subscription{Endpoint: in.Body.Endpoint, P256dh: in.Body.Keys.P256dh, Auth: in.Body.Keys.Auth}, in.Body.UserAgent)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[PushSubscription]{Body: pushSubOut(v)}, nil
		})

	huma.Register(api, op("list-push-subscriptions", http.MethodGet, "/me/push-subscriptions", "inbox", "Aparelhos com web push da pessoa"),
		func(ctx context.Context, _ *struct{}) (*ListOut[PushSubscription], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			vs, err := s.svc.ListPushSubscriptions(ctx)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(vs, Page{Limit: 500}, pushSubOut), nil
		})

	huma.Register(api, op("delete-push-subscription", http.MethodDelete, "/me/push-subscriptions/{id}", "inbox", "Remove um aparelho do web push"),
		func(ctx context.Context, in *pushRefIn) (*struct{}, error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			if err := s.svc.RemovePushSubscription(ctx, in.ID); err != nil {
				return nil, s.fail(ctx, err)
			}
			return nil, nil
		})
}

func pushSubOut(v service.PushSubscriptionView) PushSubscription {
	return PushSubscription{ID: idStr(v.ID), Endpoint: v.Endpoint, UserAgent: v.UserAgent, CreatedAt: v.CreatedAt, LastSuccessAt: v.LastSuccessAt}
}
