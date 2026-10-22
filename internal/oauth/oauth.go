// Package oauth implementa o cliente OAuth 2.1 com PKCE que o Hanko usa para autorizar servidores MCP remotos
// (ADR-0043): descoberta (metadados do recurso protegido e do servidor de autorização), registro dinâmico de
// cliente, URL de autorização com PKCE S256, troca do código e renovação do token. Só stdlib; nenhuma chamada sai
// sem o http.Client injetado (em produção, o cliente com proteção SSRF de netguard).
package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// maxBody limita qualquer resposta lida (metadados e tokens são pequenos).
const maxBody = 1 << 20

// Erros reconhecíveis.
var (
	// ErrInvalidGrant: o refresh token (ou o código) não vale mais; só uma nova autorização resolve.
	ErrInvalidGrant = errors.New("oauth: invalid_grant")
	// ErrNoPKCE: o servidor de autorização não oferece PKCE S256, que o OAuth 2.1 exige.
	ErrNoPKCE = errors.New("oauth: o servidor de autorização não suporta PKCE S256")
	// ErrNoRegistration: o servidor não tem registro dinâmico e não há client_id configurado.
	ErrNoRegistration = errors.New("oauth: o servidor não oferece registro dinâmico de cliente")
)

// Error é um erro devolvido pelo servidor de autorização (RFC 6749 §5.2). Nunca carrega token.
type Error struct {
	Code        string
	Description string
	Status      int
}

func (e *Error) Error() string {
	if e.Description != "" {
		return fmt.Sprintf("oauth: %s (%s)", e.Code, e.Description)
	}
	return "oauth: " + e.Code
}

// Is faz invalid_grant casar com ErrInvalidGrant.
func (e *Error) Is(target error) bool { return target == ErrInvalidGrant && e.Code == "invalid_grant" }

// Client fala com os servidores. HTTP é obrigatório; Validate, se definido, confere cada URL antes de usá-la
// (esquema, SSRF) e devolve o motivo da recusa.
type Client struct {
	HTTP     *http.Client
	Validate func(rawURL string) error
	Now      func() time.Time
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Client) check(raw string) error {
	if c.Validate == nil {
		return nil
	}
	if err := c.Validate(raw); err != nil {
		return &RefusedError{URL: redactURL(raw), Err: err}
	}
	return nil
}

// RefusedError: o Validate do cliente recusou o endereço (SSRF, esquema); a descoberta para em vez de seguir adiante.
type RefusedError struct {
	URL string
	Err error
}

func (e *RefusedError) Error() string {
	return fmt.Sprintf("oauth: endereço recusado (%s): %v", e.URL, e.Err)
}
func (e *RefusedError) Unwrap() error { return e.Err }

// redactURL mantém só esquema e host, para mensagens de erro.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "?"
	}
	return u.Scheme + "://" + u.Host
}

// ---------- PKCE e state ----------

// NewPKCE gera um code_verifier (43 caracteres, 32 bytes aleatórios) e o code_challenge S256 correspondente.
func NewPKCE() (verifier, challenge string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", fmt.Errorf("oauth: pkce: %w", err)
	}
	verifier = base64.RawURLEncoding.EncodeToString(b)
	return verifier, ChallengeS256(verifier), nil
}

// ChallengeS256 é BASE64URL(SHA256(verifier)) sem padding (RFC 7636 §4.2).
func ChallengeS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// NewState gera o parâmetro state (anti-CSRF): 32 bytes aleatórios.
func NewState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("oauth: state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// ---------- descoberta ----------

// Metadata é o que o Hanko precisa saber do servidor de autorização (RFC 8414).
type Metadata struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	RegistrationEndpoint  string   `json:"registration_endpoint,omitempty"`
	ScopesSupported       []string `json:"scopes_supported,omitempty"`
	CodeChallengeMethods  []string `json:"code_challenge_methods_supported,omitempty"`
	TokenAuthMethods      []string `json:"token_endpoint_auth_methods_supported,omitempty"`
}

// Discovery é o resultado da descoberta para um servidor MCP.
type Discovery struct {
	// Resource é o identificador do recurso protegido (RFC 8707), enviado como parâmetro resource.
	Resource string   `json:"resource"`
	Metadata Metadata `json:"metadata"`
	// Scopes sugeridos pelo recurso protegido.
	Scopes []string `json:"scopes,omitempty"`
}

type protectedResource struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
	ScopesSupported      []string `json:"scopes_supported"`
}

func (c *Client) getJSON(ctx context.Context, raw string, out any) (int, error) {
	if err := c.check(raw); err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return 0, fmt.Errorf("oauth: requisição: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, fmt.Errorf("oauth: %s: %w", redactURL(raw), err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return resp.StatusCode, fmt.Errorf("oauth: ler resposta: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return resp.StatusCode, fmt.Errorf("oauth: resposta inválida de %s: %w", redactURL(raw), err)
	}
	return resp.StatusCode, nil
}

// wellKnown monta /.well-known/<suffix> com o caminho da URL inserido depois (RFC 8414 §3.1, RFC 9728 §3.1).
func wellKnown(base *url.URL, suffix string) string {
	p := strings.TrimSuffix(base.Path, "/")
	u := url.URL{Scheme: base.Scheme, Host: base.Host, Path: "/.well-known/" + suffix + p}
	return u.String()
}

// resourceMetadataFromChallenge extrai resource_metadata de um WWW-Authenticate: Bearer ... (RFC 9728 §5.1).
func resourceMetadataFromChallenge(h string) string {
	i := strings.Index(h, "resource_metadata=")
	if i < 0 {
		return ""
	}
	v := h[i+len("resource_metadata="):]
	if strings.HasPrefix(v, `"`) {
		v = v[1:]
		if j := strings.Index(v, `"`); j >= 0 {
			return v[:j]
		}
		return ""
	}
	if j := strings.IndexAny(v, ", "); j >= 0 {
		v = v[:j]
	}
	return v
}

// Discover descobre o servidor de autorização de um servidor MCP: tenta o recurso protegido (cabeçalho
// WWW-Authenticate da resposta 401 ou /.well-known/oauth-protected-resource), depois os metadados do servidor de
// autorização (RFC 8414, com openid-configuration como alternativa) e, sem nada disso, os endpoints padrão da origem
// (/authorize, /token, /register), como manda a especificação de autorização do MCP.
func (c *Client) Discover(ctx context.Context, mcpURL string) (Discovery, error) {
	base, err := url.Parse(mcpURL)
	if err != nil || base.Host == "" {
		return Discovery{}, errors.New("oauth: URL do servidor inválida")
	}
	if err := c.check(mcpURL); err != nil {
		return Discovery{}, err
	}
	d := Discovery{Resource: canonicalResource(base)}
	asBase := &url.URL{Scheme: base.Scheme, Host: base.Host}

	prmURL := c.challengeMetadataURL(ctx, mcpURL)
	var prm protectedResource
	found := false
	for _, cand := range []string{prmURL, wellKnown(base, "oauth-protected-resource"), wellKnown(&url.URL{Scheme: base.Scheme, Host: base.Host}, "oauth-protected-resource")} {
		if cand == "" {
			continue
		}
		prm = protectedResource{}
		code, err := c.getJSON(ctx, cand, &prm)
		if err != nil {
			var re *RefusedError
			if errors.As(err, &re) {
				return Discovery{}, err
			}
			continue
		}
		if code == http.StatusOK && len(prm.AuthorizationServers) > 0 {
			found = true
			break
		}
	}
	if found {
		if prm.Resource != "" {
			d.Resource = prm.Resource
		}
		d.Scopes = prm.ScopesSupported
		u, err := url.Parse(prm.AuthorizationServers[0])
		if err != nil || u.Host == "" {
			return Discovery{}, errors.New("oauth: servidor de autorização inválido nos metadados do recurso")
		}
		asBase = u
	}

	for _, cand := range []string{wellKnown(asBase, "oauth-authorization-server"), wellKnown(asBase, "openid-configuration"),
		strings.TrimSuffix(asBase.String(), "/") + "/.well-known/openid-configuration"} {
		var m Metadata
		code, err := c.getJSON(ctx, cand, &m)
		if err != nil {
			var re *RefusedError
			if errors.As(err, &re) {
				return Discovery{}, err
			}
			continue
		}
		if code == http.StatusOK && m.AuthorizationEndpoint != "" && m.TokenEndpoint != "" {
			if err := c.validateMetadata(m); err != nil {
				return Discovery{}, err
			}
			d.Metadata = m
			return d, nil
		}
	}
	if found { // o recurso apontou um servidor, mas ele não publicou metadados: não adivinhamos outro
		return Discovery{}, errors.New("oauth: o servidor de autorização não publica metadados")
	}
	origin := asBase.Scheme + "://" + asBase.Host
	d.Metadata = Metadata{Issuer: origin, AuthorizationEndpoint: origin + "/authorize", TokenEndpoint: origin + "/token",
		RegistrationEndpoint: origin + "/register"}
	return d, nil
}

func (c *Client) validateMetadata(m Metadata) error {
	for _, e := range []string{m.AuthorizationEndpoint, m.TokenEndpoint, m.RegistrationEndpoint} {
		if e == "" {
			continue
		}
		if err := c.check(e); err != nil {
			return err
		}
	}
	if len(m.CodeChallengeMethods) > 0 {
		ok := false
		for _, x := range m.CodeChallengeMethods {
			if x == "S256" {
				ok = true
			}
		}
		if !ok {
			return ErrNoPKCE
		}
	}
	return nil
}

// challengeMetadataURL faz uma requisição sem credencial ao servidor MCP e devolve o resource_metadata do 401.
func (c *Client) challengeMetadataURL(ctx context.Context, mcpURL string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mcpURL, nil)
	if err != nil {
		return ""
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))
	if resp.StatusCode != http.StatusUnauthorized {
		return ""
	}
	return resourceMetadataFromChallenge(resp.Header.Get("WWW-Authenticate"))
}

// canonicalResource é o URI do recurso sem fragmento e sem barra final (RFC 8707 / especificação do MCP).
func canonicalResource(u *url.URL) string {
	c := url.URL{Scheme: strings.ToLower(u.Scheme), Host: strings.ToLower(u.Host), Path: strings.TrimSuffix(u.Path, "/")}
	return c.String()
}

// ---------- registro dinâmico (RFC 7591) ----------

// Registration é o cliente OAuth registrado no servidor de autorização.
type Registration struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret,omitempty"`
}

// Register registra o Hanko como cliente público (PKCE, sem segredo) no servidor de autorização.
func (c *Client) Register(ctx context.Context, m Metadata, clientName, redirectURI string) (Registration, error) {
	if m.RegistrationEndpoint == "" {
		return Registration{}, ErrNoRegistration
	}
	if err := c.check(m.RegistrationEndpoint); err != nil {
		return Registration{}, err
	}
	body, err := json.Marshal(map[string]any{
		"client_name":                clientName,
		"redirect_uris":              []string{redirectURI},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	})
	if err != nil {
		return Registration{}, fmt.Errorf("oauth: registro: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.RegistrationEndpoint, strings.NewReader(string(body)))
	if err != nil {
		return Registration{}, fmt.Errorf("oauth: registro: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Registration{}, fmt.Errorf("oauth: registro em %s: %w", redactURL(m.RegistrationEndpoint), err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return Registration{}, serverError(resp.StatusCode, raw)
	}
	var reg Registration
	if err := json.Unmarshal(raw, &reg); err != nil || reg.ClientID == "" {
		return Registration{}, errors.New("oauth: resposta de registro inválida")
	}
	return reg, nil
}

// ---------- autorização, troca e renovação ----------

// AuthURL monta a URL para onde o navegador do humano vai (OAuth 2.1: code + PKCE S256 + state + resource).
func AuthURL(m Metadata, clientID, redirectURI string, scopes []string, state, challenge, resource string) (string, error) {
	u, err := url.Parse(m.AuthorizationEndpoint)
	if err != nil || u.Host == "" {
		return "", errors.New("oauth: authorization_endpoint inválido")
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	if len(scopes) > 0 {
		q.Set("scope", strings.Join(scopes, " "))
	}
	if resource != "" {
		q.Set("resource", resource)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// Token é o resultado de uma troca ou renovação. Os valores são segredos: quem recebe os guarda cifrados.
type Token struct {
	AccessToken  string
	RefreshToken string
	TokenType    string
	Scope        string
	// ExpiresAt é nulo quando o servidor não informou expires_in.
	ExpiresAt *time.Time
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

func serverError(status int, raw []byte) error {
	var tr tokenResponse
	if json.Unmarshal(raw, &tr) == nil && tr.Error != "" {
		return &Error{Code: tr.Error, Description: truncate(tr.ErrorDesc, 200), Status: status}
	}
	return &Error{Code: fmt.Sprintf("http_%d", status), Status: status}
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func (c *Client) tokenRequest(ctx context.Context, m Metadata, form url.Values) (Token, error) {
	if err := c.check(m.TokenEndpoint); err != nil {
		return Token{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, fmt.Errorf("oauth: token: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Token{}, fmt.Errorf("oauth: token em %s: %w", redactURL(m.TokenEndpoint), err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if resp.StatusCode != http.StatusOK {
		return Token{}, serverError(resp.StatusCode, raw)
	}
	var tr tokenResponse
	if err := json.Unmarshal(raw, &tr); err != nil || tr.AccessToken == "" {
		return Token{}, errors.New("oauth: resposta de token inválida")
	}
	if tr.TokenType != "" && !strings.EqualFold(tr.TokenType, "bearer") {
		return Token{}, fmt.Errorf("oauth: token_type %q não suportado", truncate(tr.TokenType, 20))
	}
	t := Token{AccessToken: tr.AccessToken, RefreshToken: tr.RefreshToken, TokenType: "Bearer", Scope: tr.Scope}
	if tr.ExpiresIn > 0 {
		e := c.now().Add(time.Duration(tr.ExpiresIn) * time.Second).UTC()
		t.ExpiresAt = &e
	}
	return t, nil
}

// Exchange troca o código de autorização pelo token, provando a posse do verifier (PKCE).
func (c *Client) Exchange(ctx context.Context, m Metadata, reg Registration, code, verifier, redirectURI, resource string) (Token, error) {
	f := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirectURI},
		"client_id": {reg.ClientID}, "code_verifier": {verifier}}
	if resource != "" {
		f.Set("resource", resource)
	}
	if reg.ClientSecret != "" {
		f.Set("client_secret", reg.ClientSecret)
	}
	return c.tokenRequest(ctx, m, f)
}

// Refresh renova o access token. Se o servidor não devolver refresh token novo, o antigo continua valendo.
func (c *Client) Refresh(ctx context.Context, m Metadata, reg Registration, refreshToken, resource string) (Token, error) {
	f := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}, "client_id": {reg.ClientID}}
	if resource != "" {
		f.Set("resource", resource)
	}
	if reg.ClientSecret != "" {
		f.Set("client_secret", reg.ClientSecret)
	}
	t, err := c.tokenRequest(ctx, m, f)
	if err != nil {
		return Token{}, err
	}
	if t.RefreshToken == "" {
		t.RefreshToken = refreshToken
	}
	return t, nil
}

// ProbeStatus classifica a resposta de um servidor MCP a uma requisição autenticada.
type ProbeStatus string

const (
	ProbeOK           ProbeStatus = "ok"           // autenticou
	ProbeUnauthorized ProbeStatus = "unauthorized" // 401: token ausente, expirado ou revogado
	ProbeForbidden    ProbeStatus = "forbidden"    // 403: autenticou, mas sem permissão (escopo)
	ProbeUnreachable  ProbeStatus = "unreachable"  // erro de rede ou 5xx
)

// Probe faz o initialize do protocolo MCP com o token e diz se a autenticação vale.
func (c *Client) Probe(ctx context.Context, mcpURL, accessToken string) (ProbeStatus, error) {
	if err := c.check(mcpURL); err != nil {
		return ProbeUnreachable, err
	}
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"hanko","version":"1"}}}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, mcpURL, strings.NewReader(body))
	if err != nil {
		return ProbeUnreachable, fmt.Errorf("oauth: probe: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return ProbeUnreachable, fmt.Errorf("oauth: probe em %s: %w", redactURL(mcpURL), err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return ProbeUnauthorized, nil
	case resp.StatusCode == http.StatusForbidden:
		return ProbeForbidden, nil
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return ProbeOK, nil
	default:
		return ProbeUnreachable, fmt.Errorf("oauth: probe: HTTP %d", resp.StatusCode)
	}
}
