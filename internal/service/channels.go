package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/channel"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/netguard"
	"github.com/renatobardi/hanko/internal/store"
)

// Canais externos (ADR-0046): cadastro, vínculo de identidade e o cliente dos drivers. A entrada (channels_inbound.go) e a
// saída (channels_outbound.go) ficam em arquivos próprios.

// ChannelsConfig é a configuração do operador para os canais.
type ChannelsConfig struct {
	// PublicURL é a URL pública do Hanko (HANKO_PUBLIC_URL): base do webhook que o provedor chama e dos links dos botões.
	PublicURL string
}

// ChannelJobs é a parte de Service.Jobs que enfileira trabalho de canal. É opcional (como WebhookJobs): um Jobs que não a
// implementa deixa as mensagens de saída pendentes (entregues por DeliverChannelMessage) e processa a entrada na hora.
type ChannelJobs interface {
	SendChannelMessageTx(ctx context.Context, tx pgx.Tx, messageID domain.ID) error
	ProcessChannelInboundTx(ctx context.Context, tx pgx.Tx, messageID domain.ID) error
}

// ChannelMaxAttempts é o total de tentativas de entrega de uma mensagem de saída.
const ChannelMaxAttempts = 6

// ChannelBackoff é a espera depois da tentativa attempt (1-based) que falhou: 30 s, 2 min, 8 min, 32 min, 2 h.
func ChannelBackoff(attempt int) time.Duration {
	d := 30 * time.Second
	for i := 1; i < attempt; i++ {
		d *= 4
		if d >= 2*time.Hour {
			return 2 * time.Hour
		}
	}
	return d
}

// ChannelView é um canal com o que a lista mostra.
type ChannelView struct {
	domain.Channel
	PersonaName string `json:"persona_name,omitempty"`
	Linked      int    `json:"linked"`
	Pending     int    `json:"pending"`
	FailedOut   int    `json:"failed_out"`
	HookPath    string `json:"hook_path"`
}

// CreateChannelInput define um canal novo.
type CreateChannelInput struct {
	Kind             domain.ChannelKind
	Name             string
	Enabled          bool
	PersonaID        *domain.ID
	ProjectID        *domain.ID
	TokenRef         string
	WebhookSecretRef string
	Config           domain.ChannelConfig
}

// UpdateChannelInput muda um canal; nil mantém.
type UpdateChannelInput struct {
	Name             *string
	Enabled          *bool
	PersonaID        *domain.ID
	ClearPersona     bool
	ProjectID        *domain.ID
	ClearProject     bool
	TokenRef         *string
	WebhookSecretRef *string
	Config           *domain.ChannelConfig
}

func channelFromRow(r store.Channel) domain.Channel {
	c := domain.Channel{ID: r.ID, CompanyID: r.CompanyID, Kind: domain.ChannelKind(r.Kind), Name: r.Name, Enabled: r.Enabled,
		PersonaID: r.PersonaID, ProjectID: r.ProjectID, TokenRef: r.TokenRef, WebhookSecretRef: r.WebhookSecretRef,
		InboundToken: r.InboundToken, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
	if len(r.Config) > 2 {
		_ = json.Unmarshal(r.Config, &c.Config)
	}
	return c
}

func identityFromRow(r store.ChannelIdentity) domain.ChannelIdentity {
	i := domain.ChannelIdentity{ID: r.ID, CompanyID: r.CompanyID, ChannelID: r.ChannelID, UserID: r.UserID, ExternalLabel: r.ExternalLabel,
		Status: domain.IdentityStatus(r.Status), CodeExpiresAt: r.CodeExpiresAt, LinkedAt: r.LinkedAt, CreatedAt: r.CreatedAt}
	if r.ExternalID != nil {
		i.ExternalID = *r.ExternalID
	}
	return i
}

func channelMessageFromRow(r store.ChannelMessage) domain.ChannelMessage {
	m := domain.ChannelMessage{ID: r.ID, CompanyID: r.CompanyID, ChannelID: r.ChannelID, Direction: domain.MessageDirection(r.Direction),
		Kind: r.Kind, Peer: r.Peer, Body: r.Body, SubjectType: r.SubjectType, SubjectID: r.SubjectID, Status: domain.ChannelMessageStatus(r.Status),
		StatusReason: r.StatusReason, Attempts: int(r.Attempts), LastError: r.LastError, ProviderMsgID: r.ProviderMsgID, CreatedAt: r.CreatedAt, SentAt: r.SentAt}
	return m
}

var (
	channelMetricsOnce sync.Once
	channelInbound     metric.Int64Counter
	channelOutbound    metric.Int64Counter
)

func countChannel(ctx context.Context, direction string, kind domain.ChannelKind, result string) {
	channelMetricsOnce.Do(func() {
		m := otel.Meter("hanko/channels")
		channelInbound, _ = m.Int64Counter("hanko.channel.inbound", metric.WithDescription("mensagens que entram por canais externos, por driver e resultado"))
		channelOutbound, _ = m.Int64Counter("hanko.channel.outbound", metric.WithDescription("mensagens que saem por canais externos, por driver e resultado"))
	})
	c := channelInbound
	if direction == "out" {
		c = channelOutbound
	}
	if c != nil {
		c.Add(ctx, 1, metric.WithAttributes(attribute.String("driver", string(kind)), attribute.String("result", result)))
	}
}

// channelHTTP é o cliente de saída dos drivers: o das integrações (testes) ou o do netguard.
func (s *Service) channelHTTP() *http.Client {
	if s.Integrations.HTTP != nil {
		return s.Integrations.HTTP
	}
	return netguard.NewClient(s.Integrations.policy(), 30*time.Second)
}

// channelClient resolve o token do canal e monta o cliente do driver.
func (s *Service) channelClient(ctx context.Context, ch store.Channel) (channel.Driver, channel.Client, error) {
	d, ok := channel.For(domain.ChannelKind(ch.Kind))
	if !ok {
		return nil, channel.Client{}, &channel.PermanentError{Err: fmt.Errorf("driver %q desconhecido", ch.Kind)}
	}
	cfg := channelFromRow(ch).Config
	if ch.TokenRef == "" {
		return d, channel.Client{HTTP: s.channelHTTP(), Config: cfg}, nil
	}
	tok, _, err := s.ResolveSecretText(ctx, ch.CompanyID, ch.TokenRef)
	if err != nil {
		return nil, channel.Client{}, &channel.PermanentError{Err: fmt.Errorf("segredo do token indisponível: %w", err)}
	}
	return d, channel.Client{HTTP: s.channelHTTP(), Token: tok, Config: cfg}, nil
}

func newInboundToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("gerar identificador do canal: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// checkChannelRef valida uma referência de segredo do canal (única, da empresa, existente).
func (s *Service) checkChannelRef(ctx context.Context, q *store.Queries, companyID domain.ID, field, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", nil
	}
	if _, ok := soleSecretRef(ref); !ok {
		return "", domain.Invalid(field, "use uma única referência ${secret:NOME}")
	}
	if err := s.checkSecretRefs(ctx, q, companyID, field, ref); err != nil {
		return "", err
	}
	return ref, nil
}

// checkChannelEnable confere o que ligar um canal exige: segredos resolvíveis, persona (GitHub) e configuração.
func (s *Service) checkChannelEnable(ctx context.Context, c domain.Channel) error {
	if c.TokenRef == "" {
		return domain.Invalid("token_ref", "informe o segredo com o token (${secret:NOME}) para ligar o canal")
	}
	if c.WebhookSecretRef == "" {
		return domain.Invalid("webhook_secret_ref", "informe o segredo que autentica a entrada (${secret:NOME}) para ligar o canal")
	}
	if c.Kind == domain.ChannelGitHub && c.PersonaID == nil {
		return domain.Invalid("persona_id", "o canal GitHub precisa de uma persona para acordar")
	}
	for field, ref := range map[string]string{"token_ref": c.TokenRef, "webhook_secret_ref": c.WebhookSecretRef} {
		v, _, err := s.ResolveSecretText(ctx, c.CompanyID, ref)
		if err != nil || strings.TrimSpace(v) == "" {
			return domain.Invalid(field, "o segredo não pôde ser lido (confira Segredos e HANKO_SECRET_KEY)")
		}
	}
	return nil
}

func (s *Service) loadChannelRefs(ctx context.Context, q *store.Queries, companyID domain.ID, persona, project *domain.ID) error {
	if persona != nil {
		if _, err := q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: *persona}); err != nil {
			return domain.Invalid("persona_id", "persona not found in company")
		}
	}
	if project != nil {
		if _, err := q.GetProjectByID(ctx, store.GetProjectByIDParams{CompanyID: companyID, ID: *project}); err != nil {
			return domain.Invalid("project_id", "project not found in company")
		}
	}
	return nil
}

// CreateChannel cadastra um canal (admin). Nasce desligado, a menos que Enabled e tudo que ligar exige esteja certo.
func (s *Service) CreateChannel(ctx context.Context, actor domain.Actor, companyID domain.ID, in CreateChannelInput) (ChannelView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return ChannelView{}, err
	}
	if actor.Type == domain.ActorPersona {
		return ChannelView{}, domain.ErrForbidden
	}
	if !in.Kind.Valid() {
		return ChannelView{}, domain.Invalid("kind", "use telegram ou github")
	}
	name, err := domain.ValidChannelName(in.Name)
	if err != nil {
		return ChannelView{}, err
	}
	cfg, err := domain.NormalizeChannelConfig(in.Kind, in.Config)
	if err != nil {
		return ChannelView{}, err
	}
	tok, err := newInboundToken()
	if err != nil {
		return ChannelView{}, err
	}
	var out ChannelView
	err = s.withTx(ctx, func(t *txc) error {
		tref, err := s.checkChannelRef(ctx, t.q, companyID, "token_ref", in.TokenRef)
		if err != nil {
			return err
		}
		wref, err := s.checkChannelRef(ctx, t.q, companyID, "webhook_secret_ref", in.WebhookSecretRef)
		if err != nil {
			return err
		}
		if err := s.loadChannelRefs(ctx, t.q, companyID, in.PersonaID, in.ProjectID); err != nil {
			return err
		}
		cb, _ := json.Marshal(cfg)
		row, err := t.q.CreateChannel(ctx, store.CreateChannelParams{ID: domain.NewID(), CompanyID: companyID, Kind: string(in.Kind), Name: name,
			Enabled: false, PersonaID: in.PersonaID, ProjectID: in.ProjectID, TokenRef: tref, WebhookSecretRef: wref, Config: cb, InboundToken: tok, Now: t.now})
		if err != nil {
			return mapErr(err, "create channel")
		}
		if in.Enabled {
			if err := s.checkChannelEnable(ctx, channelFromRow(row)); err != nil {
				return err
			}
			if row, err = t.q.UpdateChannel(ctx, store.UpdateChannelParams{CompanyID: companyID, ID: row.ID, Name: row.Name, Enabled: true,
				PersonaID: row.PersonaID, ProjectID: row.ProjectID, TokenRef: row.TokenRef, WebhookSecretRef: row.WebhookSecretRef, Config: row.Config, Now: t.now}); err != nil {
				return mapErr(err, "enable channel")
			}
		}
		if err := t.activity(actor, companyID, "channel.created", "channel", row.ID,
			map[string]any{"kind": row.Kind, "name": row.Name, "enabled": row.Enabled}, ""); err != nil {
			return err
		}
		out, err = channelView(ctx, t.q, row)
		return err
	})
	return out, err
}

func channelView(ctx context.Context, q *store.Queries, row store.Channel) (ChannelView, error) {
	v := ChannelView{Channel: channelFromRow(row)}
	v.HookPath = v.ChannelHookPath()
	if row.PersonaID != nil {
		if p, err := q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: row.CompanyID, ID: *row.PersonaID}); err == nil {
			v.PersonaName = p.Name
		}
	}
	c, err := q.ChannelCounts(ctx, row.ID)
	if err != nil {
		return v, fmt.Errorf("channel counts: %w", err)
	}
	v.Linked, v.Pending, v.FailedOut = int(c.Linked), int(c.Pending), int(c.Failed)
	return v, nil
}

// ListChannels lista os canais da empresa.
func (s *Service) ListChannels(ctx context.Context, companyID domain.ID) ([]ChannelView, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return nil, err
	}
	q := s.read()
	rows, err := q.ListChannels(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list channels: %w", err)
	}
	out := make([]ChannelView, 0, len(rows))
	for _, r := range rows {
		v, err := channelView(ctx, q, r)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// GetChannel resolve ref como id ou nome.
func (s *Service) GetChannel(ctx context.Context, companyID domain.ID, ref string) (ChannelView, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return ChannelView{}, err
	}
	row, err := s.channelRow(ctx, s.read(), companyID, ref)
	if err != nil {
		return ChannelView{}, err
	}
	return channelView(ctx, s.read(), row)
}

func (s *Service) channelRow(ctx context.Context, q *store.Queries, companyID domain.ID, ref string) (store.Channel, error) {
	if id, err := uuid.Parse(ref); err == nil {
		r, err := q.GetChannel(ctx, store.GetChannelParams{CompanyID: companyID, ID: id})
		return r, mapErr(err, "get channel")
	}
	r, err := q.GetChannelByName(ctx, store.GetChannelByNameParams{CompanyID: companyID, Name: ref})
	return r, mapErr(err, "get channel")
}

// UpdateChannel muda um canal (admin).
func (s *Service) UpdateChannel(ctx context.Context, actor domain.Actor, companyID, channelID domain.ID, in UpdateChannelInput) (ChannelView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return ChannelView{}, err
	}
	if actor.Type == domain.ActorPersona {
		return ChannelView{}, domain.ErrForbidden
	}
	if in.ClearPersona && in.PersonaID != nil || in.ClearProject && in.ProjectID != nil {
		return ChannelView{}, domain.Invalid("persona_id", "cannot set and clear at once")
	}
	var out ChannelView
	err := s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetChannelForUpdate(ctx, store.GetChannelForUpdateParams{CompanyID: companyID, ID: channelID})
		if err != nil {
			return mapErr(err, "get channel")
		}
		c := channelFromRow(cur)
		var changed []string
		if in.Name != nil {
			n, err := domain.ValidChannelName(*in.Name)
			if err != nil {
				return err
			}
			if n != c.Name {
				c.Name = n
				changed = append(changed, "name")
			}
		}
		switch {
		case in.ClearPersona && c.PersonaID != nil:
			c.PersonaID = nil
			changed = append(changed, "persona_id")
		case in.PersonaID != nil && (c.PersonaID == nil || *c.PersonaID != *in.PersonaID):
			c.PersonaID = in.PersonaID
			changed = append(changed, "persona_id")
		}
		switch {
		case in.ClearProject && c.ProjectID != nil:
			c.ProjectID = nil
			changed = append(changed, "project_id")
		case in.ProjectID != nil && (c.ProjectID == nil || *c.ProjectID != *in.ProjectID):
			c.ProjectID = in.ProjectID
			changed = append(changed, "project_id")
		}
		if err := s.loadChannelRefs(ctx, t.q, companyID, in.PersonaID, in.ProjectID); err != nil {
			return err
		}
		if in.TokenRef != nil {
			ref, err := s.checkChannelRef(ctx, t.q, companyID, "token_ref", *in.TokenRef)
			if err != nil {
				return err
			}
			if ref != c.TokenRef {
				c.TokenRef = ref
				changed = append(changed, "token_ref")
			}
		}
		if in.WebhookSecretRef != nil {
			ref, err := s.checkChannelRef(ctx, t.q, companyID, "webhook_secret_ref", *in.WebhookSecretRef)
			if err != nil {
				return err
			}
			if ref != c.WebhookSecretRef {
				c.WebhookSecretRef = ref
				changed = append(changed, "webhook_secret_ref")
			}
		}
		if in.Config != nil {
			nc, err := domain.NormalizeChannelConfig(c.Kind, *in.Config)
			if err != nil {
				return err
			}
			if a, b := jsonText(nc), jsonText(c.Config); a != b {
				c.Config = nc
				changed = append(changed, "config")
			}
		}
		verb := "channel.updated"
		if in.Enabled != nil && *in.Enabled != c.Enabled {
			c.Enabled = *in.Enabled
			verb = "channel.disabled"
			if c.Enabled {
				verb = "channel.enabled"
			}
			changed = append(changed, "enabled")
		}
		if len(changed) == 0 {
			out, err = channelView(ctx, t.q, cur)
			return err
		}
		if c.Enabled {
			if err := s.checkChannelEnable(ctx, c); err != nil {
				return err
			}
		}
		cb, _ := json.Marshal(c.Config)
		row, err := t.q.UpdateChannel(ctx, store.UpdateChannelParams{CompanyID: companyID, ID: channelID, Name: c.Name, Enabled: c.Enabled,
			PersonaID: c.PersonaID, ProjectID: c.ProjectID, TokenRef: c.TokenRef, WebhookSecretRef: c.WebhookSecretRef, Config: cb, Now: t.now})
		if err != nil {
			return mapErr(err, "update channel")
		}
		if err := t.activity(actor, companyID, verb, "channel", channelID, map[string]any{"fields": changed, "name": row.Name}, ""); err != nil {
			return err
		}
		out, err = channelView(ctx, t.q, row)
		return err
	})
	return out, err
}

func jsonText(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// DeleteChannel apaga o canal com vínculos, threads e mensagens (admin).
func (s *Service) DeleteChannel(ctx context.Context, actor domain.Actor, companyID, channelID domain.ID) error {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return err
	}
	if actor.Type == domain.ActorPersona {
		return domain.ErrForbidden
	}
	return s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetChannelForUpdate(ctx, store.GetChannelForUpdateParams{CompanyID: companyID, ID: channelID})
		if err != nil {
			return mapErr(err, "get channel")
		}
		if _, err := t.q.DeleteChannel(ctx, store.DeleteChannelParams{CompanyID: companyID, ID: channelID}); err != nil {
			return fmt.Errorf("delete channel: %w", err)
		}
		return t.activity(actor, companyID, "channel.deleted", "channel", channelID, map[string]any{"kind": cur.Kind, "name": cur.Name}, "")
	})
}

// ChannelConnection é o resultado de ligar o canal ao provedor.
type ChannelConnection struct {
	URL        string `json:"url"`
	Registered bool   `json:"registered"`
	Note       string `json:"note,omitempty"`
}

// ConnectChannel registra o webhook no provedor quando o driver sabe (Telegram: setWebhook) e devolve a URL de entrada
// para o que for manual (GitHub: configurar o webhook do repositório).
func (s *Service) ConnectChannel(ctx context.Context, actor domain.Actor, companyID, channelID domain.ID) (ChannelConnection, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return ChannelConnection{}, err
	}
	if actor.Type == domain.ActorPersona {
		return ChannelConnection{}, domain.ErrForbidden
	}
	row, err := s.read().GetChannel(ctx, store.GetChannelParams{CompanyID: companyID, ID: channelID})
	if err != nil {
		return ChannelConnection{}, mapErr(err, "get channel")
	}
	res := ChannelConnection{}
	base := strings.TrimRight(s.Channels.PublicURL, "/")
	res.URL = base + channelFromRow(row).ChannelHookPath()
	if base == "" {
		res.URL = channelFromRow(row).ChannelHookPath()
		res.Note = "HANKO_PUBLIC_URL não está configurada: registre no provedor a URL pública do Hanko seguida deste caminho."
		return res, nil
	}
	if domain.ChannelKind(row.Kind) != domain.ChannelTelegram {
		res.Note = "Configure o webhook no repositório (Settings › Webhooks): conteúdo application/json, o segredo do canal e os eventos Issue comments, Pull request review comments e Pull request reviews."
		return res, nil
	}
	if row.WebhookSecretRef == "" || row.TokenRef == "" {
		return res, domain.Invalid("token_ref", "informe o token e o segredo do webhook antes de conectar")
	}
	secret, _, err := s.ResolveSecretText(ctx, companyID, row.WebhookSecretRef)
	if err != nil {
		return res, domain.Invalid("webhook_secret_ref", "o segredo do webhook não pôde ser lido")
	}
	d, cl, err := s.channelClient(ctx, row)
	if err != nil {
		return res, domain.Invalid("token_ref", "o segredo do token não pôde ser lido")
	}
	rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := d.Register(rctx, cl, res.URL, secret); err != nil {
		return res, domain.Invalid("connect", err.Error())
	}
	res.Registered = true
	err = s.withTx(ctx, func(t *txc) error {
		return t.activity(actor, companyID, "channel.connected", "channel", channelID, map[string]any{"kind": row.Kind, "name": row.Name}, "")
	})
	return res, err
}

// ---------- vínculo de identidade ----------

// ChannelLink é o código de vínculo recém-gerado.
type ChannelLink struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
	Channel   string    `json:"channel"`
	Kind      string    `json:"kind"`
	// Hint diz onde digitar o código.
	Hint string `json:"hint"`
}

func linkHint(kind domain.ChannelKind, code string) string {
	if kind == domain.ChannelGitHub {
		return "Comente num PR ou issue de um repositório do canal: /hanko vincular " + code
	}
	return "Envie ao bot no Telegram: /vincular " + code
}

// callerUserID devolve o usuário autenticado do contexto (vínculo e revogação exigem uma pessoa).
func callerUserID(ctx context.Context) (domain.User, error) {
	id, ok := auth.IdentityFrom(ctx)
	if !ok {
		return domain.User{}, domain.Invalid("user", "exige uma pessoa autenticada")
	}
	return id.User, nil
}

// CreateChannelLink gera o código de vínculo da pessoa autenticada neste canal (vale 15 minutos). Pedir de novo troca o código.
func (s *Service) CreateChannelLink(ctx context.Context, actor domain.Actor, companyID, channelID domain.ID) (ChannelLink, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return ChannelLink{}, err
	}
	user, err := callerUserID(ctx)
	if err != nil {
		return ChannelLink{}, err
	}
	var out ChannelLink
	for attempt := 0; attempt < 3; attempt++ {
		raw := make([]byte, 8)
		if _, err := rand.Read(raw); err != nil {
			return out, fmt.Errorf("gerar código de vínculo: %w", err)
		}
		code := domain.LinkCodeFromBytes(raw)
		err = s.withTx(ctx, func(t *txc) error {
			ch, err := t.q.GetChannelForUpdate(ctx, store.GetChannelForUpdateParams{CompanyID: companyID, ID: channelID})
			if err != nil {
				return mapErr(err, "get channel")
			}
			exp := t.now.Add(domain.ChannelLinkTTL)
			cur, err := t.q.GetChannelIdentityForUser(ctx, store.GetChannelIdentityForUserParams{ChannelID: channelID, UserID: user.ID})
			switch {
			case err == nil && domain.IdentityStatus(cur.Status) == domain.IdentityLinked:
				return fmt.Errorf("você já está vinculado a este canal; desvincule antes de vincular outra conta: %w", domain.ErrConflict)
			case err == nil:
				_, err = t.q.SetChannelIdentityCode(ctx, store.SetChannelIdentityCodeParams{ID: cur.ID, CodeHash: domain.HashLinkCode(code), CodeExpiresAt: &exp, Now: t.now})
			case isNoRows(err):
				_, err = t.q.InsertChannelIdentity(ctx, store.InsertChannelIdentityParams{ID: domain.NewID(), CompanyID: companyID, ChannelID: channelID,
					UserID: user.ID, Status: string(domain.IdentityPending), CodeHash: domain.HashLinkCode(code), CodeExpiresAt: &exp, Now: t.now})
			}
			if err != nil {
				return mapErr(err, "channel link code")
			}
			if err := t.activity(user.Actor(), companyID, "channel.identity_requested", "channel", channelID,
				map[string]any{"user_id": user.ID, "channel": ch.Name}, ""); err != nil {
				return err
			}
			out = ChannelLink{Code: domain.FormatLinkCode(code), ExpiresAt: exp, Channel: ch.Name, Kind: ch.Kind, Hint: linkHint(domain.ChannelKind(ch.Kind), domain.FormatLinkCode(code))}
			return nil
		})
		if err == nil || !isUniqueOn(err, "channel_identities_code_uq") {
			break
		}
	}
	_ = actor
	return out, err
}

// ListChannelIdentities lista os vínculos (ativos e pendentes) do canal (admin).
func (s *Service) ListChannelIdentities(ctx context.Context, companyID, channelID domain.ID) ([]domain.ChannelIdentity, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return nil, err
	}
	rows, err := s.read().ListChannelIdentities(ctx, store.ListChannelIdentitiesParams{CompanyID: companyID, ChannelID: channelID})
	if err != nil {
		return nil, fmt.Errorf("list channel identities: %w", err)
	}
	out := make([]domain.ChannelIdentity, len(rows))
	for i, r := range rows {
		id := identityFromRow(store.ChannelIdentity{ID: r.ID, CompanyID: r.CompanyID, ChannelID: r.ChannelID, UserID: r.UserID, ExternalID: r.ExternalID,
			ExternalLabel: r.ExternalLabel, Status: r.Status, CodeExpiresAt: r.CodeExpiresAt, LinkedAt: r.LinkedAt, CreatedAt: r.CreatedAt})
		id.UserEmail, id.UserName = r.UserEmail, r.UserName
		out[i] = id
	}
	return out, nil
}

// MyChannelIdentity devolve o vínculo da pessoa autenticada no canal (nil se não houver).
func (s *Service) MyChannelIdentity(ctx context.Context, companyID, channelID domain.ID) (*domain.ChannelIdentity, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return nil, err
	}
	user, err := callerUserID(ctx)
	if err != nil {
		return nil, nil //nolint:nilerr // sem pessoa autenticada não há vínculo a mostrar
	}
	r, err := s.read().GetChannelIdentityForUser(ctx, store.GetChannelIdentityForUserParams{ChannelID: channelID, UserID: user.ID})
	if isNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get channel identity: %w", err)
	}
	i := identityFromRow(r)
	return &i, nil
}

// RevokeChannelIdentity revoga um vínculo: a própria pessoa o seu, o admin qualquer um. Vale na hora.
func (s *Service) RevokeChannelIdentity(ctx context.Context, actor domain.Actor, companyID, channelID, identityID domain.ID) error {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return err
	}
	user, err := callerUserID(ctx)
	if err != nil {
		return err
	}
	return s.withTx(ctx, func(t *txc) error {
		ident, err := t.q.GetChannelIdentityByID(ctx, store.GetChannelIdentityByIDParams{CompanyID: companyID, ID: identityID})
		if err != nil || ident.ChannelID != channelID {
			return notFound("get channel identity")
		}
		if domain.IdentityStatus(ident.Status) == domain.IdentityRevoked {
			return nil
		}
		if ident.UserID != user.ID {
			if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
				return err
			}
		}
		if _, err := t.q.RevokeChannelIdentity(ctx, store.RevokeChannelIdentityParams{ID: identityID, Now: t.now}); err != nil {
			return mapErr(err, "revoke channel identity")
		}
		return t.activity(user.Actor(), companyID, "channel.identity_revoked", "channel", channelID,
			map[string]any{"user_id": ident.UserID, "by_self": ident.UserID == user.ID}, "")
	})
}

// ListChannelMessages lista as mensagens recentes do canal com o estado de entrega (admin).
func (s *Service) ListChannelMessages(ctx context.Context, companyID, channelID domain.ID, limit int) ([]domain.ChannelMessage, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return nil, err
	}
	q := s.read()
	if _, err := q.GetChannel(ctx, store.GetChannelParams{CompanyID: companyID, ID: channelID}); err != nil {
		return nil, mapErr(err, "get channel")
	}
	rows, err := q.ListChannelMessages(ctx, store.ListChannelMessagesParams{CompanyID: companyID, ChannelID: channelID, Lim: i32(clamp(limit, 50, 200))})
	if err != nil {
		return nil, fmt.Errorf("list channel messages: %w", err)
	}
	out := make([]domain.ChannelMessage, len(rows))
	for i, r := range rows {
		out[i] = channelMessageFromRow(r)
	}
	return out, nil
}

// errors usado por arquivos irmãos.
var errChannelDisabled = errors.New("o canal está desligado")
