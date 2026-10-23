package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/netguard"
	"github.com/renatobardi/hanko/internal/oauth"
	"github.com/renatobardi/hanko/internal/secretref"
	"github.com/renatobardi/hanko/internal/store"
)

// Conexões OAuth para servidores MCP remotos (ADR-0043). O token mora em segredos (ADR-0050); a conexão guarda o
// estado da autorização e a integração MCP correspondente referencia o segredo por ${secret:...}.

// OAuthCallbackPath é a rota fixa da UI para onde o servidor de autorização devolve o navegador.
const OAuthCallbackPath = "/oauth/callback"

const (
	connectionOAuthTimeout = 20 * time.Second
	maxConnectionScopes    = 20
	maxConnectionScopeLen  = 100
)

var (
	connMetricsOnce sync.Once
	connCounter     metric.Int64Counter
)

// connSpan abre o span de uma operação de conexão e devolve a função que a encerra e conta (result: ok|error).
func connSpan(ctx context.Context, op string) (context.Context, func(err error)) {
	connMetricsOnce.Do(func() {
		connCounter, _ = otel.Meter("hanko/connections").Int64Counter("hanko.connection.events",
			metric.WithDescription("operações de conexão OAuth por tipo e resultado (nunca o token)"))
	})
	ctx, span := otel.Tracer("hanko/connections").Start(ctx, "hanko.connection."+op)
	return ctx, func(err error) {
		result := "ok"
		if err != nil {
			result = "error"
			span.SetStatus(codes.Error, op)
		}
		if connCounter != nil {
			connCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("op", op), attribute.String("result", result)))
		}
		span.End()
	}
}

// ConnectionView é uma conexão com o passo seguinte do setup.
type ConnectionView struct {
	domain.Connection
	Setup           domain.Setup `json:"setup"`
	IntegrationSlug string       `json:"integration_slug,omitempty"`
}

func connectionFromRow(r store.Connection) domain.Connection {
	c := domain.Connection{ID: r.ID, CompanyID: r.CompanyID, Slug: r.Slug, Name: r.Name, URL: r.Url,
		Identity: domain.ConnectionIdentity(r.Identity), OwnerUserID: r.OwnerUserID, Status: domain.ConnectionStatus(r.Status),
		IntegrationID: r.IntegrationID, Issuer: r.Issuer, Scopes: nonNil(r.Scopes), ClientRegistered: r.ClientID != "",
		ExpiresAt: r.ExpiresAt, ConnectedAt: r.ConnectedAt, LastRefreshAt: r.LastRefreshAt, LastProbeAt: r.LastProbeAt,
		LastProbeStatus: r.LastProbeStatus, LastError: r.LastError, GateID: r.GateID, AuthExpiresAt: r.AuthExpiresAt,
		CreatedBy: actorOf(r.CreatedByType, r.CreatedByID), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
	return c
}

func (s *Service) connectionView(r store.Connection) ConnectionView {
	c := connectionFromRow(r)
	return ConnectionView{Connection: c, Setup: c.NextSetup(s.Now().UTC())}
}

// oauthClient monta o cliente OAuth: o HTTP de testes, se houver, senão o de netguard (SSRF na conexão). Todo endereço
// é validado antes do uso; http só vale quando o operador liberou destinos internos.
func (s *Service) oauthClient() *oauth.Client {
	hc := s.Integrations.HTTP
	if hc == nil {
		hc = netguard.NewClient(s.Integrations.policy(), connectionOAuthTimeout)
	}
	allowHTTP := s.Integrations.privateAllowed()
	pol := s.Integrations.policy()
	return &oauth.Client{HTTP: hc, Now: s.Now, Validate: func(raw string) error {
		_, err := netguard.ValidateURL(raw, allowHTTP, pol)
		return err
	}}
}

func (s *Service) connRedirectURI() (string, error) {
	base := strings.TrimRight(strings.TrimSpace(s.PublicURL), "/")
	if base == "" {
		return "", domain.Invalid("public_url", "HANKO_PUBLIC_URL não está configurada neste servidor; sem ela o navegador não volta do OAuth")
	}
	return base + OAuthCallbackPath, nil
}

func hashState(state string) string {
	sum := sha256.Sum256([]byte(state))
	return hex.EncodeToString(sum[:])
}

func connVerifierAAD(id uuid.UUID) string { return "conn/" + id.String() }

// callerUserID devolve o usuário autenticado (nil em modo trusted).
func callerUserID(ctx context.Context) *uuid.UUID {
	if id, ok := auth.IdentityFrom(ctx); ok {
		u := id.User.ID
		return &u
	}
	return nil
}

// authorizeConn confere se o chamador pode operar a conexão: a da empresa exige admin; a pessoal, o próprio dono.
func (s *Service) authorizeConn(ctx context.Context, r store.Connection) error {
	if r.Identity == string(domain.ConnIdentityUser) {
		if err := s.require(ctx, r.CompanyID, domain.MemberMember); err != nil {
			return err
		}
		if u := callerUserID(ctx); u == nil || r.OwnerUserID == nil || *u != *r.OwnerUserID {
			return notFound("connection") // a conexão pessoal de outro não existe para quem não é o dono
		}
		return nil
	}
	return s.require(ctx, r.CompanyID, domain.MemberAdmin)
}

// getConnRow acha a conexão por id ou slug e confere que o chamador a enxerga.
func (s *Service) getConnRow(ctx context.Context, companyID domain.ID, ref string) (store.Connection, error) {
	var r store.Connection
	var err error
	if id, perr := domain.ParseID(ref); perr == nil {
		r, err = s.read().GetConnectionByID(ctx, store.GetConnectionByIDParams{CompanyID: companyID, ID: id})
	} else {
		r, err = s.read().GetConnectionBySlug(ctx, store.GetConnectionBySlugParams{CompanyID: companyID, Slug: ref})
	}
	if err != nil {
		return r, mapErr(err, "get connection")
	}
	if r.Identity == string(domain.ConnIdentityUser) {
		if u := callerUserID(ctx); u == nil || r.OwnerUserID == nil || *u != *r.OwnerUserID {
			return r, notFound("connection")
		}
	}
	return r, nil
}

func normalizeScopes(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	for _, sc := range in {
		sc = strings.TrimSpace(sc)
		if sc == "" {
			continue
		}
		if len(sc) > maxConnectionScopeLen || strings.ContainsAny(sc, " \t\r\n\"\\") {
			return nil, domain.Invalid("scopes", "escopo inválido")
		}
		if !slices.Contains(out, sc) {
			out = append(out, sc)
		}
	}
	if len(out) > maxConnectionScopes {
		return nil, domain.Invalid("scopes", fmt.Sprintf("no máximo %d escopos", maxConnectionScopes))
	}
	return out, nil
}

// CreateConnectionInput define uma conexão nova (rascunho, ainda sem autorização).
type CreateConnectionInput struct {
	Name     string
	Slug     string // derivado do nome quando vazio
	URL      string // endereço do servidor MCP http
	Identity domain.ConnectionIdentity
	Scopes   []string
	// ClientID e ClientSecretRef: cliente OAuth já registrado, para servidor sem registro dinâmico. ClientSecretRef é o
	// NOME de um segredo da empresa (nunca o valor).
	ClientID        string
	ClientSecretRef string
}

// CreateConnection cria o rascunho da conexão e, para identidade da empresa, a integração MCP (desligada) que usará o
// token. A empresa: admin. Pessoal: o próprio usuário.
func (s *Service) CreateConnection(ctx context.Context, actor domain.Actor, companyID domain.ID, in CreateConnectionInput) (v ConnectionView, err error) {
	ctx, done := connSpan(ctx, "create")
	defer func() { done(err) }()
	if actor.Type == domain.ActorPersona {
		return ConnectionView{}, domain.ErrForbidden // persona propõe por RequestConnection
	}
	if in.Identity == "" {
		in.Identity = domain.ConnIdentityCompany
	}
	if !in.Identity.Valid() {
		return ConnectionView{}, domain.Invalid("identity", "must be company or user")
	}
	var owner *uuid.UUID
	if in.Identity == domain.ConnIdentityUser {
		if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
			return ConnectionView{}, err
		}
		if owner = callerUserID(ctx); owner == nil {
			return ConnectionView{}, domain.Invalid("identity", "conexão pessoal exige uma pessoa autenticada")
		}
	} else if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return ConnectionView{}, err
	}
	var row store.Connection
	err = s.withTx(ctx, func(t *txc) error {
		var e error
		row, e = s.createConnectionTx(t, actor, companyID, owner, in)
		return e
	})
	if err != nil {
		return ConnectionView{}, err
	}
	return s.connectionView(row), nil
}

func (s *Service) createConnectionTx(t *txc, actor domain.Actor, companyID domain.ID, owner *uuid.UUID, in CreateConnectionInput) (store.Connection, error) {
	name, err := validateName(in.Name)
	if err != nil {
		return store.Connection{}, err
	}
	slug, err := resolveSlug(in.Slug, name)
	if err != nil {
		return store.Connection{}, err
	}
	if !domain.ValidConnectionSlug(slug) || slices.Contains(reservedIntegrationSlugs, slug) || slices.Contains(reservedUISlugs, slug) {
		return store.Connection{}, domain.Invalid("slug", fmt.Sprintf("%q não serve de slug de conexão (minúsculas, dígitos e hífen; começa por letra)", slug))
	}
	u, err := s.connectionURL(in.URL)
	if err != nil {
		return store.Connection{}, err
	}
	scopes, err := normalizeScopes(in.Scopes)
	if err != nil {
		return store.Connection{}, err
	}
	in.ClientID = strings.TrimSpace(in.ClientID)
	if len(in.ClientID) > 300 {
		return store.Connection{}, domain.Invalid("client_id", "too long")
	}
	if in.ClientSecretRef != "" {
		if !secretref.ValidName(in.ClientSecretRef) {
			return store.Connection{}, domain.Invalid("client_secret_ref", "nome de segredo inválido")
		}
		if _, err := t.q.GetCompanySecretByName(t.ctx, store.GetCompanySecretByNameParams{CompanyID: companyID, Name: in.ClientSecretRef}); err != nil {
			return store.Connection{}, domain.Invalid("client_secret_ref", "o segredo "+in.ClientSecretRef+" não existe na empresa")
		}
	}
	if !s.secretBox().Enabled() {
		return store.Connection{}, domain.Invalid("secrets", "HANKO_SECRET_KEY não está configurada neste servidor; sem ela não há como guardar tokens")
	}
	if _, err := t.q.GetCompanyByID(t.ctx, companyID); err != nil {
		return store.Connection{}, mapErr(err, "company")
	}
	n, err := t.q.CountConnections(t.ctx, companyID)
	if err != nil {
		return store.Connection{}, fmt.Errorf("count connections: %w", err)
	}
	if n >= domain.MaxConnectionsPerCompany {
		return store.Connection{}, domain.Invalid("name", fmt.Sprintf("a empresa já tem %d conexões (o máximo)", domain.MaxConnectionsPerCompany))
	}

	var integrationID *domain.ID
	if in.Identity == domain.ConnIdentityCompany {
		// segredo-placeholder: a integração (desligada) já referencia o token; o valor de verdade chega ao conectar
		sid := domain.NewID()
		m, err := s.prepareMaterial(sid, domain.SecretProviderLocal, "pending", false, "")
		if err != nil {
			return store.Connection{}, err
		}
		var sv SecretView
		if err := s.createSecretTx(t, actor, companyID, domain.SecretScopeCompany, nil, sid, domain.ConnectionAccessSecret(slug),
			"Token de acesso da conexão "+slug+" (gerenciado pelo Hanko)", m, &sv); err != nil {
			return store.Connection{}, err
		}
		iid := domain.NewID()
		sec := integrationSecrets{Headers: map[string]string{"Authorization": "Bearer ${secret:" + domain.ConnectionAccessSecret(slug) + "}"}}
		enc, err := s.seal(iid, sec)
		if err != nil {
			return store.Connection{}, err
		}
		cfgJSON, err := json.Marshal(domain.IntegrationConfig{Transport: domain.TransportHTTP, URL: u, HeaderNames: []string{"Authorization"}})
		if err != nil {
			return store.Connection{}, fmt.Errorf("marshal integration config: %w", err)
		}
		irow, err := t.q.CreateIntegration(t.ctx, store.CreateIntegrationParams{ID: iid, CompanyID: companyID, Kind: string(domain.IntegrationMCP),
			Slug: slug, Name: name, Enabled: false, AllPersonas: false, Config: cfgJSON, Events: []string{}, SecretEnc: enc,
			ActorType: string(actor.Type), ActorID: actor.ID, Now: t.now})
		if err != nil {
			return store.Connection{}, mapErr(err, "create integration")
		}
		if err := t.createPendingCatalog(companyID, irow.ID, irow.Kind); err != nil {
			return store.Connection{}, err
		}
		integrationID = &irow.ID
		if err := t.activity(actor, companyID, "integration.created", "integration", irow.ID,
			map[string]any{"slug": irow.Slug, "name": irow.Name, "kind": irow.Kind, "connection": true}, ""); err != nil {
			return store.Connection{}, err
		}
	}
	row, err := t.q.CreateConnection(t.ctx, store.CreateConnectionParams{ID: domain.NewID(), CompanyID: companyID, Slug: slug, Name: name,
		Url: u, Identity: string(in.Identity), OwnerUserID: owner, IntegrationID: integrationID, Scopes: scopes, ClientID: in.ClientID,
		ClientSecretRef: in.ClientSecretRef, CreatedByType: string(actor.Type), CreatedByID: actor.ID, Now: t.now})
	if err != nil {
		return store.Connection{}, mapErr(err, "create connection")
	}
	if err := t.activity(actor, companyID, "connection.created", "connection", row.ID,
		map[string]any{"slug": row.Slug, "name": row.Name, "identity": row.Identity}, ""); err != nil {
		return store.Connection{}, err
	}
	return row, nil
}

// connectionURL valida o endereço do servidor MCP de uma conexão: http(s) sem credenciais; destino interno só com a
// liberação do operador (quem conecta, no OAuth, é o servidor do Hanko).
func (s *Service) connectionURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) > maxURLBytes {
		return "", domain.Invalid("url", "too long")
	}
	u, err := netguard.ValidateURL(raw, s.Integrations.privateAllowed(), s.Integrations.policy())
	if err != nil {
		return "", domain.Invalid("url", err.Error())
	}
	u.RawQuery, u.Fragment = "", ""
	return u.String(), nil
}

// ListConnections lista as conexões da empresa visíveis ao chamador: as da empresa e as pessoais dele.
func (s *Service) ListConnections(ctx context.Context, companyID domain.ID) ([]ConnectionView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	rows, err := s.read().ListConnections(ctx, store.ListConnectionsParams{CompanyID: companyID, ViewerUserID: callerUserID(ctx)})
	if err != nil {
		return nil, fmt.Errorf("list connections: %w", err)
	}
	slugs := integrationSlugs(ctx, s.read(), companyID)
	out := make([]ConnectionView, len(rows))
	for i, r := range rows {
		out[i] = s.connectionView(r)
		if r.IntegrationID != nil {
			out[i].IntegrationSlug = slugs[*r.IntegrationID]
		}
	}
	return out, nil
}

func integrationSlugs(ctx context.Context, q *store.Queries, companyID domain.ID) map[domain.ID]string {
	m := map[domain.ID]string{}
	rows, err := q.ListIntegrations(ctx, companyID)
	if err != nil {
		return m
	}
	for _, r := range rows {
		m[r.ID] = r.Slug
	}
	return m
}

// GetConnection devolve a conexão por id ou slug.
func (s *Service) GetConnection(ctx context.Context, companyID domain.ID, ref string) (ConnectionView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return ConnectionView{}, err
	}
	r, err := s.getConnRow(ctx, companyID, ref)
	if err != nil {
		return ConnectionView{}, err
	}
	v := s.connectionView(r)
	if r.IntegrationID != nil {
		v.IntegrationSlug = integrationSlugs(ctx, s.read(), companyID)[*r.IntegrationID]
	}
	return v, nil
}

// UpdateConnectionInput muda o que não depende do servidor; nil fica como está. Mudar escopos ou cliente exige reconectar.
type UpdateConnectionInput struct {
	Name            *string
	Scopes          *[]string
	ClientID        *string
	ClientSecretRef *string
}

// UpdateConnection edita nome, escopos e cliente OAuth.
func (s *Service) UpdateConnection(ctx context.Context, actor domain.Actor, companyID domain.ID, ref string, in UpdateConnectionInput) (v ConnectionView, err error) {
	ctx, done := connSpan(ctx, "update")
	defer func() { done(err) }()
	r, err := s.getConnRow(ctx, companyID, ref)
	if err != nil {
		return ConnectionView{}, err
	}
	if err := s.authorizeConn(ctx, r); err != nil {
		return ConnectionView{}, err
	}
	name, scopes, cid, csr := r.Name, r.Scopes, r.ClientID, r.ClientSecretRef
	if in.Name != nil {
		if name, err = validateName(*in.Name); err != nil {
			return ConnectionView{}, err
		}
	}
	if in.Scopes != nil {
		if scopes, err = normalizeScopes(*in.Scopes); err != nil {
			return ConnectionView{}, err
		}
	}
	if in.ClientID != nil {
		cid = strings.TrimSpace(*in.ClientID)
		if len(cid) > 300 {
			return ConnectionView{}, domain.Invalid("client_id", "too long")
		}
	}
	if in.ClientSecretRef != nil {
		csr = strings.TrimSpace(*in.ClientSecretRef)
		if csr != "" {
			if !secretref.ValidName(csr) {
				return ConnectionView{}, domain.Invalid("client_secret_ref", "nome de segredo inválido")
			}
			if _, e := s.read().GetCompanySecretByName(ctx, store.GetCompanySecretByNameParams{CompanyID: companyID, Name: csr}); e != nil {
				return ConnectionView{}, domain.Invalid("client_secret_ref", "o segredo "+csr+" não existe na empresa")
			}
		}
	}
	var row store.Connection
	err = s.withTx(ctx, func(t *txc) error {
		cur, e := t.q.GetConnectionByIDForUpdate(t.ctx, store.GetConnectionByIDForUpdateParams{CompanyID: companyID, ID: r.ID})
		if e != nil {
			return mapErr(e, "get connection")
		}
		if row, e = t.q.UpdateConnectionSettings(t.ctx, store.UpdateConnectionSettingsParams{CompanyID: companyID, ID: cur.ID, Name: name,
			Scopes: scopes, ClientID: cid, ClientSecretRef: csr, Now: t.now}); e != nil {
			return mapErr(e, "update connection")
		}
		return t.activity(actor, companyID, "connection.updated", "connection", row.ID, map[string]any{"slug": row.Slug}, "")
	})
	if err != nil {
		return ConnectionView{}, err
	}
	return s.connectionView(row), nil
}

// ConnectionAuthStart é a URL para onde o navegador do humano vai autorizar.
type ConnectionAuthStart struct {
	URL       string    `json:"authorization_url"`
	ExpiresAt time.Time `json:"expires_at"`
	Connected bool      `json:"-"`
}

// AuthorizeConnection começa (ou recomeça) a autorização: descobre o servidor, registra o cliente se preciso, gera
// state e PKCE e devolve a URL de autorização. Pode ser chamada em qualquer estado ("Reconectar").
func (s *Service) AuthorizeConnection(ctx context.Context, actor domain.Actor, companyID domain.ID, ref string) (out ConnectionAuthStart, err error) {
	ctx, done := connSpan(ctx, "authorize")
	defer func() { done(err) }()
	if actor.Type != domain.ActorHuman {
		return out, domain.ErrForbidden // só um humano consente
	}
	r, err := s.getConnRow(ctx, companyID, ref)
	if err != nil {
		return out, err
	}
	if err := s.authorizeConn(ctx, r); err != nil {
		return out, err
	}
	if !s.secretBox().Enabled() {
		return out, domain.Invalid("secrets", "HANKO_SECRET_KEY não está configurada neste servidor; sem ela não há como guardar tokens")
	}
	redirect, err := s.connRedirectURI()
	if err != nil {
		return out, err
	}
	dctx, cancel := context.WithTimeout(ctx, 2*connectionOAuthTimeout)
	defer cancel()
	oc := s.oauthClient()
	d, err := oc.Discover(dctx, r.Url)
	if err != nil {
		return out, s.connFail(ctx, r, "descoberta do servidor de autorização", err)
	}
	clientID := r.ClientID
	if clientID == "" || (r.Issuer != "" && r.Issuer != d.Metadata.Issuer && r.ClientSecretRef == "") {
		reg, rerr := oc.Register(dctx, d.Metadata, "Hanko", redirect)
		switch {
		case errors.Is(rerr, oauth.ErrNoRegistration):
			if clientID == "" {
				return out, domain.Invalid("client_id", "o servidor não oferece registro dinâmico: informe o client_id da conexão")
			}
		case rerr != nil:
			return out, s.connFail(ctx, r, "registro do cliente", rerr)
		default:
			clientID = reg.ClientID
		}
	}
	scopes := r.Scopes
	if len(scopes) == 0 {
		scopes = d.Scopes
	}
	verifier, challenge, err := oauth.NewPKCE()
	if err != nil {
		return out, err
	}
	state, err := oauth.NewState()
	if err != nil {
		return out, err
	}
	authURL, err := oauth.AuthURL(d.Metadata, clientID, redirect, scopes, state, challenge, d.Resource)
	if err != nil {
		return out, s.connFail(ctx, r, "URL de autorização", err)
	}
	encVerifier, err := s.secretBox().Seal([]byte(verifier), connVerifierAAD(r.ID))
	if err != nil {
		return out, fmt.Errorf("seal verifier: %w", err)
	}
	expires := s.Now().UTC().Add(domain.ConnectionAuthTTL)
	err = s.withTx(ctx, func(t *txc) error {
		cur, e := t.q.GetConnectionByIDForUpdate(t.ctx, store.GetConnectionByIDForUpdateParams{CompanyID: companyID, ID: r.ID})
		if e != nil {
			return mapErr(e, "get connection")
		}
		if _, e = t.q.SetConnectionDiscovery(t.ctx, store.SetConnectionDiscoveryParams{CompanyID: companyID, ID: cur.ID, Resource: d.Resource,
			Issuer: d.Metadata.Issuer, AuthorizationEndpoint: d.Metadata.AuthorizationEndpoint, TokenEndpoint: d.Metadata.TokenEndpoint,
			RegistrationEndpoint: d.Metadata.RegistrationEndpoint, ClientID: clientID, Scopes: nonNil(scopes), Now: t.now}); e != nil {
			return mapErr(e, "set discovery")
		}
		h := hashState(state)
		if _, e = t.q.StartConnectionAuth(t.ctx, store.StartConnectionAuthParams{CompanyID: companyID, ID: cur.ID, AuthStateHash: &h,
			AuthVerifierEnc: encVerifier, AuthStartedByID: callerUserID(ctx), AuthExpiresAt: &expires, Now: t.now}); e != nil {
			return mapErr(e, "start auth")
		}
		return t.activity(actor, companyID, "connection.authorize_started", "connection", cur.ID,
			map[string]any{"slug": cur.Slug, "issuer": d.Metadata.Issuer}, "")
	})
	if err != nil {
		return out, err
	}
	return ConnectionAuthStart{URL: authURL, ExpiresAt: expires}, nil
}

// connFail transforma uma falha de OAuth em erro de domínio (422) sem vazar corpo de resposta nem token, e a registra
// como último erro da conexão.
func (s *Service) connFail(ctx context.Context, r store.Connection, what string, err error) error {
	msg := what + ": " + oauthMessage(err)
	_ = s.recordConnError(ctx, r.CompanyID, r.ID, "connection.error", msg)
	return domain.Invalid("connection", msg)
}

func oauthMessage(err error) string {
	var oe *oauth.Error
	var re *oauth.RefusedError
	switch {
	case errors.Is(err, oauth.ErrNoPKCE):
		return "o servidor não oferece PKCE S256"
	case errors.As(err, &re):
		return "endereço não permitido (" + re.URL + ")"
	case errors.As(err, &oe):
		return oe.Error()
	}
	m := err.Error()
	if len(m) > 200 {
		m = m[:200]
	}
	return m
}

func (s *Service) recordConnError(ctx context.Context, companyID, id domain.ID, verb, msg string) error {
	return s.withTx(context.WithoutCancel(ctx), func(t *txc) error {
		if err := t.q.SetConnectionError(t.ctx, store.SetConnectionErrorParams{CompanyID: companyID, ID: id, LastError: truncateRunes(msg, 480), Now: t.now}); err != nil {
			return err
		}
		return t.activity(domain.SystemActor, companyID, verb, "connection", id, map[string]any{"error": truncateRunes(msg, 200)}, "")
	})
}

// CompleteConnectionInput é o retorno do navegador (GET /oauth/callback).
type CompleteConnectionInput struct {
	State     string
	Code      string
	ErrorCode string // error= do servidor de autorização (ex.: access_denied)
	ErrorDesc string
}

// CompleteConnectionAuth conclui a autorização: consome o state (uso único), troca o código pelo token provando o
// verifier, grava os tokens como segredos, liga a integração e aprova o gate da conexão, se houver.
func (s *Service) CompleteConnectionAuth(ctx context.Context, actor domain.Actor, in CompleteConnectionInput) (v ConnectionView, err error) {
	ctx, done := connSpan(ctx, "callback")
	defer func() { done(err) }()
	if in.State == "" {
		return ConnectionView{}, notFound("authorization")
	}
	hash := hashState(in.State)
	var row store.Connection
	var verifier string
	var expired bool
	err = s.withTx(ctx, func(t *txc) error {
		r, e := t.q.GetConnectionByStateHash(t.ctx, &hash)
		if e != nil {
			return mapErr(e, "authorization")
		}
		if e = s.authorizeConn(t.ctx, r); e != nil {
			return e
		}
		if u := callerUserID(ctx); u != nil && r.AuthStartedByID != nil && *u != *r.AuthStartedByID {
			return domain.ErrForbidden // quem volta do navegador tem de ser quem começou
		}
		row = r
		if r.AuthExpiresAt == nil || !r.AuthExpiresAt.After(t.now) {
			if _, e = t.q.ClearConnectionAuth(t.ctx, store.ClearConnectionAuthParams{CompanyID: r.CompanyID, ID: r.ID,
				LastError: "a autorização expirou; comece de novo", Now: t.now}); e != nil {
				return mapErr(e, "clear auth")
			}
			expired = true // confirma a limpeza; o erro sai depois do commit
			return t.activity(actor, r.CompanyID, "connection.authorize_expired", "connection", r.ID, map[string]any{"slug": r.Slug}, "")
		}
		plain, e := s.secretBox().Open(r.AuthVerifierEnc, connVerifierAAD(r.ID))
		if e != nil {
			return fmt.Errorf("open verifier: %w", e)
		}
		verifier = string(plain)
		msg := ""
		if in.ErrorCode != "" {
			msg = "autorização recusada pelo servidor: " + truncateRunes(in.ErrorCode+" "+in.ErrorDesc, 200)
		}
		// consome o state em qualquer desfecho: o retorno é de uso único
		if _, e = t.q.ClearConnectionAuth(t.ctx, store.ClearConnectionAuthParams{CompanyID: r.CompanyID, ID: r.ID, LastError: msg, Now: t.now}); e != nil {
			return mapErr(e, "clear auth")
		}
		if msg != "" {
			if e = t.activity(actor, r.CompanyID, "connection.authorize_denied", "connection", r.ID, map[string]any{"slug": r.Slug}, ""); e != nil {
				return e
			}
			in.ErrorCode = msg
			return nil
		}
		return t.activity(actor, r.CompanyID, "connection.authorize_returned", "connection", r.ID, map[string]any{"slug": r.Slug}, "")
	})
	if err != nil {
		return ConnectionView{}, err
	}
	known := s.connectionView(row) // a conexão já é conhecida: os erros abaixo a devolvem para a UI saber a empresa
	if expired {
		return known, fmt.Errorf("a autorização expirou; comece de novo: %w", domain.ErrConflict)
	}
	if in.ErrorCode != "" {
		return known, domain.Invalid("authorization", in.ErrorCode)
	}
	if in.Code == "" {
		return known, domain.Invalid("code", "o retorno do servidor não trouxe o código")
	}
	redirect, err := s.connRedirectURI()
	if err != nil {
		return known, err
	}
	reg, err := s.connRegistration(ctx, row)
	if err != nil {
		return known, err
	}
	xctx, cancel := context.WithTimeout(ctx, connectionOAuthTimeout)
	defer cancel()
	tok, err := s.oauthClient().Exchange(xctx, connMetadata(row), reg, in.Code, verifier, redirect, row.Resource)
	if err != nil {
		return known, s.connFail(ctx, row, "troca do código pelo token", err)
	}
	var fresh store.Connection
	err = s.withTx(ctx, func(t *txc) error {
		var e error
		fresh, e = s.storeTokenTx(t, actor, row, tok, true)
		return e
	})
	if err != nil {
		return known, err
	}
	s.afterConnected(ctx, actor, fresh)
	return s.connectionView(fresh), nil
}

func connMetadata(r store.Connection) oauth.Metadata {
	return oauth.Metadata{Issuer: r.Issuer, AuthorizationEndpoint: r.AuthorizationEndpoint, TokenEndpoint: r.TokenEndpoint, RegistrationEndpoint: r.RegistrationEndpoint}
}

// connRegistration monta o cliente OAuth da conexão; o segredo do cliente, se houver, é resolvido agora.
func (s *Service) connRegistration(ctx context.Context, r store.Connection) (oauth.Registration, error) {
	reg := oauth.Registration{ClientID: r.ClientID}
	if r.ClientSecretRef != "" {
		v, err := s.resolveRef(ctx, r.CompanyID, secretref.Ref{Name: r.ClientSecretRef}, nil)
		if err != nil {
			return reg, domain.Invalid("client_secret_ref", "segredo do cliente indisponível: "+r.ClientSecretRef)
		}
		reg.ClientSecret = v
	}
	return reg, nil
}

// secretScopeOf devolve onde os tokens da conexão ficam.
func secretScopeOf(r store.Connection) (domain.SecretScope, *uuid.UUID) {
	if r.Identity == string(domain.ConnIdentityUser) {
		return domain.SecretScopeUser, r.OwnerUserID
	}
	return domain.SecretScopeCompany, nil
}

// upsertScopedSecretTx cria o segredo local no escopo dado ou troca o valor (nova versão).
func (s *Service) upsertScopedSecretTx(t *txc, actor domain.Actor, companyID domain.ID, scope domain.SecretScope, owner *uuid.UUID, name, desc, value string) error {
	if scope == domain.SecretScopeCompany {
		return s.upsertCompanySecretTx(t, actor, companyID, name, desc, value)
	}
	cur, err := t.q.GetUserSecretByNameForUpdate(t.ctx, store.GetUserSecretByNameForUpdateParams{CompanyID: companyID, OwnerUserID: owner, Name: name})
	switch {
	case err == nil:
		m, err := s.prepareMaterial(cur.ID, domain.SecretProviderLocal, value, false, "")
		if err != nil {
			return err
		}
		row, err := t.q.RotateSecret(t.ctx, store.RotateSecretParams{ID: cur.ID, CompanyID: companyID, Provider: string(m.provider),
			ValueEnc: m.enc, Description: cur.Description, Now: t.now})
		if err != nil {
			return mapErr(err, "rotate secret")
		}
		return t.activity(actor, companyID, "secret.rotated", "secret", row.ID, secretEventData(row), "")
	case !isNoRows(err):
		return fmt.Errorf("get secret: %w", err)
	}
	id := domain.NewID()
	m, err := s.prepareMaterial(id, domain.SecretProviderLocal, value, false, "")
	if err != nil {
		return err
	}
	var sv SecretView
	return s.createSecretTx(t, actor, companyID, scope, owner, id, name, desc, m, &sv)
}

// storeTokenTx grava access/refresh como segredos e atualiza a conexão. first=true é a primeira autorização (liga a
// integração e liga a persona do gate); false é uma renovação.
func (s *Service) storeTokenTx(t *txc, actor domain.Actor, r store.Connection, tok oauth.Token, first bool) (store.Connection, error) {
	scope, owner := secretScopeOf(r)
	if err := s.upsertScopedSecretTx(t, actor, r.CompanyID, scope, owner, domain.ConnectionAccessSecret(r.Slug),
		"Token de acesso da conexão "+r.Slug+" (gerenciado pelo Hanko)", tok.AccessToken); err != nil {
		return r, err
	}
	if tok.RefreshToken != "" {
		if err := s.upsertScopedSecretTx(t, actor, r.CompanyID, scope, owner, domain.ConnectionRefreshSecret(r.Slug),
			"Token de renovação da conexão "+r.Slug+" (gerenciado pelo Hanko)", tok.RefreshToken); err != nil {
			return r, err
		}
	}
	var row store.Connection
	var err error
	if first {
		row, err = t.q.FinishConnectionAuth(t.ctx, store.FinishConnectionAuthParams{CompanyID: r.CompanyID, ID: r.ID, ExpiresAt: tok.ExpiresAt, Now: &t.now})
	} else {
		row, err = t.q.SetConnectionToken(t.ctx, store.SetConnectionTokenParams{CompanyID: r.CompanyID, ID: r.ID, ExpiresAt: tok.ExpiresAt, Now: &t.now})
	}
	if err != nil {
		return r, mapErr(err, "store token")
	}
	verb := "connection.refreshed"
	if first {
		verb = "connection.connected"
		if row.IntegrationID != nil {
			if err := t.q.SetIntegrationEnabled(t.ctx, store.SetIntegrationEnabledParams{CompanyID: r.CompanyID, ID: *row.IntegrationID, Enabled: true, Now: t.now}); err != nil {
				return r, fmt.Errorf("enable integration: %w", err)
			}
			if r.GateID != nil { // a persona que pediu a conexão passa a poder usá-la
				if g, gerr := t.q.GetGateByID(t.ctx, store.GetGateByIDParams{CompanyID: r.CompanyID, ID: *r.GateID}); gerr == nil && g.PersonaID != nil {
					if err := t.q.SetIntegrationPersona(t.ctx, store.SetIntegrationPersonaParams{CompanyID: r.CompanyID, IntegrationID: *row.IntegrationID, PersonaID: *g.PersonaID}); err != nil {
						return r, fmt.Errorf("link persona: %w", err)
					}
				}
			}
		}
	}
	if err := t.activity(actor, r.CompanyID, verb, "connection", r.ID, map[string]any{"slug": r.Slug, "identity": r.Identity}, ""); err != nil {
		return r, err
	}
	return row, nil
}

// afterConnected roda depois do commit da primeira autorização: descobre as tools (melhor esforço) e aprova o gate.
func (s *Service) afterConnected(ctx context.Context, actor domain.Actor, r store.Connection) {
	if r.IntegrationID != nil {
		_, _ = s.refreshCatalogBestEffort(ctx, r)
	}
	if r.GateID == nil {
		return
	}
	g, err := s.read().GetGateByID(ctx, store.GetGateByIDParams{CompanyID: r.CompanyID, ID: *r.GateID})
	if err != nil || domain.GateStatus(g.Status) != domain.GatePending {
		return
	}
	if _, err := s.DecideGate(ctx, actor, r.CompanyID, g.ID, DecideGateInput{Decision: domain.GateApproved, Note: "Conexão " + r.Name + " autorizada."}); err != nil {
		_ = s.recordConnError(ctx, r.CompanyID, r.ID, "connection.error", "conectou, mas não consegui aprovar o gate: "+oauthMessage(err))
	}
}

func (s *Service) refreshCatalogBestEffort(ctx context.Context, r store.Connection) (ToolCatalogResult, error) {
	integ, err := s.read().GetIntegrationByID(ctx, store.GetIntegrationByIDParams{CompanyID: r.CompanyID, ID: *r.IntegrationID})
	if err != nil {
		return ToolCatalogResult{}, err
	}
	return s.refreshCatalog(ctx, domain.SystemActor, integ)
}

// ---------- renovação ----------

// connectionToken lê o segredo de token da conexão (acesso ou renovação).
func (s *Service) connectionToken(ctx context.Context, r store.Connection, name string) (string, error) {
	return s.resolveRef(ctx, r.CompanyID, secretref.Ref{Name: name, User: r.Identity == string(domain.ConnIdentityUser)}, r.OwnerUserID)
}

// refreshConnection renova o token (uma renovação por vez; a rede roda fora de transação). Devolve a linha atual.
func (s *Service) refreshConnection(ctx context.Context, actor domain.Actor, r store.Connection) (out store.Connection, err error) {
	ctx, done := connSpan(ctx, "refresh")
	defer func() { done(err) }()
	now := s.Now().UTC()
	q := s.read()
	until := now.Add(domain.ConnectionRefreshLease)
	cur, err := q.ClaimConnectionRefresh(ctx, store.ClaimConnectionRefreshParams{CompanyID: r.CompanyID, ID: r.ID, Until: &until, Now: &now})
	if err != nil {
		if isNoRows(err) {
			return r, nil // outra renovação em andamento, ou a conexão não está conectada
		}
		return r, fmt.Errorf("claim refresh: %w", err)
	}
	release := func() {
		_ = q.ReleaseConnectionRefresh(context.WithoutCancel(ctx), store.ReleaseConnectionRefreshParams{CompanyID: r.CompanyID, ID: r.ID})
	}
	rt, err := s.connectionToken(ctx, cur, domain.ConnectionRefreshSecret(cur.Slug))
	if err != nil || rt == "" {
		release()
		return s.markConn(ctx, actor, cur, domain.ConnExpired, "sem token de renovação: autorize de novo", "connection.refresh_failed")
	}
	reg, err := s.connRegistration(ctx, cur)
	if err != nil {
		release()
		return cur, err
	}
	rctx, cancel := context.WithTimeout(ctx, connectionOAuthTimeout)
	defer cancel()
	tok, err := s.oauthClient().Refresh(rctx, connMetadata(cur), reg, rt, cur.Resource)
	switch {
	case errors.Is(err, oauth.ErrInvalidGrant):
		return s.markConn(ctx, actor, cur, domain.ConnRevoked, "o servidor recusou a renovação; autorize de novo", "connection.refresh_failed")
	case err != nil:
		msg := "renovação falhou: " + oauthMessage(err)
		st := domain.ConnConnected
		if cur.ExpiresAt != nil && !cur.ExpiresAt.After(now) {
			st = domain.ConnError // o token já venceu e não renovou
		}
		return s.markConn(ctx, actor, cur, st, msg, "connection.refresh_failed")
	}
	err = s.withTx(ctx, func(t *txc) error {
		var e error
		out, e = s.storeTokenTx(t, actor, cur, tok, false)
		return e
	})
	if err != nil {
		release()
		return cur, err
	}
	return out, nil
}

// markConn grava um estado com o motivo e registra a atividade (sem segredo).
func (s *Service) markConn(ctx context.Context, actor domain.Actor, r store.Connection, st domain.ConnectionStatus, msg, verb string) (store.Connection, error) {
	var row store.Connection
	err := s.withTx(ctx, func(t *txc) error {
		var e error
		if row, e = t.q.SetConnectionStatus(t.ctx, store.SetConnectionStatusParams{CompanyID: r.CompanyID, ID: r.ID, Status: string(st),
			LastError: truncateRunes(msg, 480), Now: t.now}); e != nil {
			return mapErr(e, "set status")
		}
		return t.activity(actor, r.CompanyID, verb, "connection", r.ID, map[string]any{"slug": r.Slug, "status": st}, "")
	})
	return row, err
}

// RefreshConnection renova o token agora (admin, ou o dono da conexão pessoal).
func (s *Service) RefreshConnection(ctx context.Context, actor domain.Actor, companyID domain.ID, ref string) (ConnectionView, error) {
	r, err := s.getConnRow(ctx, companyID, ref)
	if err != nil {
		return ConnectionView{}, err
	}
	if err := s.authorizeConn(ctx, r); err != nil {
		return ConnectionView{}, err
	}
	if domain.ConnectionStatus(r.Status) != domain.ConnConnected {
		return ConnectionView{}, fmt.Errorf("a conexão está %s: autorize primeiro: %w", r.Status, domain.ErrConflict)
	}
	row, err := s.refreshConnection(ctx, actor, r)
	if err != nil {
		return ConnectionView{}, err
	}
	return s.connectionView(row), nil
}

// EnsureConnectionFresh é chamado pelo gateway antes de abrir uma integração: se ela pertence a uma conexão e o token
// vence em breve, renova. Melhor esforço (a chamada seguinte mostra o erro de verdade). Devolve o erro de estado se a
// conexão não está autorizada.
func (s *Service) ensureConnectionFresh(ctx context.Context, integ store.Integration) error {
	r, err := s.read().GetConnectionByIntegration(ctx, store.GetConnectionByIntegrationParams{CompanyID: integ.CompanyID, IntegrationID: &integ.ID})
	if err != nil {
		return nil // integração comum: sem conexão
	}
	if st := domain.ConnectionStatus(r.Status); st != domain.ConnConnected {
		return fmt.Errorf("a conexão %q não está autorizada (%s): autorize em Integrações › Conexões", r.Slug, st)
	}
	if connectionFromRow(r).NeedsRefresh(s.Now().UTC(), domain.ConnectionRefreshSoon) {
		_, _ = s.refreshConnection(ctx, domain.SystemActor, r)
	}
	return nil
}

// DueConnection identifica uma conexão a renovar.
type DueConnection struct{ ID, CompanyID domain.ID }

// RefreshDueConnections renova as conexões que vencem em breve (varredura periódica). Devolve quantas renovou.
func (s *Service) RefreshDueConnections(ctx context.Context, now time.Time, limit int) (int, error) {
	if limit <= 0 {
		limit = 20
	}
	before := now.Add(domain.ConnectionRefreshAhead)
	rows, err := s.read().DueConnectionRefreshes(ctx, store.DueConnectionRefreshesParams{Before: &before, Now: &now, Lim: int32(limit)}) //nolint:gosec // limit pequeno e positivo
	if err != nil {
		return 0, fmt.Errorf("due connections: %w", err)
	}
	n := 0
	var errs []error
	for _, d := range rows {
		r, err := s.read().GetConnectionByID(ctx, store.GetConnectionByIDParams{CompanyID: d.CompanyID, ID: d.ID})
		if err != nil {
			continue
		}
		if _, err := s.refreshConnection(ctx, domain.SystemActor, r); err != nil {
			errs = append(errs, err)
			continue
		}
		n++
	}
	return n, errors.Join(errs...)
}

// ---------- probe ----------

// ProbeConnection testa a autenticação: initialize do MCP com o token. 401 tenta renovar uma vez; depois, expira.
func (s *Service) ProbeConnection(ctx context.Context, actor domain.Actor, companyID domain.ID, ref string) (v ConnectionView, err error) {
	ctx, done := connSpan(ctx, "probe")
	defer func() { done(err) }()
	r, err := s.getConnRow(ctx, companyID, ref)
	if err != nil {
		return ConnectionView{}, err
	}
	if err := s.authorizeConn(ctx, r); err != nil {
		return ConnectionView{}, err
	}
	st := domain.ConnectionStatus(r.Status)
	if st == domain.ConnDraft || st == domain.ConnPendingAuth {
		return ConnectionView{}, fmt.Errorf("a conexão ainda não foi autorizada: %w", domain.ErrConflict)
	}
	probe := func(row store.Connection) (oauth.ProbeStatus, error) {
		tok, terr := s.connectionToken(ctx, row, domain.ConnectionAccessSecret(row.Slug))
		if terr != nil {
			return oauth.ProbeUnauthorized, nil
		}
		pctx, cancel := context.WithTimeout(ctx, connectionOAuthTimeout)
		defer cancel()
		return s.oauthClient().Probe(pctx, row.Url, tok)
	}
	res, perr := probe(r)
	revoked := false
	if res == oauth.ProbeUnauthorized && st == domain.ConnConnected {
		if nr, rerr := s.refreshConnection(ctx, domain.SystemActor, r); rerr == nil {
			switch domain.ConnectionStatus(nr.Status) {
			case domain.ConnConnected:
				r = nr
				res, perr = probe(r)
			case domain.ConnRevoked:
				revoked = true // o servidor recusou o refresh: o estado já foi gravado, o probe não o rebaixa
			}
		}
	}
	newStatus, msg := domain.ConnConnected, ""
	switch res {
	case oauth.ProbeOK:
	case oauth.ProbeForbidden:
		msg = "o servidor autenticou, mas recusou a permissão (escopo insuficiente?)"
	case oauth.ProbeUnauthorized:
		newStatus, msg = domain.ConnExpired, "o servidor recusou o token; reconecte"
	default:
		newStatus, msg = domain.ConnError, "servidor inalcançável"
		if perr != nil {
			msg = "servidor inalcançável: " + oauthMessage(perr)
		}
	}
	if revoked || st == domain.ConnRevoked {
		newStatus, msg = domain.ConnRevoked, "o servidor recusou a renovação; autorize de novo"
	}
	var row store.Connection
	err = s.withTx(ctx, func(t *txc) error {
		var e error
		if row, e = t.q.SetConnectionProbe(t.ctx, store.SetConnectionProbeParams{CompanyID: companyID, ID: r.ID, LastProbeStatus: string(res),
			Status: string(newStatus), LastError: truncateRunes(msg, 480), Now: &t.now}); e != nil {
			return mapErr(e, "probe")
		}
		return t.activity(actor, companyID, "connection.probed", "connection", r.ID, map[string]any{"slug": r.Slug, "result": res}, "")
	})
	if err != nil {
		return ConnectionView{}, err
	}
	return s.connectionView(row), nil
}

// ---------- desconectar e apagar ----------

// DisconnectConnection apaga os tokens, desliga a integração e volta a conexão a rascunho.
func (s *Service) DisconnectConnection(ctx context.Context, actor domain.Actor, companyID domain.ID, ref string) (v ConnectionView, err error) {
	ctx, done := connSpan(ctx, "disconnect")
	defer func() { done(err) }()
	r, err := s.getConnRow(ctx, companyID, ref)
	if err != nil {
		return ConnectionView{}, err
	}
	if err := s.authorizeConn(ctx, r); err != nil {
		return ConnectionView{}, err
	}
	var row store.Connection
	err = s.withTx(ctx, func(t *txc) error {
		cur, e := t.q.GetConnectionByIDForUpdate(t.ctx, store.GetConnectionByIDForUpdateParams{CompanyID: companyID, ID: r.ID})
		if e != nil {
			return mapErr(e, "get connection")
		}
		if e = s.dropTokensTx(t, actor, cur, false); e != nil {
			return e
		}
		if cur.IntegrationID != nil {
			if e = t.q.SetIntegrationEnabled(t.ctx, store.SetIntegrationEnabledParams{CompanyID: companyID, ID: *cur.IntegrationID, Enabled: false, Now: t.now}); e != nil {
				return fmt.Errorf("disable integration: %w", e)
			}
		}
		if row, e = t.q.DisconnectConnection(t.ctx, store.DisconnectConnectionParams{CompanyID: companyID, ID: cur.ID, Now: t.now}); e != nil {
			return mapErr(e, "disconnect")
		}
		return t.activity(actor, companyID, "connection.disconnected", "connection", cur.ID, map[string]any{"slug": cur.Slug}, "")
	})
	if err != nil {
		return ConnectionView{}, err
	}
	return s.connectionView(row), nil
}

// dropTokensTx troca o segredo de acesso por um placeholder (a integração continua com referência válida) e apaga o de
// renovação. Com remove, apaga os dois (a conexão está sendo excluída).
func (s *Service) dropTokensTx(t *txc, actor domain.Actor, r store.Connection, remove bool) error {
	scope, owner := secretScopeOf(r)
	get := func(name string) (store.Secret, error) {
		return getSecretRow(t.ctx, t.q, r.CompanyID, scope, owner, name, true)
	}
	if acc, err := get(domain.ConnectionAccessSecret(r.Slug)); err == nil {
		if remove {
			if _, err := t.q.DeleteSecret(t.ctx, store.DeleteSecretParams{ID: acc.ID, CompanyID: r.CompanyID}); err != nil {
				return fmt.Errorf("delete access secret: %w", err)
			}
		} else if err := s.upsertScopedSecretTx(t, actor, r.CompanyID, scope, owner, acc.Name, acc.Description, "disconnected"); err != nil {
			return err
		}
	} else if !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	if ref, err := get(domain.ConnectionRefreshSecret(r.Slug)); err == nil {
		if _, err := t.q.DeleteSecret(t.ctx, store.DeleteSecretParams{ID: ref.ID, CompanyID: r.CompanyID}); err != nil {
			return fmt.Errorf("delete refresh secret: %w", err)
		}
	} else if !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	return nil
}

// DeleteConnection apaga a conexão, a integração e os segredos de token.
func (s *Service) DeleteConnection(ctx context.Context, actor domain.Actor, companyID domain.ID, ref string) (err error) {
	ctx, done := connSpan(ctx, "delete")
	defer func() { done(err) }()
	r, err := s.getConnRow(ctx, companyID, ref)
	if err != nil {
		return err
	}
	if err := s.authorizeConn(ctx, r); err != nil {
		return err
	}
	return s.withTx(ctx, func(t *txc) error {
		cur, e := t.q.GetConnectionByIDForUpdate(t.ctx, store.GetConnectionByIDForUpdateParams{CompanyID: companyID, ID: r.ID})
		if e != nil {
			return mapErr(e, "get connection")
		}
		if e = t.q.DeleteConnection(t.ctx, store.DeleteConnectionParams{CompanyID: companyID, ID: cur.ID}); e != nil {
			return fmt.Errorf("delete connection: %w", e)
		}
		if cur.IntegrationID != nil {
			if _, e = t.q.DeleteIntegration(t.ctx, store.DeleteIntegrationParams{CompanyID: companyID, ID: *cur.IntegrationID}); e != nil {
				return fmt.Errorf("delete integration: %w", e)
			}
		}
		if e = s.dropTokensTx(t, actor, cur, true); e != nil {
			return e
		}
		return t.activity(actor, companyID, "connection.deleted", "connection", cur.ID, map[string]any{"slug": cur.Slug}, "")
	})
}
