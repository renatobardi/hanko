package service

import (
	"context"
	"crypto/subtle"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Acesso (ADR-0057): login da CLI pelo navegador, pedidos de entrada na empresa e a lista de contas da instalação.

// ---------- login da CLI ----------

const (
	// CLIAuthTTL é a validade de um pedido de login da CLI.
	CLIAuthTTL = 10 * time.Minute
	// MaxOpenCLIAuth é o teto de pedidos abertos ao mesmo tempo (o endpoint de criação é público).
	MaxOpenCLIAuth = 200
	// CLIAuthPollEvery é o intervalo de consulta sugerido à CLI.
	CLIAuthPollEvery = 2 * time.Second
)

// CLIAuthStart é a resposta da criação: o código vai na URL do navegador e o segredo de consulta só a CLI conhece.
type CLIAuthStart struct {
	Code      string
	PollToken string
	ExpiresAt time.Time
}

func cliAuthFromRow(r store.CliAuthRequest) domain.CLIAuthRequest {
	return domain.CLIAuthRequest{ID: r.ID, Device: r.Device, Scope: domain.KeyScope(r.Scope), Status: domain.CLIAuthStatus(r.Status),
		UserID: r.UserID, CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt}
}

// StartCLIAuth abre um pedido de login (público: quem chama ainda não tem credencial). device é o nome do
// dispositivo que a pessoa vê ao autorizar.
func (s *Service) StartCLIAuth(ctx context.Context, device string, scope domain.KeyScope) (CLIAuthStart, error) {
	ctx, span := lifecycleSpan(ctx, "cli_auth.start")
	defer span.End()
	device = strings.TrimSpace(device)
	if device == "" {
		return CLIAuthStart{}, domain.Invalid("device", "required")
	}
	if len([]rune(device)) > 120 {
		return CLIAuthStart{}, domain.Invalid("device", "máximo de 120 caracteres")
	}
	if scope == "" {
		scope = domain.KeyScopeFull
	}
	if !scope.Valid() {
		return CLIAuthStart{}, domain.Invalid("scope", "want full or read")
	}
	code, codeHash, err := auth.NewCLIAuthCode()
	if err != nil {
		return CLIAuthStart{}, err
	}
	poll, pollHash, err := auth.NewSessionToken()
	if err != nil {
		return CLIAuthStart{}, err
	}
	now := s.Now().UTC()
	var out CLIAuthStart
	err = s.withTx(ctx, func(t *txc) error {
		if err := t.q.PruneCLIAuthRequests(ctx, now.Add(-time.Hour)); err != nil {
			return fmt.Errorf("prune cli auth: %w", err)
		}
		open, err := t.q.CountOpenCLIAuthRequests(ctx, now)
		if err != nil {
			return fmt.Errorf("count cli auth: %w", err)
		}
		if open >= MaxOpenCLIAuth {
			return fmt.Errorf("pedidos de login demais em andamento; tente de novo em instantes: %w", domain.ErrConflict)
		}
		row, err := t.q.CreateCLIAuthRequest(ctx, store.CreateCLIAuthRequestParams{ID: domain.NewID(), CodeHash: codeHash, PollHash: pollHash,
			Device: device, Scope: string(scope), Now: now, ExpiresAt: now.Add(CLIAuthTTL)})
		if err != nil {
			return mapErr(err, "create cli auth")
		}
		out = CLIAuthStart{Code: code, PollToken: poll, ExpiresAt: row.ExpiresAt}
		return nil
	})
	return out, err
}

// GetCLIAuth devolve o pedido pendente do código, para a página de autorização. Desconhecido, expirado ou já
// decidido = ErrNotFound (mesma resposta para não revelar qual).
func (s *Service) GetCLIAuth(ctx context.Context, code string) (domain.CLIAuthRequest, error) {
	row, err := s.read().GetCLIAuthByCode(ctx, auth.HashToken(code))
	if err != nil {
		return domain.CLIAuthRequest{}, mapErr(err, "cli auth")
	}
	r := cliAuthFromRow(row)
	if r.Status != domain.CLIAuthPending || r.Expired(s.Now().UTC()) {
		return domain.CLIAuthRequest{}, notFound("cli auth")
	}
	return r, nil
}

// DecideCLIAuth autoriza ou nega o pedido em nome do usuário logado (sessão). Fica em admin_audit.
func (s *Service) DecideCLIAuth(ctx context.Context, userID domain.ID, code string, approve bool) (domain.CLIAuthRequest, error) {
	ctx, span := lifecycleSpan(ctx, "cli_auth.decide")
	defer span.End()
	status, action := domain.CLIAuthDenied, "cli_auth.deny"
	if approve {
		status, action = domain.CLIAuthApproved, "cli_auth.approve"
	}
	var out domain.CLIAuthRequest
	err := s.withTx(ctx, func(t *txc) error {
		u, err := t.q.GetUserByID(ctx, userID)
		if err != nil {
			return mapErr(err, "user")
		}
		row, err := t.q.DecideCLIAuth(ctx, store.DecideCLIAuthParams{Status: string(status), UserID: &userID, Now: &t.now, CodeHash: auth.HashToken(code)})
		if err != nil {
			return mapErr(err, "cli auth")
		}
		out = cliAuthFromRow(row)
		return s.recordAdmin(ctx, t.q, action, u.Email, &userID, "ok")
	})
	if err == nil {
		s.logAdmin(ctx, slog.LevelInfo, action, out.Device, &userID, "ok", "scope", string(out.Scope))
	}
	return out, err
}

// CLIAuthPoll é a resposta da consulta da CLI. APIKey só vem uma vez, na aprovação.
type CLIAuthPoll struct {
	Status domain.CLIAuthStatus
	APIKey string
	Key    domain.APIKey
	User   domain.User
}

// PollCLIAuth devolve o estado do pedido. Aprovado, cria a API key (nome = dispositivo, com escopo e dispositivo) uma
// única vez e marca o pedido como consumido; consultas seguintes devolvem consumed sem chave. Pedido desconhecido =
// ErrNotFound; pendente vencido = ErrNotFound.
func (s *Service) PollCLIAuth(ctx context.Context, pollToken string) (CLIAuthPoll, error) {
	ctx, span := lifecycleSpan(ctx, "cli_auth.poll")
	defer span.End()
	var out CLIAuthPoll
	err := s.withTx(ctx, func(t *txc) error {
		row, err := t.q.GetCLIAuthByPollForUpdate(ctx, auth.HashToken(pollToken))
		if err != nil {
			return mapErr(err, "cli auth")
		}
		if subtle.ConstantTimeCompare([]byte(row.PollHash), []byte(auth.HashToken(pollToken))) != 1 {
			return notFound("cli auth")
		}
		r := cliAuthFromRow(row)
		out.Status = r.Status
		switch r.Status {
		case domain.CLIAuthPending:
			if r.Expired(t.now) {
				return notFound("cli auth")
			}
		case domain.CLIAuthApproved:
			if r.UserID == nil {
				return fmt.Errorf("cli auth approved without user")
			}
			u, err := t.q.GetUserByID(ctx, *r.UserID)
			if err != nil {
				return mapErr(err, "user")
			}
			if !s.stillAdmitted(u) {
				return domain.ErrUnauthenticated
			}
			name := clip(r.Device, 80)
			key, k, err := createAPIKey(ctx, t.q, t.now, u.ID, name, r.Scope, r.Device)
			if err != nil {
				return err
			}
			if err := t.q.MarkCLIAuthConsumed(ctx, r.ID); err != nil {
				return fmt.Errorf("consume cli auth: %w", err)
			}
			out.APIKey, out.Key, out.User, out.Status = key, k, userFromRow(u), domain.CLIAuthApproved
			return s.recordAdmin(ctx, t.q, "cli_auth.key_issued", u.Email, &u.ID, "ok")
		}
		return nil
	})
	return out, err
}

// ---------- pedidos de entrada ----------

func joinFromRow(r store.ListJoinRequestsRow) domain.JoinRequest {
	var role *domain.MemberRole
	if r.Role != nil {
		v := domain.MemberRole(*r.Role)
		role = &v
	}
	return domain.JoinRequest{ID: r.ID, CompanyID: r.CompanyID, UserID: r.UserID, Email: r.Email, Name: r.Name, Message: r.Message,
		Status: domain.JoinStatus(r.Status), Role: role, DecidedBy: r.DecidedBy, CreatedAt: r.CreatedAt, DecidedAt: r.DecidedAt}
}

// JoinLinkState diz se a empresa tem link de entrada e se está ligado. Admin.
func (s *Service) JoinLinkState(ctx context.Context, companyID domain.ID) (domain.JoinLink, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.JoinLink{}, err
	}
	row, err := s.read().GetJoinLink(ctx, companyID)
	if isNoRows(err) {
		return domain.JoinLink{CompanyID: companyID}, nil
	}
	if err != nil {
		return domain.JoinLink{}, fmt.Errorf("get join link: %w", err)
	}
	return domain.JoinLink{CompanyID: companyID, Enabled: row.Enabled, CreatedAt: row.CreatedAt}, nil
}

// CreateJoinLink gera (ou troca) o link público de entrada e devolve o token em claro, só aqui. Admin.
func (s *Service) CreateJoinLink(ctx context.Context, actor domain.Actor, companyID domain.ID) (string, domain.JoinLink, error) {
	ctx, span := lifecycleSpan(ctx, "join_link.create")
	defer span.End()
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return "", domain.JoinLink{}, err
	}
	tok, hash, err := auth.NewInviteToken()
	if err != nil {
		return "", domain.JoinLink{}, err
	}
	var out domain.JoinLink
	err = s.withTx(ctx, func(t *txc) error {
		row, err := t.q.UpsertJoinLink(ctx, store.UpsertJoinLinkParams{CompanyID: companyID, TokenHash: hash, CreatedBy: actor.ID, Now: t.now})
		if err != nil {
			return mapErr(err, "join link")
		}
		out = domain.JoinLink{CompanyID: companyID, Enabled: row.Enabled, CreatedAt: row.CreatedAt}
		return t.activity(actor, companyID, "join_link.created", "company", companyID, nil, "")
	})
	if err != nil {
		return "", domain.JoinLink{}, err
	}
	return tok, out, nil
}

// RevokeJoinLink desliga o link público. Sem link ligado = ErrNotFound. Admin.
func (s *Service) RevokeJoinLink(ctx context.Context, actor domain.Actor, companyID domain.ID) error {
	ctx, span := lifecycleSpan(ctx, "join_link.revoke")
	defer span.End()
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return err
	}
	return s.withTx(ctx, func(t *txc) error {
		n, err := t.q.DisableJoinLink(ctx, companyID)
		if err != nil {
			return fmt.Errorf("disable join link: %w", err)
		}
		if n == 0 {
			return notFound("join link")
		}
		return t.activity(actor, companyID, "join_link.revoked", "company", companyID, nil, "")
	})
}

// JoinLinkInfo devolve o nome da empresa de um link ligado, para a página pública. Token desconhecido ou desligado =
// ErrNotFound (a mesma resposta).
func (s *Service) JoinLinkInfo(ctx context.Context, token string) (domain.Company, error) {
	q := s.read()
	link, err := q.GetJoinLinkByHash(ctx, auth.HashToken(token))
	if err != nil {
		return domain.Company{}, mapErr(err, "join link")
	}
	co, err := q.GetCompanyByID(ctx, link.CompanyID)
	if err != nil {
		return domain.Company{}, mapErr(err, "join link")
	}
	if co.ArchivedAt != nil {
		return domain.Company{}, notFound("join link")
	}
	return companyFromRow(co), nil
}

// maxJoinMessageRunes é o tamanho máximo da mensagem do pedido.
const maxJoinMessageRunes = 500

// RequestJoin registra o pedido do usuário logado de entrar na empresa do link. Já membro = ErrConflict; pedido aberto
// repetido = ErrConflict.
func (s *Service) RequestJoin(ctx context.Context, userID domain.ID, token, message string) (domain.JoinRequest, error) {
	ctx, span := lifecycleSpan(ctx, "join.request")
	defer span.End()
	message = strings.TrimSpace(message)
	if len([]rune(message)) > maxJoinMessageRunes {
		return domain.JoinRequest{}, domain.Invalid("message", fmt.Sprintf("máximo de %d caracteres", maxJoinMessageRunes))
	}
	co, err := s.JoinLinkInfo(ctx, token)
	if err != nil {
		return domain.JoinRequest{}, err
	}
	var out domain.JoinRequest
	err = s.withTx(ctx, func(t *txc) error {
		u, err := t.q.GetUserByID(ctx, userID)
		if err != nil {
			return mapErr(err, "user")
		}
		if err := t.q.LockCompanyMembers(ctx, co.ID); err != nil {
			return mapErr(err, "company")
		}
		if _, err := t.q.GetMemberRole(ctx, store.GetMemberRoleParams{CompanyID: co.ID, UserID: userID}); err == nil {
			return fmt.Errorf("você já participa desta empresa: %w", domain.ErrConflict)
		} else if !isNoRows(err) {
			return fmt.Errorf("get member: %w", err)
		}
		if u.Role == string(domain.RoleOwner) {
			return fmt.Errorf("o dono da instalação já enxerga todas as empresas: %w", domain.ErrConflict)
		}
		row, err := t.q.CreateJoinRequest(ctx, store.CreateJoinRequestParams{ID: domain.NewID(), CompanyID: co.ID, UserID: userID, Message: message, Now: t.now})
		if err != nil {
			if isUnique(err) {
				return fmt.Errorf("você já tem um pedido aberto nesta empresa: %w", domain.ErrConflict)
			}
			return mapErr(err, "join request")
		}
		out = domain.JoinRequest{ID: row.ID, CompanyID: row.CompanyID, UserID: userID, Email: u.Email, Name: u.Name, Message: row.Message,
			Status: domain.JoinPending, CreatedAt: row.CreatedAt}
		return t.activity(userFromRow(u).Actor(), co.ID, "join.requested", "join_request", row.ID, map[string]any{"email": u.Email}, "")
	})
	return out, err
}

// ListJoinRequests lista os pedidos da empresa (abertos primeiro). Admin.
func (s *Service) ListJoinRequests(ctx context.Context, companyID domain.ID, onlyPending bool) ([]domain.JoinRequest, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return nil, err
	}
	rows, err := s.read().ListJoinRequests(ctx, store.ListJoinRequestsParams{CompanyID: companyID, OnlyPending: onlyPending})
	if err != nil {
		return nil, fmt.Errorf("list join requests: %w", err)
	}
	out := make([]domain.JoinRequest, len(rows))
	for i, r := range rows {
		out[i] = joinFromRow(r)
	}
	return out, nil
}

// DecideJoinRequest aprova (com o papel escolhido) ou nega um pedido aberto. Admin só concede member/viewer; admin e
// owner exigem owner (como nos convites).
func (s *Service) DecideJoinRequest(ctx context.Context, actor domain.Actor, companyID, requestID domain.ID, approve bool, role domain.MemberRole) (domain.JoinRequest, error) {
	ctx, span := lifecycleSpan(ctx, "join.decide")
	defer span.End()
	by, _, err := s.callerRole(ctx, companyID)
	if err != nil {
		return domain.JoinRequest{}, err
	}
	if !by.AtLeast(domain.MemberAdmin) {
		return domain.JoinRequest{}, domain.ErrForbidden
	}
	if approve {
		if role == "" {
			role = domain.MemberMember
		}
		if !role.Valid() {
			return domain.JoinRequest{}, domain.Invalid("role", "owner, admin, member ou viewer")
		}
		if !by.Manages(role) {
			return domain.JoinRequest{}, domain.ErrForbidden
		}
	}
	var out domain.JoinRequest
	err = s.withTx(ctx, func(t *txc) error {
		if err := t.q.LockCompanyMembers(ctx, companyID); err != nil {
			return mapErr(err, "company")
		}
		cur, err := t.q.GetJoinRequestForUpdate(ctx, store.GetJoinRequestForUpdateParams{CompanyID: companyID, ID: requestID})
		if err != nil {
			return mapErr(err, "join request")
		}
		if cur.Status != string(domain.JoinPending) {
			return fmt.Errorf("o pedido já foi decidido: %w", domain.ErrConflict)
		}
		u, err := t.q.GetUserByID(ctx, cur.UserID)
		if err != nil {
			return mapErr(err, "user")
		}
		status, verb := string(domain.JoinDenied), "join.denied"
		var rolePtr *string
		if approve {
			status, verb = string(domain.JoinApproved), "join.approved"
			r := string(role)
			rolePtr = &r
			if _, err := t.q.GetMemberRole(ctx, store.GetMemberRoleParams{CompanyID: companyID, UserID: u.ID}); err == nil {
				return fmt.Errorf("a pessoa já participa da empresa: %w", domain.ErrConflict)
			} else if !isNoRows(err) {
				return fmt.Errorf("get member: %w", err)
			}
			if _, err := t.q.AddMember(ctx, store.AddMemberParams{CompanyID: companyID, UserID: u.ID, Role: r, InvitedBy: actor.ID, Now: t.now}); err != nil {
				return mapErr(err, "add member")
			}
			if err := t.q.MarkUserInvited(ctx, u.ID); err != nil { // como o convite: vale como admissão (ADR-0021)
				return fmt.Errorf("mark invited: %w", err)
			}
			if err := t.activity(userFromRow(u).Actor(), companyID, "member.joined", "user", u.ID,
				map[string]any{"email": u.Email, "role": role, "via": "join_request", "approved_by": actor.ID}, ""); err != nil {
				return err
			}
		}
		row, err := t.q.DecideJoinRequest(ctx, store.DecideJoinRequestParams{Status: status, Role: rolePtr, DecidedBy: actor.ID, Now: &t.now,
			CompanyID: companyID, ID: requestID})
		if err != nil {
			return mapErr(err, "join request")
		}
		out = domain.JoinRequest{ID: row.ID, CompanyID: companyID, UserID: u.ID, Email: u.Email, Name: u.Name, Message: row.Message,
			Status: domain.JoinStatus(row.Status), DecidedBy: row.DecidedBy, CreatedAt: row.CreatedAt, DecidedAt: row.DecidedAt}
		if approve {
			out.Role = &role
		}
		return t.activity(actor, companyID, verb, "join_request", requestID, map[string]any{"email": u.Email, "role": role}, "")
	})
	return out, err
}

// ---------- contas da instalação ----------

// ListInstanceUsers lista as contas da instalação com o que cada uma tem (empresas e chaves ativas). Dono ou admin da
// instalação (no banco; o papel do contexto não é confiado). Sem identidade (trusted), liberado.
func (s *Service) ListInstanceUsers(ctx context.Context) ([]domain.InstanceUser, error) {
	q := s.read()
	if id, ok := auth.IdentityFrom(ctx); ok {
		u, err := q.GetUserByID(ctx, id.User.ID)
		if err != nil {
			if isNoRows(err) {
				return nil, domain.ErrForbidden
			}
			return nil, fmt.Errorf("get user: %w", err)
		}
		if u.Role != string(domain.RoleOwner) && u.Role != string(domain.RoleAdmin) {
			return nil, domain.ErrForbidden
		}
	}
	users, err := q.ListUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	keys, err := q.CountActiveAPIKeysByUser(ctx)
	if err != nil {
		return nil, fmt.Errorf("count api keys: %w", err)
	}
	mem, err := q.CountMembershipsByUser(ctx)
	if err != nil {
		return nil, fmt.Errorf("count memberships: %w", err)
	}
	kn, mn := map[domain.ID]int64{}, map[domain.ID]int64{}
	for _, k := range keys {
		kn[k.UserID] = k.N
	}
	for _, m := range mem {
		mn[m.UserID] = m.N
	}
	out := make([]domain.InstanceUser, len(users))
	for i, u := range users {
		out[i] = domain.InstanceUser{User: userFromRow(u), Companies: mn[u.ID], ActiveKeys: kn[u.ID]}
	}
	return out, nil
}

// Whoami descreve quem está chamando: a identidade resolvida pelo Guard, com a chave quando é API key.
type Whoami struct {
	Authenticated bool
	User          domain.User
	Via           auth.Via
	Key           *domain.APIKey
}

// WhoamiOf monta a resposta do `whoami` a partir da identidade do contexto.
func (s *Service) WhoamiOf(ctx context.Context) (Whoami, error) {
	id, ok := auth.IdentityFrom(ctx)
	if !ok {
		return Whoami{}, nil
	}
	w := Whoami{Authenticated: true, User: id.User, Via: id.Via}
	if id.Via == auth.ViaAPIKey && id.KeyID != nil {
		row, err := s.read().GetAPIKeyByID(ctx, store.GetAPIKeyByIDParams{ID: *id.KeyID, UserID: id.User.ID})
		if err != nil {
			return Whoami{}, mapErr(err, "api key")
		}
		k := apiKeyFromRow(row)
		w.Key = &k
	}
	return w, nil
}
