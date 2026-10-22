package domain

import (
	"regexp"
	"time"
)

// Conexões OAuth para servidores MCP remotos (ADR-0043). Nada aqui carrega token: o valor mora nos segredos
// conn.<slug>.access e conn.<slug>.refresh.

// ConnectionStatus é o estado da autorização de uma conexão.
type ConnectionStatus string

const (
	ConnDraft       ConnectionStatus = "draft"        // criada, sem autorização ("concluir depois")
	ConnPendingAuth ConnectionStatus = "pending_auth" // URL de autorização emitida, esperando o retorno do navegador
	ConnConnected   ConnectionStatus = "connected"
	ConnExpired     ConnectionStatus = "expired" // token vencido e sem renovação possível
	ConnError       ConnectionStatus = "error"   // probe ou renovação falhou por rede/servidor; o token foi mantido
	ConnRevoked     ConnectionStatus = "revoked" // o servidor recusou o refresh (invalid_grant): autorizar de novo
)

// Valid informa se o estado é conhecido.
func (s ConnectionStatus) Valid() bool {
	switch s {
	case ConnDraft, ConnPendingAuth, ConnConnected, ConnExpired, ConnError, ConnRevoked:
		return true
	}
	return false
}

// Usable informa se o token pode ser usado numa chamada agora (a validade é checada à parte).
func (s ConnectionStatus) Usable() bool { return s == ConnConnected }

// NeedsAuthorization informa se o estado exige um humano autorizar (ou reautorizar) no navegador.
func (s ConnectionStatus) NeedsAuthorization() bool {
	return s == ConnDraft || s == ConnPendingAuth || s == ConnExpired || s == ConnRevoked
}

// ConnectionIdentity diz de quem é o token: da empresa (personas usam) ou de um usuário (só ele usa).
type ConnectionIdentity string

const (
	ConnIdentityCompany ConnectionIdentity = "company"
	ConnIdentityUser    ConnectionIdentity = "user"
)

// Valid informa se a identidade é conhecida.
func (i ConnectionIdentity) Valid() bool { return i == ConnIdentityCompany || i == ConnIdentityUser }

// Tempos da conexão.
const (
	// ConnectionAuthTTL é a validade do pedido de autorização (state + verifier) em voo.
	ConnectionAuthTTL = 15 * time.Minute
	// ConnectionRefreshSoon: o gateway renova um token que vence em menos que isso.
	ConnectionRefreshSoon = 2 * time.Minute
	// ConnectionRefreshAhead: a varredura periódica renova um token que vence em menos que isso.
	ConnectionRefreshAhead = 10 * time.Minute
	// ConnectionRefreshLease é a reserva da renovação (uma por vez).
	ConnectionRefreshLease = 30 * time.Second
)

// MaxConnectionsPerCompany limita as conexões de uma empresa.
const MaxConnectionsPerCompany = 100

var connectionSlugRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// ValidConnectionSlug informa se s serve de slug de conexão (é também o da integração e o prefixo dos segredos).
func ValidConnectionSlug(s string) bool { return connectionSlugRe.MatchString(s) }

// ConnectionAccessSecret e ConnectionRefreshSecret são os nomes dos segredos que guardam os tokens.
func ConnectionAccessSecret(slug string) string  { return "conn." + slug + ".access" }
func ConnectionRefreshSecret(slug string) string { return "conn." + slug + ".refresh" }

// Connection é a autorização de um servidor MCP remoto. Nunca carrega token, state ou verifier.
type Connection struct {
	ID            ID                 `json:"id"`
	CompanyID     ID                 `json:"company_id"`
	Slug          string             `json:"slug"`
	Name          string             `json:"name"`
	URL           string             `json:"url"`
	Identity      ConnectionIdentity `json:"identity"`
	OwnerUserID   *ID                `json:"owner_user_id,omitempty"`
	Status        ConnectionStatus   `json:"status"`
	IntegrationID *ID                `json:"integration_id,omitempty"`
	Issuer        string             `json:"issuer,omitempty"`
	Scopes        []string           `json:"scopes"`
	// ClientRegistered: há client_id (registro dinâmico feito ou informado).
	ClientRegistered bool       `json:"client_registered"`
	ExpiresAt        *time.Time `json:"expires_at,omitempty"`
	ConnectedAt      *time.Time `json:"connected_at,omitempty"`
	LastRefreshAt    *time.Time `json:"last_refresh_at,omitempty"`
	LastProbeAt      *time.Time `json:"last_probe_at,omitempty"`
	LastProbeStatus  string     `json:"last_probe_status,omitempty"`
	LastError        string     `json:"last_error,omitempty"`
	GateID           *ID        `json:"gate_id,omitempty"`
	AuthExpiresAt    *time.Time `json:"auth_expires_at,omitempty"`
	CreatedBy        Actor      `json:"created_by"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// NeedsRefresh informa se o token vence dentro de window (ou já venceu). Sem validade conhecida, nunca.
func (c Connection) NeedsRefresh(now time.Time, window time.Duration) bool {
	if c.Status != ConnConnected || c.ExpiresAt == nil {
		return false
	}
	return c.ExpiresAt.Before(now.Add(window))
}

// AuthPending informa se há uma autorização em voo e ainda válida.
func (c Connection) AuthPending(now time.Time) bool {
	return c.Status == ConnPendingAuth && c.AuthExpiresAt != nil && c.AuthExpiresAt.After(now)
}

// Setup é o passo seguinte de um setup incompleto, para a tela e a CLI dizerem o que falta ("concluir depois").
type Setup string

const (
	SetupNone      Setup = ""          // nada a fazer
	SetupAuthorize Setup = "authorize" // autorizar no navegador
	SetupFinish    Setup = "finish"    // há uma autorização em voo: concluir no navegador (ou reiniciar se venceu)
	SetupReconnect Setup = "reconnect" // já foi conectada; autorizar de novo
	SetupProbe     Setup = "probe"     // erro transitório: testar de novo
)

// NextSetup diz o que falta para a conexão funcionar.
func (c Connection) NextSetup(now time.Time) Setup {
	switch c.Status {
	case ConnDraft:
		return SetupAuthorize
	case ConnPendingAuth:
		if c.AuthPending(now) {
			return SetupFinish
		}
		return SetupAuthorize
	case ConnExpired, ConnRevoked:
		return SetupReconnect
	case ConnError:
		return SetupProbe
	}
	return SetupNone
}
