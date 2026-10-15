package auth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// GoogleJWKSURL publica as chaves públicas que assinam os ID tokens do Firebase Auth.
const GoogleJWKSURL = "https://www.googleapis.com/service_accounts/v1/jwk/securetoken@system.gserviceaccount.com"

var (
	// ErrInvalidIDToken: o ID token não passou na validação (assinatura, iss, aud, validade…).
	ErrInvalidIDToken = errors.New("ID token do Firebase inválido")
	// ErrUnverifiedEmail: token válido, mas o e-mail não foi verificado.
	ErrUnverifiedEmail = errors.New("e-mail do Firebase não verificado")
	// ErrJWKSUnavailable: não foi possível obter as chaves públicas do Google.
	ErrJWKSUnavailable = errors.New("chaves públicas do Firebase indisponíveis")
)

// FirebaseClaims são as claims aceitas de um ID token já validado.
type FirebaseClaims struct {
	UID           string
	Email         string
	EmailVerified bool
	Name          string
	SignInMethod  string // "google.com", "password"…
	AuthTime      time.Time
}

// FirebaseVerifier valida ID tokens do Firebase Auth sem o Admin SDK: assinatura RS256
// com as chaves públicas do Google (cache conforme Cache-Control, refresh quando o kid
// é desconhecido), iss, aud, exp/iat/auth_time e sub.
type FirebaseVerifier struct {
	ProjectID string
	// JWKSURL é a origem das chaves (default GoogleJWKSURL; testes injetam um httptest).
	JWKSURL string
	HTTP    *http.Client
	Now     func() time.Time
	// MinRefresh é o intervalo mínimo entre buscas forçadas por kid desconhecido (default 10 s),
	// para que tokens forjados não virem uma torrente de requisições ao Google.
	MinRefresh time.Duration
	// StaleGrace é por quanto tempo, depois de vencido o cache, as chaves antigas ainda valem se o
	// refresh falhar (default 1 h). Passado esse prazo o verificador falha fechado (ErrJWKSUnavailable).
	StaleGrace time.Duration
	// Log recebe o aviso de uso de chaves vencidas (nil = descarta).
	Log *slog.Logger

	mu          sync.Mutex
	keys        map[string]*rsa.PublicKey
	expires     time.Time
	lastAttempt time.Time
}

// DefaultStaleGrace é a tolerância a um JWKS inalcançável depois do vencimento do cache.
const DefaultStaleGrace = time.Hour

func (v *FirebaseVerifier) now() time.Time {
	if v.Now != nil {
		return v.Now()
	}
	return time.Now()
}

type firebaseClaims struct {
	jwt.RegisteredClaims
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
	AuthTime      int64  `json:"auth_time"`
	Firebase      struct {
		SignInProvider string `json:"sign_in_provider"`
	} `json:"firebase"`
}

// Verify valida o ID token e devolve as claims. Erros de token envolvem ErrInvalidIDToken
// (ou ErrUnverifiedEmail); falha ao obter chaves envolve ErrJWKSUnavailable.
func (v *FirebaseVerifier) Verify(ctx context.Context, idToken string) (FirebaseClaims, error) {
	if v.ProjectID == "" {
		return FirebaseClaims{}, fmt.Errorf("%w: projeto não configurado", ErrInvalidIDToken)
	}
	var c firebaseClaims
	var keyErr error
	_, err := jwt.ParseWithClaims(idToken, &c, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			return nil, errors.New("sem kid")
		}
		k, err := v.key(ctx, kid)
		if err != nil {
			keyErr = err
			return nil, err
		}
		return k, nil
	},
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithIssuer("https://securetoken.google.com/"+v.ProjectID),
		jwt.WithAudience(v.ProjectID),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithTimeFunc(v.now),
		jwt.WithLeeway(time.Minute),
	)
	if err != nil {
		if keyErr != nil && errors.Is(keyErr, ErrJWKSUnavailable) {
			return FirebaseClaims{}, keyErr
		}
		return FirebaseClaims{}, fmt.Errorf("%w: %v", ErrInvalidIDToken, err)
	}
	now := v.now()
	if c.Subject == "" || len(c.Subject) > 128 {
		return FirebaseClaims{}, fmt.Errorf("%w: sub ausente ou inválido", ErrInvalidIDToken)
	}
	if c.IssuedAt == nil {
		return FirebaseClaims{}, fmt.Errorf("%w: iat ausente", ErrInvalidIDToken)
	}
	if c.AuthTime <= 0 || time.Unix(c.AuthTime, 0).After(now.Add(time.Minute)) {
		return FirebaseClaims{}, fmt.Errorf("%w: auth_time ausente ou no futuro", ErrInvalidIDToken)
	}
	email := strings.TrimSpace(c.Email)
	if email == "" {
		return FirebaseClaims{}, fmt.Errorf("%w: token sem e-mail", ErrInvalidIDToken)
	}
	out := FirebaseClaims{
		UID: c.Subject, Email: email, EmailVerified: c.EmailVerified, Name: strings.TrimSpace(c.Name),
		SignInMethod: c.Firebase.SignInProvider, AuthTime: time.Unix(c.AuthTime, 0),
	}
	if !out.EmailVerified {
		return out, ErrUnverifiedEmail
	}
	return out, nil
}

// key devolve a chave pública do kid, buscando o JWKS se o cache expirou ou o kid é novo.
//
// Se o refresh falha e o cache venceu há menos de StaleGrace, as chaves em cache continuam valendo
// (com aviso no log): uma queda curta do endpoint do Google não derruba o login. Depois disso, falha fechado.
func (v *FirebaseVerifier) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	now := v.now()
	min := v.MinRefresh
	if min == 0 {
		min = 10 * time.Second
	}
	throttled := min > 0 && now.Sub(v.lastAttempt) < min
	if v.keys != nil && now.Before(v.expires) {
		if k, ok := v.keys[kid]; ok {
			return k, nil
		}
		if throttled {
			return nil, fmt.Errorf("kid %q desconhecido", kid)
		}
	}
	grace := v.StaleGrace
	if grace <= 0 {
		grace = DefaultStaleGrace
	}
	stale := v.keys != nil && !now.Before(v.expires) && now.Before(v.expires.Add(grace))
	if stale && throttled { // o último refresh falhou há pouco: não martela o Google, usa o cache vencido
		if k, ok := v.keys[kid]; ok {
			return k, nil
		}
		return nil, fmt.Errorf("%w: refresh recente falhou e kid %q não está no cache vencido", ErrJWKSUnavailable, kid)
	}
	if err := v.fetchLocked(ctx); err != nil {
		if stale {
			v.logger().Warn("firebase: refresh do JWKS falhou; usando chaves em cache vencidas", "err", err,
				"venceu_em", v.expires, "tolerancia_ate", v.expires.Add(grace))
			if k, ok := v.keys[kid]; ok {
				return k, nil
			}
		}
		return nil, err
	}
	if k, ok := v.keys[kid]; ok {
		return k, nil
	}
	return nil, fmt.Errorf("kid %q desconhecido", kid)
}

type jwksDoc struct {
	Keys []struct {
		Kid string `json:"kid"`
		Kty string `json:"kty"`
		N   string `json:"n"`
		E   string `json:"e"`
	} `json:"keys"`
}

func (v *FirebaseVerifier) logger() *slog.Logger {
	if v.Log != nil {
		return v.Log
	}
	return slog.New(slog.DiscardHandler)
}

func (v *FirebaseVerifier) fetchLocked(ctx context.Context) error {
	v.lastAttempt = v.now()
	url := v.JWKSURL
	if url == "" {
		url = GoogleJWKSURL
	}
	cl := v.HTTP
	if cl == nil {
		cl = &http.Client{Timeout: 5 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrJWKSUnavailable, err)
	}
	resp, err := cl.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrJWKSUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: HTTP %d", ErrJWKSUnavailable, resp.StatusCode)
	}
	var doc jwksDoc
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&doc); err != nil {
		return fmt.Errorf("%w: JWKS ilegível: %v", ErrJWKSUnavailable, err)
	}
	keys := make(map[string]*rsa.PublicKey, len(doc.Keys))
	for _, k := range doc.Keys {
		if k.Kty != "RSA" || k.Kid == "" {
			continue
		}
		n, err1 := base64.RawURLEncoding.DecodeString(k.N)
		e, err2 := base64.RawURLEncoding.DecodeString(k.E)
		if err1 != nil || err2 != nil || len(n) < 256 {
			continue
		}
		exp := new(big.Int).SetBytes(e)
		if !exp.IsInt64() || exp.Int64() < 3 || exp.Int64() > 1<<31 {
			continue
		}
		keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(exp.Int64())}
	}
	if len(keys) == 0 {
		return fmt.Errorf("%w: JWKS sem chaves RSA válidas", ErrJWKSUnavailable)
	}
	now := v.now()
	v.keys, v.expires = keys, now.Add(maxAge(resp.Header.Get("Cache-Control")))
	return nil
}

// maxAge extrai max-age de Cache-Control, limitado a [1 min, 24 h]; ausente = 1 h.
func maxAge(cc string) time.Duration {
	d := time.Hour
	for _, part := range strings.Split(cc, ",") {
		part = strings.TrimSpace(strings.ToLower(part))
		if v, ok := strings.CutPrefix(part, "max-age="); ok {
			if n, err := strconv.Atoi(strings.Trim(v, `"`)); err == nil && n >= 0 {
				d = time.Duration(n) * time.Second
			}
		}
		if part == "no-store" || part == "no-cache" {
			d = time.Minute
		}
	}
	return min(max(d, time.Minute), 24*time.Hour)
}

// Admission é a política de admissão de usuários do provider firebase.
type Admission struct {
	Emails  []string // e-mails exatos (sem diferenciar caixa)
	Domains []string // domínios (ex.: "empresa.com")
}

// Configured informa se há alguma lista.
func (a Admission) Configured() bool { return len(a.Emails) > 0 || len(a.Domains) > 0 }

// Allows informa se o e-mail está em alguma lista.
func (a Admission) Allows(email string) bool {
	email = FoldEmail(email)
	at := strings.LastIndex(email, "@")
	if at <= 0 {
		return false
	}
	domain := email[at+1:]
	for _, e := range a.Emails {
		if FoldEmail(e) == email {
			return true
		}
	}
	for _, d := range a.Domains {
		if FoldEmail(strings.TrimPrefix(strings.TrimSpace(d), "@")) == domain {
			return true
		}
	}
	return false
}
