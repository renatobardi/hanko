package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

const (
	// SessionCookie é o nome do cookie de sessão.
	SessionCookie = "hanko_session"
	// SessionTTL é a validade deslizante da sessão.
	SessionTTL = 30 * 24 * time.Hour
	// SessionAbsoluteTTL é o teto de vida da sessão desde o login, por mais que ela seja renovada.
	SessionAbsoluteTTL = 90 * 24 * time.Hour
	// TouchEvery limita a renovação de last_seen_at (e de last_used_at da API key) a 1 por minuto.
	TouchEvery = time.Minute
	// APIKeyPrefix distingue API keys de usuário de run tokens (JWT).
	APIKeyPrefix = "hk_"
)

// randomToken devolve n bytes aleatórios em base64url sem padding.
func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("gerar token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// HashToken é o SHA-256 (hex) de um token de sessão ou API key; é o que vai ao banco.
func HashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

// NewSessionToken gera um token de sessão de 32 bytes e seu hash.
func NewSessionToken() (token, hash string, err error) {
	token, err = randomToken(32)
	if err != nil {
		return "", "", err
	}
	return token, HashToken(token), nil
}

// NewAPIKey gera uma API key "hk_…" (32 bytes de entropia), o prefixo exibível e o hash.
func NewAPIKey() (key, prefix, hash string, err error) {
	body, err := randomToken(32)
	if err != nil {
		return "", "", "", err
	}
	key = APIKeyPrefix + body
	return key, key[:len(APIKeyPrefix)+6], HashToken(key), nil
}

// IsAPIKey informa se o token tem a forma de uma API key de usuário.
func IsAPIKey(tok string) bool { return strings.HasPrefix(tok, APIKeyPrefix) }

// BearerToken extrai o token de "Authorization: Bearer <token>".
func BearerToken(header string) (string, bool) {
	scheme, tok, ok := strings.Cut(strings.TrimSpace(header), " ")
	if !ok || !strings.EqualFold(scheme, "bearer") {
		return "", false
	}
	tok = strings.TrimSpace(tok)
	return tok, tok != ""
}

// InviteTTL é a validade de um convite de empresa.
const InviteTTL = 7 * 24 * time.Hour

// ClaimTTL é a validade do link de claim do primeiro dono (ADR-0058 §A).
const ClaimTTL = time.Hour

// ClaimPath é o prefixo público do link de claim (/claim/{token}); o token é o segredo do caminho.
const ClaimPath = "/claim/"

// NewClaimToken gera o token do claim do primeiro dono (32 bytes de entropia) e seu hash. Só o hash vai ao banco.
func NewClaimToken() (token, hash string, err error) {
	token, err = randomToken(32)
	if err != nil {
		return "", "", err
	}
	return token, HashToken(token), nil
}

// NewInviteToken gera o token de um convite (32 bytes de entropia) e seu hash. Só o hash vai ao banco.
func NewInviteToken() (token, hash string, err error) {
	token, err = randomToken(32)
	if err != nil {
		return "", "", err
	}
	return token, HashToken(token), nil
}

// CLIAuthCodeBytes é a entropia do código de um pedido de login da CLI (72 bits; 12 caracteres na URL).
const CLIAuthCodeBytes = 9

// NewCLIAuthCode gera o código de /cli-auth/<código> e seu hash. Só o hash vai ao banco.
func NewCLIAuthCode() (code, hash string, err error) {
	code, err = randomToken(CLIAuthCodeBytes)
	if err != nil {
		return "", "", err
	}
	return code, HashToken(code), nil
}
