package service

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Autenticação (M5, ADR-0007/0014). Usuários e credenciais são globais (não pertencem a
// uma empresa), então estas escritas não geram activity_event (que é por empresa).

// SessionMeta descreve o cliente que abre a sessão.
type SessionMeta struct{ UserAgent, IP string }

// FirebaseLogin é o resultado de um ID token já validado mais a política de admissão.
type FirebaseLogin struct {
	UID, Email, Name string
	// ListsConfigured: HANKO_AUTH_ALLOWED_EMAILS/DOMAINS tem alguma entrada.
	ListsConfigured bool
	// Admitted: o e-mail está nas listas.
	Admitted bool
	// OwnerEmail (HANKO_AUTH_OWNER_EMAIL, opcional): com ele, só esse e-mail vira dono (e é sempre
	// admitido); os demais só entram se estiverem nas listas, como membros.
	OwnerEmail string
	// InviteToken (opcional): token de um convite aberto para este e-mail. Vale como admissão
	// (como membro), mesmo fora das listas e com o dono já criado (ADR-0021). O aceite é à parte
	// (AcceptInvite) e é ele que consome o convite.
	InviteToken string
}

func userFromRow(r store.User) domain.User {
	u := domain.User{ID: r.ID, Email: r.Email, Name: r.Name, Provider: domain.AuthProvider(r.Provider),
		Role: domain.UserRole(r.Role), CreatedAt: r.CreatedAt, LastLoginAt: r.LastLoginAt}
	if r.ExternalID != nil {
		u.ExternalID = *r.ExternalID
	}
	if r.PasswordHash != nil {
		u.PasswordHash = *r.PasswordHash
	}
	return u
}

func apiKeyFromRow(r store.ApiKey) domain.APIKey {
	return domain.APIKey{ID: r.ID, UserID: r.UserID, Name: r.Name, Prefix: r.Prefix,
		CreatedAt: r.CreatedAt, LastUsedAt: r.LastUsedAt, RevokedAt: r.RevokedAt}
}

// NormalizeEmail valida e normaliza (minúsculas) um endereço simples.
func NormalizeEmail(s string) (string, error) {
	s = strings.TrimSpace(s)
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s || a.Name != "" || strings.ContainsAny(s, " <>,;") || len(s) > 254 {
		return "", domain.Invalid("email", "endereço inválido")
	}
	return auth.FoldEmail(s), nil
}

// SameEmail compara dois e-mails normalizando os DOIS lados com NormalizeEmail (nunca strings.EqualFold:
// ele aproxima o sinal de Kelvin de "k"). Endereço inválido de qualquer lado = diferente.
func SameEmail(a, b string) bool {
	na, err := NormalizeEmail(a)
	if err != nil {
		return false
	}
	nb, err := NormalizeEmail(b)
	return err == nil && na == nb
}

// CountUsers devolve quantos usuários existem.
func (s *Service) CountUsers(ctx context.Context) (int, error) {
	n, err := s.read().CountUsers(ctx)
	if err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return int(n), nil
}

// ListUsers devolve todos os usuários (para nomes na UI).
func (s *Service) ListUsers(ctx context.Context) ([]domain.User, error) {
	rows, err := s.read().ListUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	out := make([]domain.User, len(rows))
	for i, r := range rows {
		out[i] = userFromRow(r)
	}
	return out, nil
}

// GetUserByEmail busca sem diferenciar caixa.
func (s *Service) GetUserByEmail(ctx context.Context, email string) (domain.User, error) {
	row, err := s.read().GetUserByEmail(ctx, strings.TrimSpace(email))
	if err != nil {
		return domain.User{}, mapErr(err, "get user")
	}
	return userFromRow(row), nil
}

// CreateOwner cria o primeiro usuário (provider local, papel owner). Só vale enquanto
// não há usuários; depois disso devolve ErrConflict.
func (s *Service) CreateOwner(ctx context.Context, email, name, password string) (domain.User, error) {
	email, err := NormalizeEmail(email)
	if err != nil {
		return domain.User{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return domain.User{}, domain.Invalid("name", "required")
	}
	if err := auth.ValidatePassword(password); err != nil {
		return domain.User{}, domain.Invalid("password", err.Error())
	}
	release, err := s.PasswordGate.Acquire(ctx)
	if err != nil {
		return domain.User{}, err
	}
	hash, err := auth.HashPassword(password)
	release()
	if err != nil {
		return domain.User{}, err
	}
	var out domain.User
	err = s.withTx(ctx, func(t *txc) error {
		if err := t.q.LockUsers(ctx); err != nil {
			return fmt.Errorf("lock users: %w", err)
		}
		n, err := t.q.CountUsers(ctx)
		if err != nil {
			return fmt.Errorf("count users: %w", err)
		}
		if n > 0 {
			return fmt.Errorf("já existe um usuário: %w", domain.ErrConflict)
		}
		row, err := t.q.CreateUser(ctx, store.CreateUserParams{ID: domain.NewID(), Email: email, Name: name,
			Provider: string(domain.ProviderLocal), PasswordHash: &hash, Role: string(domain.RoleOwner), Now: t.now})
		if err != nil {
			return mapErr(err, "create owner")
		}
		out = userFromRow(row)
		return nil
	})
	return out, err
}

// AuthenticateLocal confere e-mail e senha. Falha (usuário inexistente, provider errado ou
// senha errada) devolve sempre o mesmo erro, domain.ErrUnauthenticated, após gastar o mesmo
// custo de hash.
func (s *Service) AuthenticateLocal(ctx context.Context, email, password string) (domain.User, error) {
	if len(password) > auth.MaxPasswordLen*4 {
		return domain.User{}, domain.ErrUnauthenticated
	}
	row, err := s.read().GetUserByEmail(ctx, strings.TrimSpace(email))
	if err != nil || row.Provider != string(domain.ProviderLocal) || row.PasswordHash == nil {
		if err != nil && !isNoRows(err) {
			return domain.User{}, fmt.Errorf("authenticate: %w", err)
		}
		release, gerr := s.PasswordGate.Acquire(ctx)
		if gerr != nil {
			return domain.User{}, gerr
		}
		auth.BurnPassword(password)
		release()
		return domain.User{}, domain.ErrUnauthenticated
	}
	release, gerr := s.PasswordGate.Acquire(ctx)
	if gerr != nil {
		return domain.User{}, gerr
	}
	ok, verr := auth.VerifyPassword(password, *row.PasswordHash)
	release()
	if verr != nil || !ok {
		return domain.User{}, domain.ErrUnauthenticated
	}
	if err := s.read().TouchUserLogin(ctx, store.TouchUserLoginParams{ID: row.ID, Now: s.Now().UTC()}); err != nil {
		return domain.User{}, fmt.Errorf("touch login: %w", err)
	}
	return userFromRow(row), nil
}

// LoginFirebase cria ou atualiza o usuário de um ID token validado, aplicando a política
// de admissão: com listas, só entra quem estiver nelas (o primeiro vira owner); sem listas,
// só o primeiro usuário entra (owner) e os demais são recusados (ErrNotAllowed). Um e-mail
// já usado por outro método ou outro uid é ErrConflict (sem vínculo automático).
func (s *Service) LoginFirebase(ctx context.Context, in FirebaseLogin) (domain.User, error) {
	email, err := NormalizeEmail(in.Email)
	if err != nil {
		return domain.User{}, err
	}
	if in.UID == "" {
		return domain.User{}, domain.Invalid("uid", "required")
	}
	name := strings.TrimSpace(in.Name)
	ownerEmail := auth.FoldEmail(in.OwnerEmail)
	isOwner := ownerEmail != "" && email == ownerEmail
	var out domain.User
	var denied error
	err = s.withTx(ctx, func(t *txc) error {
		if err := t.q.LockUsers(ctx); err != nil {
			return fmt.Errorf("lock users: %w", err)
		}
		uid := in.UID
		invited := false
		if in.InviteToken != "" {
			if inv, err := openInvite(ctx, t.q, in.InviteToken, t.now); err == nil && SameEmail(inv.Email, email) {
				invited = true
			}
		}
		existing, err := t.q.GetUserByExternal(ctx, store.GetUserByExternalParams{Provider: string(domain.ProviderFirebase), ExternalID: &uid})
		switch {
		case err == nil:
			// users.invited só vale enquanto a pessoa tem alguma empresa (ADR-0021). Quem entrou pelo convite
			// e nunca o aceitou (ou perdeu todas as empresas) volta a depender das listas: limpa a marca.
			wasInvited := existing.Invited
			if wasInvited && !invited {
				n, err := t.q.CountUserMemberships(ctx, existing.ID)
				if err != nil {
					return fmt.Errorf("count memberships: %w", err)
				}
				if n == 0 {
					if err := t.q.ClearUserInvited(ctx, existing.ID); err != nil {
						return fmt.Errorf("clear invited: %w", err)
					}
					wasInvited = false
				}
			}
			if in.ListsConfigured && !in.Admitted && !isOwner && !invited && !wasInvited {
				// a recusa sai depois do commit, para a limpeza acima não ser desfeita
				denied = fmt.Errorf("e-mail fora da lista de admissão: %w", domain.ErrNotAllowed)
				return nil
			}
			if name == "" {
				name = existing.Name
			}
			row, err := t.q.UpdateUserProfile(ctx, store.UpdateUserProfileParams{ID: existing.ID, Email: email, Name: name, Now: t.now})
			if err != nil {
				return mapErr(err, "update user")
			}
			out = userFromRow(row)
			return nil
		case !isNoRows(err):
			return fmt.Errorf("get user: %w", err)
		}

		n, err := t.q.CountUsers(ctx)
		if err != nil {
			return fmt.Errorf("count users: %w", err)
		}
		role := domain.RoleMember
		switch {
		case invited:
			// convite aberto para este e-mail: entra como membro (o papel na empresa vem do convite)
		case ownerEmail != "":
			// dono nomeado: só o e-mail dele vira dono; os demais precisam estar nas listas
			if isOwner {
				role = domain.RoleOwner
			} else if !in.ListsConfigured || !in.Admitted {
				return fmt.Errorf("e-mail fora da lista de admissão e diferente do dono configurado: %w", domain.ErrNotAllowed)
			}
		case in.ListsConfigured:
			if !in.Admitted {
				return fmt.Errorf("e-mail fora da lista de admissão: %w", domain.ErrNotAllowed)
			}
			if n == 0 {
				role = domain.RoleOwner
			}
		default:
			if n > 0 {
				return fmt.Errorf("sem lista de admissão e já existe usuário: %w", domain.ErrNotAllowed)
			}
			role = domain.RoleOwner
		}
		row, err := t.q.CreateUser(ctx, store.CreateUserParams{ID: domain.NewID(), Email: email, Name: name,
			Provider: string(domain.ProviderFirebase), ExternalID: &uid, Role: string(role), Now: t.now})
		if err != nil {
			return mapErr(err, "create user")
		}
		out = userFromRow(row)
		if invited { // conta criada pelo convite: a marca vale só para quem entrou por ele
			if err := t.q.MarkUserInvited(ctx, out.ID); err != nil {
				return fmt.Errorf("mark invited: %w", err)
			}
		}
		return nil
	})
	if err == nil && denied != nil {
		return domain.User{}, denied
	}
	return out, err
}

// CreateSession abre uma sessão para o usuário e devolve o token (só aqui em claro). Se
// replaceToken não for vazio, a sessão antiga é removida na mesma transação (rotação).
func (s *Service) CreateSession(ctx context.Context, userID domain.ID, meta SessionMeta, replaceToken string) (string, domain.Session, error) {
	tok, hash, err := auth.NewSessionToken()
	if err != nil {
		return "", domain.Session{}, err
	}
	var out domain.Session
	err = s.withTx(ctx, func(t *txc) error {
		if replaceToken != "" {
			if err := t.q.DeleteSessionByHash(ctx, auth.HashToken(replaceToken)); err != nil {
				return fmt.Errorf("rotate session: %w", err)
			}
		}
		if err := t.q.DeleteExpiredSessions(ctx, t.now); err != nil {
			return fmt.Errorf("purge sessions: %w", err)
		}
		row, err := t.q.CreateSession(ctx, store.CreateSessionParams{ID: domain.NewID(), UserID: userID, TokenHash: hash,
			ExpiresAt: t.now.Add(auth.SessionTTL), AbsoluteExpiresAt: t.now.Add(auth.SessionAbsoluteTTL), UserAgent: clip(meta.UserAgent, 300), Ip: clip(meta.IP, 64), Now: t.now})
		if err != nil {
			return mapErr(err, "create session")
		}
		out = domain.Session{ID: row.ID, UserID: row.UserID, CreatedAt: row.CreatedAt, ExpiresAt: row.ExpiresAt,
			LastSeenAt: row.LastSeenAt, UserAgent: row.UserAgent, IP: row.Ip}
		return nil
	})
	return tok, out, err
}

// stillAdmitted aplica a política de admissão a quem já tem credencial: com listas configuradas, um
// usuário firebase cujo e-mail não passa mais nelas perde o acesso (sair da lista vale na hora, não só
// no próximo login). O dono configurado (HANKO_AUTH_OWNER_EMAIL) é o único que sempre passa.
func (s *Service) stillAdmitted(u store.User) bool {
	if u.Provider != string(domain.ProviderFirebase) || !s.Admission.Configured() || u.Invited {
		return true
	}
	if s.OwnerEmail != "" && SameEmail(u.Email, s.OwnerEmail) {
		return true
	}
	return s.Admission.Allows(u.Email)
}

// ResolveSession devolve o usuário do token de sessão, renovando a validade deslizante
// (no máximo uma vez por minuto, nunca além do teto absoluto de auth.SessionAbsoluteTTL).
// Sessão desconhecida ou expirada = ErrUnauthenticated; no provider firebase, com listas de
// admissão, usuário que saiu delas também.
func (s *Service) ResolveSession(ctx context.Context, token string) (domain.User, error) {
	return s.resolveSession(ctx, token, true)
}

// CheckSession faz as mesmas verificações de ResolveSession (sessão existente e não expirada, usuário presente e
// ainda admitido) SEM efeito colateral: não renova a validade deslizante. É para revalidações automáticas (o ping
// do SSE): um stream aberto não pode manter a sessão viva sozinho; só o uso de verdade (requisições) renova.
func (s *Service) CheckSession(ctx context.Context, token string) (domain.User, error) {
	return s.resolveSession(ctx, token, false)
}

func (s *Service) resolveSession(ctx context.Context, token string, touch bool) (domain.User, error) {
	if token == "" {
		return domain.User{}, domain.ErrUnauthenticated
	}
	q := s.read()
	now := s.Now().UTC()
	sess, err := q.GetSessionByHash(ctx, store.GetSessionByHashParams{TokenHash: auth.HashToken(token), Now: now})
	if err != nil {
		if isNoRows(err) {
			return domain.User{}, domain.ErrUnauthenticated
		}
		return domain.User{}, fmt.Errorf("get session: %w", err)
	}
	u, err := q.GetUserByID(ctx, sess.UserID)
	if err != nil {
		if isNoRows(err) {
			return domain.User{}, domain.ErrUnauthenticated
		}
		return domain.User{}, fmt.Errorf("get session user: %w", err)
	}
	if !s.stillAdmitted(u) {
		_ = q.DeleteSessionByHash(ctx, auth.HashToken(token))
		return domain.User{}, domain.ErrUnauthenticated
	}
	if touch && now.Sub(sess.LastSeenAt) >= auth.TouchEvery {
		if err := q.TouchSession(ctx, store.TouchSessionParams{ID: sess.ID, Now: now, ExpiresAt: now.Add(auth.SessionTTL)}); err != nil {
			return domain.User{}, fmt.Errorf("touch session: %w", err)
		}
	}
	return userFromRow(u), nil
}

// EndSession encerra a sessão do token (idempotente).
func (s *Service) EndSession(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	if err := s.read().DeleteSessionByHash(ctx, auth.HashToken(token)); err != nil {
		return fmt.Errorf("end session: %w", err)
	}
	return nil
}

// CreateAPIKey cria uma API key para o usuário; a chave em claro só é devolvida aqui.
func (s *Service) CreateAPIKey(ctx context.Context, userID domain.ID, name string) (string, domain.APIKey, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", domain.APIKey{}, domain.Invalid("name", "required")
	}
	if len([]rune(name)) > 80 {
		return "", domain.APIKey{}, domain.Invalid("name", "máximo de 80 caracteres")
	}
	key, prefix, hash, err := auth.NewAPIKey()
	if err != nil {
		return "", domain.APIKey{}, err
	}
	row, err := s.read().CreateAPIKey(ctx, store.CreateAPIKeyParams{ID: domain.NewID(), UserID: userID, Name: name,
		Prefix: prefix, Hash: hash, Now: s.Now().UTC()})
	if err != nil {
		return "", domain.APIKey{}, mapErr(err, "create api key")
	}
	return key, apiKeyFromRow(row), nil
}

// ListAPIKeys lista as chaves do usuário (ativas e revogadas), da mais nova para a mais antiga.
func (s *Service) ListAPIKeys(ctx context.Context, userID domain.ID) ([]domain.APIKey, error) {
	rows, err := s.read().ListAPIKeys(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list api keys: %w", err)
	}
	out := make([]domain.APIKey, len(rows))
	for i, r := range rows {
		out[i] = apiKeyFromRow(r)
	}
	return out, nil
}

// RevokeAPIKey revoga a chave do usuário. Chave inexistente, de outro usuário ou já revogada = ErrNotFound.
func (s *Service) RevokeAPIKey(ctx context.Context, userID, keyID domain.ID) (domain.APIKey, error) {
	row, err := s.read().RevokeAPIKey(ctx, store.RevokeAPIKeyParams{ID: keyID, UserID: userID, Now: s.Now().UTC()})
	if err != nil {
		return domain.APIKey{}, mapErr(err, "api key")
	}
	return apiKeyFromRow(row), nil
}

// ResolveAPIKey devolve o dono de uma API key ativa e anota o último uso (no máximo 1×/min).
func (s *Service) ResolveAPIKey(ctx context.Context, key string) (domain.User, error) {
	return s.resolveAPIKey(ctx, key, true)
}

// CheckAPIKey é ResolveAPIKey sem efeito colateral: não anota o último uso (ver CheckSession).
func (s *Service) CheckAPIKey(ctx context.Context, key string) (domain.User, error) {
	return s.resolveAPIKey(ctx, key, false)
}

func (s *Service) resolveAPIKey(ctx context.Context, key string, touch bool) (domain.User, error) {
	if !auth.IsAPIKey(key) {
		return domain.User{}, domain.ErrUnauthenticated
	}
	q := s.read()
	k, err := q.GetAPIKeyByHash(ctx, auth.HashToken(key))
	if err != nil {
		if isNoRows(err) {
			return domain.User{}, domain.ErrUnauthenticated
		}
		return domain.User{}, fmt.Errorf("get api key: %w", err)
	}
	u, err := q.GetUserByID(ctx, k.UserID)
	if err != nil {
		if isNoRows(err) {
			return domain.User{}, domain.ErrUnauthenticated
		}
		return domain.User{}, fmt.Errorf("get api key user: %w", err)
	}
	if !s.stillAdmitted(u) {
		return domain.User{}, domain.ErrUnauthenticated
	}
	now := s.Now().UTC()
	if touch && (k.LastUsedAt == nil || now.Sub(*k.LastUsedAt) >= auth.TouchEvery) {
		if err := q.TouchAPIKey(ctx, store.TouchAPIKeyParams{ID: k.ID, Now: now}); err != nil && !errors.Is(err, context.Canceled) {
			return domain.User{}, fmt.Errorf("touch api key: %w", err)
		}
	}
	return userFromRow(u), nil
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
