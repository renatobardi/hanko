package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/auth/passkey"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/settings"
	"github.com/renatobardi/hanko/internal/store"
)

// Reautenticação recente e passkey (ADR-0070). Ação sensível pede RequireReauth; a prova sai por passkey (qualquer
// provider), senha (provider local) ou token do Firebase com auth_time recente (provider firebase). Só quem tem
// sessão prova; API key full faz a ação sem prova, e o fato leva o id da chave.

const (
	defaultReauthMinutes = 5
	passkeyLimit         = 10
	passkeyChallengeTTL  = 2 * time.Minute
	// reauthLimiterIP é a chave fixa do limitador de provas: o limite é por usuário, não por IP.
	reauthLimiterIP = "reauth"
	// viaTrusted é o modo trusted (ADR-0017): sem identidade, sem reautenticação.
	viaTrusted = "trusted"
)

// IDTokenVerifier valida um ID token do Firebase (auth.FirebaseVerifier implementa).
type IDTokenVerifier interface {
	Verify(ctx context.Context, idToken string) (auth.FirebaseClaims, error)
}

// ReauthWindow é quanto tempo uma prova vale (reauth.minutes, padrão 5).
func (s *Service) ReauthWindow() time.Duration {
	m := int64(0)
	if s.Settings != nil {
		m = s.Settings.Int(settings.ReauthMinutes)
	}
	if m <= 0 {
		m = defaultReauthMinutes
	}
	return time.Duration(m) * time.Minute
}

// ReauthGrant diz por que uma ação sensível foi liberada. Vai para o fato (ADR-0070 §6).
type ReauthGrant struct {
	Via    string     // session, api_key ou trusted
	Method string     // com session: passkey, password ou firebase
	KeyID  *domain.ID // com api_key: a chave que fez a ação
}

// RequireReauth confere se a identidade pode fazer uma ação sensível agora (ADR-0070 §1, §2, §5). Sessão sem prova
// recente recebe *domain.ReauthError (401 reauth_required); API key de escopo read, 403; API key full e modo trusted
// passam; persona e canal não fazem ação sensível.
func (s *Service) RequireReauth(id auth.Identity) (ReauthGrant, error) {
	switch id.Via {
	case "":
		return ReauthGrant{Via: viaTrusted}, nil
	case auth.ViaAPIKey:
		if id.KeyScope != domain.KeyScopeFull {
			return ReauthGrant{}, fmt.Errorf("%w: ação sensível exige API key de escopo full", domain.ErrNotAllowed)
		}
		return ReauthGrant{Via: string(auth.ViaAPIKey), KeyID: id.KeyID}, nil
	case auth.ViaSession:
		window := s.ReauthWindow()
		if at := id.Session.ReauthAt; at != nil && s.Now().UTC().Sub(*at) <= window {
			return ReauthGrant{Via: string(auth.ViaSession), Method: id.Session.ReauthMethod}, nil
		}
		return ReauthGrant{}, &domain.ReauthError{WindowMinutes: int(window / time.Minute)}
	}
	return ReauthGrant{}, fmt.Errorf("%w: ação sensível só por sessão ou API key full", domain.ErrNotAllowed)
}

// ReauthProof é a prova aceita.
type ReauthProof struct {
	Method     string
	At         time.Time
	ValidUntil time.Time
}

// ReauthOptions são as provas que o usuário pode usar agora. Passkey vem com o desafio, quando há passkey cadastrada.
type ReauthOptions struct {
	Methods []string
	Window  time.Duration
	Passkey *PasskeyChallenge
}

// PasskeyChallenge é uma cerimônia pendente: o id do desafio (uso único, 2 min) e as opções que o navegador recebe.
type PasskeyChallenge struct {
	ID      domain.ID
	Options json.RawMessage
}

// ReauthOptionsFor lista as provas da sessão. Só sessão prova (API key e canal não reautenticam).
func (s *Service) ReauthOptionsFor(ctx context.Context, id auth.Identity) (ReauthOptions, error) {
	if id.Via != auth.ViaSession {
		return ReauthOptions{}, fmt.Errorf("%w: reautenticação é só por sessão", domain.ErrNotAllowed)
	}
	u, err := s.read().GetUserByID(ctx, id.User.ID)
	if err != nil {
		return ReauthOptions{}, fmt.Errorf("reauth options: %w", err)
	}
	dom := userFromRow(u)
	out := ReauthOptions{Window: s.ReauthWindow()}
	rows, err := s.read().ListUserPasskeys(ctx, dom.ID)
	if err != nil {
		return ReauthOptions{}, fmt.Errorf("list passkeys: %w", err)
	}
	if len(rows) > 0 {
		out.Methods = append(out.Methods, "passkey")
		ch, err := s.BeginPasskeyReauth(ctx, id)
		if err != nil {
			return ReauthOptions{}, err
		}
		out.Passkey = &ch
	}
	switch dom.Provider {
	case domain.ProviderLocal:
		out.Methods = append(out.Methods, "password")
	case domain.ProviderFirebase:
		out.Methods = append(out.Methods, "firebase")
	}
	return out, nil
}

// ReauthWithPassword prova a identidade com a senha (provider local, o mesmo argon2id do login).
func (s *Service) ReauthWithPassword(ctx context.Context, id auth.Identity, password string) (ReauthProof, error) {
	if id.Via != auth.ViaSession {
		return ReauthProof{}, fmt.Errorf("%w: reautenticação é só por sessão", domain.ErrNotAllowed)
	}
	uid := id.User.ID
	if err := s.reauthAllowed(uid); err != nil {
		return ReauthProof{}, err
	}
	u, err := s.read().GetUserByID(ctx, uid)
	if err != nil {
		return ReauthProof{}, fmt.Errorf("reauth password: %w", err)
	}
	dom := userFromRow(u)
	if dom.Provider != domain.ProviderLocal || dom.PasswordHash == "" || len(password) > auth.MaxPasswordLen*4 {
		s.reauthFailed(uid)
		return ReauthProof{}, domain.ErrUnauthenticated
	}
	release, err := s.PasswordGate.Acquire(ctx)
	if err != nil {
		return ReauthProof{}, err
	}
	ok, verr := auth.VerifyPassword(password, dom.PasswordHash)
	release()
	if verr != nil || !ok {
		s.reauthFailed(uid)
		return ReauthProof{}, domain.ErrUnauthenticated
	}
	return s.markReauth(ctx, id, dom, "password", nil)
}

// ReauthWithFirebase prova a identidade com um ID token novo do Firebase: o uid tem que ser o da conta e o auth_time
// tem que estar dentro da janela.
func (s *Service) ReauthWithFirebase(ctx context.Context, id auth.Identity, idToken string) (ReauthProof, error) {
	if id.Via != auth.ViaSession {
		return ReauthProof{}, fmt.Errorf("%w: reautenticação é só por sessão", domain.ErrNotAllowed)
	}
	uid := id.User.ID
	if s.IDVerifier == nil {
		return ReauthProof{}, fmt.Errorf("%w: provider firebase não configurado", domain.ErrNotAllowed)
	}
	if err := s.reauthAllowed(uid); err != nil {
		return ReauthProof{}, err
	}
	claims, err := s.IDVerifier.Verify(ctx, idToken)
	if err != nil {
		s.reauthFailed(uid)
		return ReauthProof{}, domain.ErrUnauthenticated
	}
	u, err := s.read().GetUserByID(ctx, uid)
	if err != nil {
		return ReauthProof{}, fmt.Errorf("reauth firebase: %w", err)
	}
	dom := userFromRow(u)
	if dom.Provider != domain.ProviderFirebase || dom.ExternalID == "" || dom.ExternalID != claims.UID {
		s.reauthFailed(uid)
		return ReauthProof{}, domain.ErrUnauthenticated
	}
	// Token com login antigo: não é senha errada, então não conta no limite; pede prova de novo.
	window := s.ReauthWindow()
	if claims.AuthTime.IsZero() || s.Now().UTC().Sub(claims.AuthTime) > window {
		return ReauthProof{}, &domain.ReauthError{WindowMinutes: int(window / time.Minute)}
	}
	return s.markReauth(ctx, id, dom, "firebase", nil)
}

// BeginPasskeyReauth cria o desafio de prova por passkey (ADR-0070 §3). Exige passkey cadastrada.
func (s *Service) BeginPasskeyReauth(ctx context.Context, id auth.Identity) (PasskeyChallenge, error) {
	if s.Passkeys == nil {
		return PasskeyChallenge{}, errPasskeyOff
	}
	if id.Via != auth.ViaSession {
		return PasskeyChallenge{}, fmt.Errorf("%w: reautenticação é só por sessão", domain.ErrNotAllowed)
	}
	u, err := s.read().GetUserByID(ctx, id.User.ID)
	if err != nil {
		return PasskeyChallenge{}, fmt.Errorf("begin passkey reauth: %w", err)
	}
	dom := userFromRow(u)
	creds, _, err := s.passkeyCreds(ctx, dom.ID)
	if err != nil {
		return PasskeyChallenge{}, err
	}
	if len(creds) == 0 {
		return PasskeyChallenge{}, fmt.Errorf("%w: nenhuma passkey cadastrada", domain.ErrNotFound)
	}
	challenge, options, err := s.Passkeys.LoginBegin(passkeyUser(dom), creds)
	if err != nil {
		return PasskeyChallenge{}, err
	}
	cid, err := s.saveChallenge(ctx, id, dom.ID, "reauth", challenge)
	if err != nil {
		return PasskeyChallenge{}, err
	}
	return PasskeyChallenge{ID: cid, Options: options}, nil
}

// FinishPasskeyReauth verifica a prova por passkey (desafio, origem, assinatura, contador e UV) e grava a reautenticação.
func (s *Service) FinishPasskeyReauth(ctx context.Context, id auth.Identity, challengeID domain.ID, response json.RawMessage) (ReauthProof, error) {
	if s.Passkeys == nil {
		return ReauthProof{}, errPasskeyOff
	}
	if id.Via != auth.ViaSession {
		return ReauthProof{}, fmt.Errorf("%w: reautenticação é só por sessão", domain.ErrNotAllowed)
	}
	uid := id.User.ID
	if err := s.reauthAllowed(uid); err != nil {
		return ReauthProof{}, err
	}
	ch, err := s.consumeChallenge(ctx, id, challengeID, "reauth")
	if err != nil {
		s.reauthFailed(uid)
		return ReauthProof{}, err
	}
	u, err := s.read().GetUserByID(ctx, uid)
	if err != nil {
		return ReauthProof{}, fmt.Errorf("finish passkey reauth: %w", err)
	}
	dom := userFromRow(u)
	creds, rows, err := s.passkeyCreds(ctx, dom.ID)
	if err != nil {
		return ReauthProof{}, err
	}
	used, err := s.Passkeys.LoginFinish(passkeyUser(dom), ch.Challenge, creds, response)
	if err != nil {
		s.reauthFailed(uid)
		return ReauthProof{}, domain.ErrUnauthenticated
	}
	var stored *store.UserPasskey
	for i := range rows {
		if string(rows[i].CredentialID) == string(used.CredentialID) {
			stored = &rows[i]
		}
	}
	if stored == nil {
		s.reauthFailed(uid)
		return ReauthProof{}, domain.ErrUnauthenticated
	}
	touch := func(t *txc) error {
		return t.q.TouchPasskey(t.ctx, store.TouchPasskeyParams{ID: stored.ID, UserID: dom.ID, SignCount: int64(used.SignCount),
			BackupState: used.BackupState, Now: &t.now})
	}
	return s.markReauth(ctx, id, dom, "passkey", touch)
}

// BeginPasskeyRegistration cria o desafio de cadastro (ADR-0070 §4). Exige reautenticação recente.
func (s *Service) BeginPasskeyRegistration(ctx context.Context, id auth.Identity) (PasskeyChallenge, error) {
	if s.Passkeys == nil {
		return PasskeyChallenge{}, errPasskeyOff
	}
	if _, err := s.RequireReauth(id); err != nil {
		return PasskeyChallenge{}, err
	}
	creds, rows, err := s.passkeyCreds(ctx, id.User.ID)
	if err != nil {
		return PasskeyChallenge{}, err
	}
	if len(rows) >= passkeyLimit {
		return PasskeyChallenge{}, fmt.Errorf("%w: limite de %d passkeys", domain.ErrConflict, passkeyLimit)
	}
	challenge, options, err := s.Passkeys.RegistrationBegin(passkeyUser(id.User), creds)
	if err != nil {
		return PasskeyChallenge{}, err
	}
	cid, err := s.saveChallenge(ctx, id, id.User.ID, "register", challenge)
	if err != nil {
		return PasskeyChallenge{}, err
	}
	return PasskeyChallenge{ID: cid, Options: options}, nil
}

// FinishPasskeyRegistration verifica a resposta de cadastro e grava a passkey. Exige reautenticação recente.
func (s *Service) FinishPasskeyRegistration(ctx context.Context, id auth.Identity, challengeID domain.ID, name string, response json.RawMessage) (domain.Passkey, error) {
	if s.Passkeys == nil {
		return domain.Passkey{}, errPasskeyOff
	}
	grant, err := s.RequireReauth(id)
	if err != nil {
		return domain.Passkey{}, err
	}
	ch, err := s.consumeChallenge(ctx, id, challengeID, "register")
	if err != nil {
		return domain.Passkey{}, err
	}
	creds, rows, err := s.passkeyCreds(ctx, id.User.ID)
	if err != nil {
		return domain.Passkey{}, err
	}
	if len(rows) >= passkeyLimit {
		return domain.Passkey{}, fmt.Errorf("%w: limite de %d passkeys", domain.ErrConflict, passkeyLimit)
	}
	cred, err := s.Passkeys.RegistrationFinish(passkeyUser(id.User), ch.Challenge, creds, response)
	if err != nil {
		return domain.Passkey{}, domain.Invalid("credential", "passkey recusada: a verificação do autenticador falhou")
	}
	pid := domain.NewID()
	var out store.UserPasskey
	err = s.withTx(ctx, func(t *txc) error {
		row, err := t.q.CreateUserPasskey(ctx, store.CreateUserPasskeyParams{ID: pid, UserID: id.User.ID, CredentialID: cred.CredentialID,
			PublicKey: cred.PublicKey, SignCount: int64(cred.SignCount), Aaguid: aaguidUUID(cred.AAGUID), Transports: cred.Transports,
			BackupEligible: cred.BackupEligible, BackupState: cred.BackupState, Name: name, Now: t.now})
		if err != nil {
			return mapErr(err, "create passkey")
		}
		out = row
		return t.authFact(id.User.Actor(), id.User.ID, "auth.passkey_added", "passkey", pid, grantData(grant))
	})
	if err != nil {
		return domain.Passkey{}, err
	}
	return passkeyFromRow(out), nil
}

// ListPasskeys devolve as passkeys do usuário (sem a chave pública).
func (s *Service) ListPasskeys(ctx context.Context, userID domain.ID) ([]domain.Passkey, error) {
	rows, err := s.read().ListUserPasskeys(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list passkeys: %w", err)
	}
	out := make([]domain.Passkey, len(rows))
	for i, r := range rows {
		out[i] = passkeyFromRow(r)
	}
	return out, nil
}

// DeletePasskey remove uma passkey do usuário. Exige reautenticação recente.
func (s *Service) DeletePasskey(ctx context.Context, id auth.Identity, passkeyID domain.ID) error {
	grant, err := s.RequireReauth(id)
	if err != nil {
		return err
	}
	return s.withTx(ctx, func(t *txc) error {
		n, err := t.q.DeleteUserPasskey(ctx, store.DeleteUserPasskeyParams{ID: passkeyID, UserID: id.User.ID})
		if err != nil {
			return fmt.Errorf("delete passkey: %w", err)
		}
		if n == 0 {
			return domain.ErrNotFound
		}
		return t.authFact(id.User.Actor(), id.User.ID, "auth.passkey_removed", "passkey", passkeyID, grantData(grant))
	})
}

// markReauth grava a prova na sessão e o fato auth.reauthenticated (ADR-0070 §6), na mesma transação. extra roda junto
// (o uso da passkey, por exemplo). Prova aceita zera o limitador.
func (s *Service) markReauth(ctx context.Context, id auth.Identity, dom domain.User, method string, extra func(*txc) error) (ReauthProof, error) {
	var proof ReauthProof
	err := s.withTx(ctx, func(t *txc) error {
		m := method
		if err := t.q.MarkSessionReauth(t.ctx, store.MarkSessionReauthParams{Now: &t.now, ReauthMethod: &m, ID: id.Session.ID, UserID: dom.ID}); err != nil {
			return fmt.Errorf("mark reauth: %w", err)
		}
		if extra != nil {
			if err := extra(t); err != nil {
				return err
			}
		}
		proof = ReauthProof{Method: method, At: t.now, ValidUntil: t.now.Add(s.ReauthWindow())}
		return t.authFact(dom.Actor(), dom.ID, "auth.reauthenticated", "user", dom.ID, map[string]any{"method": method, "via": "session"})
	})
	if err != nil {
		return ReauthProof{}, err
	}
	s.reauthOK(dom.ID)
	return proof, nil
}

// consumeChallenge gasta o desafio (uso único, mesmo usuário, propósito e sessão, dentro do prazo). Gasta mesmo que a
// verificação depois falhe: um desafio não serve a duas tentativas. Roda na própria transação, já confirmada.
func (s *Service) consumeChallenge(ctx context.Context, id auth.Identity, challengeID domain.ID, purpose string) (store.WebauthnChallenge, error) {
	var ch store.WebauthnChallenge
	err := s.withTx(ctx, func(t *txc) error {
		row, err := t.q.ConsumeWebauthnChallenge(t.ctx, store.ConsumeWebauthnChallengeParams{ID: challengeID, UserID: id.User.ID, Purpose: purpose, Now: t.now})
		if err != nil {
			if isNoRows(err) {
				return domain.Invalid("challenge_id", "desafio inválido, usado ou vencido")
			}
			return fmt.Errorf("consume challenge: %w", err)
		}
		if !sameChallengeOwner(row.SessionID, id) {
			return domain.Invalid("challenge_id", "desafio de outra sessão")
		}
		ch = row
		return nil
	})
	return ch, err
}

// saveChallenge guarda um desafio novo, com prazo de 2 min. Desafio de sessão fica preso a ela; o de API key não tem
// sessão (session_id fica nulo) e só vale para quem o pediu.
func (s *Service) saveChallenge(ctx context.Context, id auth.Identity, uid domain.ID, purpose string, challenge []byte) (domain.ID, error) {
	cid := domain.NewID()
	err := s.withTx(ctx, func(t *txc) error {
		_, err := t.q.CreateWebauthnChallenge(t.ctx, store.CreateWebauthnChallengeParams{ID: cid, UserID: uid, SessionID: challengeSession(id),
			Purpose: purpose, Challenge: challenge, ExpiresAt: t.now.Add(passkeyChallengeTTL), Now: t.now})
		return mapErr(err, "create challenge")
	})
	return cid, err
}

// challengeSession é a sessão a que o desafio fica preso; nil quando a identidade não é de sessão.
func challengeSession(id auth.Identity) *uuid.UUID {
	if id.Via != auth.ViaSession {
		return nil
	}
	sid := id.Session.ID
	return &sid
}

// sameChallengeOwner confere que o desafio é da mesma sessão (ou, sem sessão, do mesmo tipo de identidade).
func sameChallengeOwner(sid *uuid.UUID, id auth.Identity) bool {
	if id.Via != auth.ViaSession {
		return sid == nil
	}
	return sid != nil && *sid == id.Session.ID
}

// passkeyCreds lê as passkeys do usuário no formato do pacote passkey, junto com as linhas do banco.
func (s *Service) passkeyCreds(ctx context.Context, uid domain.ID) ([]passkey.Credential, []store.UserPasskey, error) {
	rows, err := s.read().ListUserPasskeys(ctx, uid)
	if err != nil {
		return nil, nil, fmt.Errorf("list passkeys: %w", err)
	}
	creds := make([]passkey.Credential, len(rows))
	for i, r := range rows {
		if r.SignCount < 0 || r.SignCount > math.MaxUint32 {
			return nil, nil, fmt.Errorf("passkey: contador de assinatura fora da faixa: %d", r.SignCount)
		}
		creds[i] = passkey.Credential{CredentialID: r.CredentialID, PublicKey: r.PublicKey, SignCount: uint32(r.SignCount),
			AAGUID: uuidBytes(r.Aaguid), Transports: r.Transports, BackupEligible: r.BackupEligible, BackupState: r.BackupState}
	}
	return creds, rows, nil
}

// reauthAllowed recusa a prova quando o usuário já errou demais (o mesmo limitador do login, por usuário).
func (s *Service) reauthAllowed(uid domain.ID) error {
	if s.ReauthLimiter == nil {
		return nil
	}
	if ok, _ := s.ReauthLimiter.Allow(reauthLimiterIP, uid.String()); !ok {
		return domain.ErrTooManyAttempts
	}
	return nil
}

func (s *Service) reauthFailed(uid domain.ID) {
	if s.ReauthLimiter != nil {
		s.ReauthLimiter.Fail(reauthLimiterIP, uid.String())
	}
}

func (s *Service) reauthOK(uid domain.ID) {
	if s.ReauthLimiter != nil {
		s.ReauthLimiter.Success(reauthLimiterIP, uid.String())
	}
}

// authFact grava um fato de conta em cada empresa ativa em que o usuário é membro: activity_events tem company_id
// obrigatório, e o banco não guarda fato sem empresa. Usuário sem empresa não gera fato. Empresa arquivada é só leitura
// (ADR-0057) e fica de fora, para a reautenticação não falhar por ela.
func (t *txc) authFact(actor domain.Actor, uid domain.ID, verb, subjectType string, subjectID domain.ID, data any) error {
	companies, err := t.q.ListCompaniesForUser(t.ctx, uid)
	if err != nil {
		return fmt.Errorf("list companies for fact: %w", err)
	}
	for _, c := range companies {
		archived, err := t.q.IsCompanyArchived(t.ctx, c.ID)
		if err != nil {
			return fmt.Errorf("check company archived: %w", err)
		}
		if archived {
			continue
		}
		if err := t.activity(actor, c.ID, verb, subjectType, subjectID, data, ""); err != nil {
			return err
		}
	}
	return nil
}

// grantData é o trecho do fato que diz como a ação foi liberada: a prova ou a chave de API (ADR-0070 §5).
func grantData(g ReauthGrant) map[string]any {
	d := map[string]any{"via": g.Via}
	if g.Method != "" {
		d["method"] = g.Method
	}
	if g.KeyID != nil {
		d["key_id"] = g.KeyID.String()
	}
	return d
}

func passkeyUser(u domain.User) passkey.User {
	return passkey.User{ID: u.ID, Name: u.Email}
}

func passkeyFromRow(r store.UserPasskey) domain.Passkey {
	p := domain.Passkey{ID: r.ID, UserID: r.UserID, Name: r.Name, Transports: r.Transports, CreatedAt: r.CreatedAt, LastUsedAt: r.LastUsedAt}
	if r.Aaguid != nil {
		a := *r.Aaguid
		p.AAGUID = &a
	}
	return p
}

// aaguidUUID converte o AAGUID (16 bytes) para uuid; sem AAGUID (ou tamanho errado) devolve nil.
func aaguidUUID(b []byte) *uuid.UUID {
	if len(b) != 16 {
		return nil
	}
	u, err := uuid.FromBytes(b)
	if err != nil {
		return nil
	}
	return &u
}

func uuidBytes(u *uuid.UUID) []byte {
	if u == nil {
		return nil
	}
	return u[:]
}

var errPasskeyOff = errors.New("passkey desligada nesta instalação (HANKO_PUBLIC_URL inválida)")
