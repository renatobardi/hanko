// Package auth holds credentials logic. v0.1: run tokens (short-lived HMAC JWTs that
// identify the persona/run calling back into Hanko via MCP/API). ADR-0007.
package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/renatobardi/hanko/internal/domain"
)

// RunClaims identify a run calling back into Hanko.
type RunClaims struct {
	CompanyID string `json:"cid"`
	PersonaID string `json:"pid"`
	RunID     string `json:"rid"`
	TaskID    string `json:"tid"`
	// ConversationID is set instead of TaskID on the token of a chat run (ADR-0027).
	ConversationID string `json:"cvid,omitempty"`
	jwt.RegisteredClaims
}

// RunTokens signs and verifies run tokens with a shared secret (HANKO_SECRET).
type RunTokens struct {
	Secret []byte
	TTL    time.Duration
	Now    func() time.Time
}

var ErrBadToken = errors.New("invalid run token")

func (t RunTokens) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

// Issue creates a token for a run.
func (t RunTokens) Issue(companyID, personaID, runID, taskID domain.ID) (string, error) {
	return t.issue(RunClaims{CompanyID: companyID.String(), PersonaID: personaID.String(), RunID: runID.String(), TaskID: taskID.String()})
}

// IssueForConversation creates the token of a chat run: it has a conversation and no task.
func (t RunTokens) IssueForConversation(companyID, personaID, runID, conversationID domain.ID) (string, error) {
	return t.issue(RunClaims{CompanyID: companyID.String(), PersonaID: personaID.String(), RunID: runID.String(), ConversationID: conversationID.String()})
}

func (t RunTokens) issue(c RunClaims) (string, error) {
	if len(t.Secret) < 16 {
		return "", errors.New("run token secret too short")
	}
	ttl := t.TTL
	if ttl <= 0 {
		ttl = 6 * time.Hour
	}
	now := t.now()
	c.RegisteredClaims = jwt.RegisteredClaims{
		Issuer: "hanko", Subject: "run:" + c.RunID,
		IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(t.Secret)
}

// Verify parses and validates a token.
func (t RunTokens) Verify(tok string) (RunClaims, error) {
	var c RunClaims
	_, err := jwt.ParseWithClaims(tok, &c, func(*jwt.Token) (any, error) { return t.Secret, nil },
		jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer("hanko"), jwt.WithTimeFunc(t.now))
	if err != nil {
		return RunClaims{}, fmt.Errorf("%w: %v", ErrBadToken, err)
	}
	return c, nil
}

// Actor returns the persona actor encoded in the claims.
func (c RunClaims) Actor() (domain.Actor, error) {
	id, err := domain.ParseID(c.PersonaID)
	if err != nil {
		return domain.Actor{}, ErrBadToken
	}
	return domain.PersonaActor(id), nil
}

// RunStatusFunc consulta o status atual de um run (normalmente service.GetRun).
type RunStatusFunc func(ctx context.Context, companyID, runID domain.ID) (domain.RunStatus, error)

// ErrRunInactive indica um token assinado cujo run não está mais queued/running.
var ErrRunInactive = fmt.Errorf("%w: run não está ativo", ErrBadToken)

// VerifyActive valida a assinatura e confirma que o run do token está ativo
// (queued ou running). Run inexistente ou encerrado devolve erro que casa com
// ErrBadToken; falha na consulta (banco) é devolvida sem esse sentinela.
func (t RunTokens) VerifyActive(ctx context.Context, tok string, status RunStatusFunc) (RunClaims, error) {
	c, err := t.Verify(tok)
	if err != nil {
		return RunClaims{}, err
	}
	if status == nil {
		return RunClaims{}, fmt.Errorf("%w: sem consulta de run", ErrBadToken)
	}
	cid, err := domain.ParseID(c.CompanyID)
	if err != nil {
		return RunClaims{}, ErrBadToken
	}
	rid, err := domain.ParseID(c.RunID)
	if err != nil {
		return RunClaims{}, ErrBadToken
	}
	st, err := status(ctx, cid, rid)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return RunClaims{}, ErrRunInactive
		}
		return RunClaims{}, fmt.Errorf("consultar run do token: %w", err)
	}
	if !st.Active() {
		return RunClaims{}, ErrRunInactive
	}
	return c, nil
}
