// Package auth holds credentials logic. v0.1: run tokens (short-lived HMAC JWTs that
// identify the persona/run calling back into Hanko via MCP/API). ADR-0007.
package auth

import (
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
	if len(t.Secret) < 16 {
		return "", errors.New("run token secret too short")
	}
	ttl := t.TTL
	if ttl <= 0 {
		ttl = 6 * time.Hour
	}
	now := t.now()
	c := RunClaims{
		CompanyID: companyID.String(), PersonaID: personaID.String(), RunID: runID.String(), TaskID: taskID.String(),
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: "hanko", Subject: "run:" + runID.String(),
			IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
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
