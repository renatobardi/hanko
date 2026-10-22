package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Canais externos (ADR-0046): Telegram e GitHub. Tipos e regras puras; o protocolo de cada um mora em internal/channel.

// ChannelKind é o driver de um canal.
type ChannelKind string

// Drivers.
const (
	ChannelTelegram ChannelKind = "telegram"
	ChannelGitHub   ChannelKind = "github"
)

// Valid diz se k é um driver conhecido.
func (k ChannelKind) Valid() bool { return k == ChannelTelegram || k == ChannelGitHub }

// Label é o nome do driver para telas e mensagens.
func (k ChannelKind) Label() string {
	switch k {
	case ChannelTelegram:
		return "Telegram"
	case ChannelGitHub:
		return "GitHub"
	}
	return string(k)
}

// Limites dos canais.
const (
	MaxChannelName       = 80
	MaxChannelRepos      = 50
	MaxChannelInboundLen = 8000 // runas de uma mensagem de entrada que seguem adiante
	ChannelLinkTTL       = 15 * time.Minute
)

// ChannelConfig é a configuração sem segredo de um canal.
type ChannelConfig struct {
	// APIBase substitui a raiz da API do provedor (espelho, GitHub Enterprise, testes). Vazio = a pública.
	APIBase string `json:"api_base,omitempty"`
	// Repos (GitHub): "dono/repo" ou "dono/*". Só eles acordam a persona.
	Repos []string `json:"repos,omitempty"`
	// Mention (GitHub): a citação que acorda a persona; padrão "@hanko".
	Mention string `json:"mention,omitempty"`
}

// DefaultGitHubMention é a citação padrão do canal GitHub.
const DefaultGitHubMention = "@hanko"

var (
	repoPatternRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}/([A-Za-z0-9_.-]{1,100}|\*)$`)
	mentionRE     = regexp.MustCompile(`^@[A-Za-z0-9][A-Za-z0-9_-]{0,38}$`)
)

// NormalizeChannelConfig valida e limpa a configuração conforme o driver.
func NormalizeChannelConfig(kind ChannelKind, c ChannelConfig) (ChannelConfig, error) {
	c.APIBase = strings.TrimRight(strings.TrimSpace(c.APIBase), "/")
	if c.APIBase != "" {
		u, err := url.Parse(c.APIBase)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || len(c.APIBase) > 300 {
			return c, Invalid("api_base", "use uma URL http(s) sem credenciais")
		}
	}
	switch kind {
	case ChannelTelegram:
		if len(c.Repos) > 0 || c.Mention != "" {
			return c, Invalid("config", "repos e mention só valem para o GitHub")
		}
		return c, nil
	case ChannelGitHub:
		if len(c.Repos) == 0 {
			return c, Invalid("repos", "informe ao menos um repositório (dono/repo ou dono/*)")
		}
		if len(c.Repos) > MaxChannelRepos {
			return c, Invalid("repos", fmt.Sprintf("no máximo %d repositórios", MaxChannelRepos))
		}
		seen := map[string]bool{}
		repos := make([]string, 0, len(c.Repos))
		for _, r := range c.Repos {
			r = strings.TrimSpace(r)
			if !repoPatternRE.MatchString(r) {
				return c, Invalid("repos", fmt.Sprintf("%q não é dono/repo nem dono/*", r))
			}
			if k := strings.ToLower(r); !seen[k] {
				seen[k] = true
				repos = append(repos, r)
			}
		}
		c.Repos = repos
		c.Mention = strings.TrimSpace(c.Mention)
		if c.Mention == "" {
			c.Mention = DefaultGitHubMention
		}
		if !mentionRE.MatchString(c.Mention) {
			return c, Invalid("mention", "use @nome, com letras, números, - e _")
		}
		return c, nil
	}
	return c, Invalid("kind", "use telegram ou github")
}

// RepoAllowed diz se "dono/repo" está na lista (comparação sem diferenciar maiúsculas; "dono/*" libera o dono).
func RepoAllowed(repos []string, full string) bool {
	full = strings.ToLower(strings.TrimSpace(full))
	owner, _, _ := strings.Cut(full, "/")
	for _, r := range repos {
		r = strings.ToLower(r)
		if r == full || r == owner+"/*" {
			return true
		}
	}
	return false
}

// ValidChannelName confere o nome de exibição.
func ValidChannelName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > MaxChannelName {
		return "", Invalid("name", fmt.Sprintf("1 a %d caracteres", MaxChannelName))
	}
	return name, nil
}

// Channel é um canal externo de uma empresa.
type Channel struct {
	ID               ID            `json:"id"`
	CompanyID        ID            `json:"company_id"`
	Kind             ChannelKind   `json:"kind"`
	Name             string        `json:"name"`
	Enabled          bool          `json:"enabled"`
	PersonaID        *ID           `json:"persona_id,omitempty"`
	ProjectID        *ID           `json:"project_id,omitempty"`
	TokenRef         string        `json:"token_ref,omitempty"`          // ${secret:NOME} do token do bot/API
	WebhookSecretRef string        `json:"webhook_secret_ref,omitempty"` // ${secret:NOME} que autentica a entrada
	Config           ChannelConfig `json:"config"`
	InboundToken     string        `json:"inbound_token"`
	CreatedAt        time.Time     `json:"created_at"`
	UpdatedAt        time.Time     `json:"updated_at"`
}

// ChannelHookPath é o caminho (relativo à URL pública) onde o provedor entrega os eventos do canal.
func (c Channel) ChannelHookPath() string { return "/hooks/channels/" + c.InboundToken }

// IdentityStatus é o estado de um vínculo de identidade.
type IdentityStatus string

// Estados do vínculo.
const (
	IdentityPending IdentityStatus = "pending"
	IdentityLinked  IdentityStatus = "linked"
	IdentityRevoked IdentityStatus = "revoked"
)

// ChannelIdentity liga uma pessoa do canal a um usuário do Hanko.
type ChannelIdentity struct {
	ID            ID             `json:"id"`
	CompanyID     ID             `json:"company_id"`
	ChannelID     ID             `json:"channel_id"`
	UserID        ID             `json:"user_id"`
	UserEmail     string         `json:"user_email,omitempty"`
	UserName      string         `json:"user_name,omitempty"`
	ExternalID    string         `json:"external_id,omitempty"`
	ExternalLabel string         `json:"external_label,omitempty"`
	Status        IdentityStatus `json:"status"`
	CodeExpiresAt *time.Time     `json:"code_expires_at,omitempty"`
	LinkedAt      *time.Time     `json:"linked_at,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
}

// MessageDirection é o sentido de uma mensagem de canal.
type MessageDirection string

// Sentidos.
const (
	MessageIn  MessageDirection = "in"
	MessageOut MessageDirection = "out"
)

// ChannelMessageStatus é o estado de uma mensagem (entrada: received → processed | ignored | failed; saída: pending → sent | failed).
type ChannelMessageStatus string

// Estados de mensagem.
const (
	MsgReceived  ChannelMessageStatus = "received"
	MsgProcessed ChannelMessageStatus = "processed"
	MsgIgnored   ChannelMessageStatus = "ignored"
	MsgPending   ChannelMessageStatus = "pending"
	MsgSent      ChannelMessageStatus = "sent"
	MsgFailed    ChannelMessageStatus = "failed"
)

// ChannelMessage é uma mensagem que entrou ou saiu por um canal, com o estado de entrega.
type ChannelMessage struct {
	ID            ID                   `json:"id"`
	CompanyID     ID                   `json:"company_id"`
	ChannelID     ID                   `json:"channel_id"`
	Direction     MessageDirection     `json:"direction"`
	Kind          string               `json:"kind"`
	Peer          string               `json:"peer,omitempty"`
	Body          string               `json:"body"`
	SubjectType   string               `json:"subject_type,omitempty"`
	SubjectID     *ID                  `json:"subject_id,omitempty"`
	Status        ChannelMessageStatus `json:"status"`
	StatusReason  string               `json:"status_reason,omitempty"`
	Attempts      int                  `json:"attempts"`
	LastError     string               `json:"last_error,omitempty"`
	ProviderMsgID string               `json:"provider_msg_id,omitempty"`
	CreatedAt     time.Time            `json:"created_at"`
	SentAt        *time.Time           `json:"sent_at,omitempty"`
}

// ---- código de vínculo ----

// linkAlphabet não tem 0/O/1/I/L: o código é digitado à mão.
const linkAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// FormatLinkCode agrupa o código bruto de 8 caracteres em XXXX-XXXX.
func FormatLinkCode(raw string) string {
	if len(raw) != 8 {
		return raw
	}
	return raw[:4] + "-" + raw[4:]
}

// LinkCodeFromBytes monta o código (8 caracteres do alfabeto) a partir de bytes aleatórios; devolve "" se faltar byte.
func LinkCodeFromBytes(b []byte) string {
	if len(b) < 8 {
		return ""
	}
	out := make([]byte, 8)
	for i := range out {
		out[i] = linkAlphabet[int(b[i])%len(linkAlphabet)]
	}
	return string(out)
}

// NormalizeLinkCode aceita o código com hífen, espaços e em qualquer caixa; "" se não for um código possível.
func NormalizeLinkCode(s string) string {
	s = strings.ToUpper(strings.NewReplacer("-", "", " ", "", "\t", "").Replace(strings.TrimSpace(s)))
	if len(s) != 8 {
		return ""
	}
	for _, c := range s {
		if !strings.ContainsRune(linkAlphabet, c) {
			return ""
		}
	}
	return s
}

// HashLinkCode é o que o banco guarda do código.
func HashLinkCode(normalized string) string {
	sum := sha256.Sum256([]byte("hanko-channel-link:" + normalized))
	return hex.EncodeToString(sum[:])
}

// ClipInbound corta o texto que entra por um canal.
func ClipInbound(s string) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= MaxChannelInboundLen {
		return s
	}
	return string([]rune(s)[:MaxChannelInboundLen])
}

// ---- review do GitHub ----

// GitHubReviewEvent é o veredito de uma review publicada.
type GitHubReviewEvent string

// Vereditos.
const (
	GHReviewComment        GitHubReviewEvent = "COMMENT"
	GHReviewApprove        GitHubReviewEvent = "APPROVE"
	GHReviewRequestChanges GitHubReviewEvent = "REQUEST_CHANGES"
)

// Valid diz se e é um veredito conhecido.
func (e GitHubReviewEvent) Valid() bool {
	return e == GHReviewComment || e == GHReviewApprove || e == GHReviewRequestChanges
}

var peerRE = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9-]{0,38})/([A-Za-z0-9_.-]{1,100})#([0-9]{1,9})$`)

// ParseGitHubPeer lê "dono/repo#N".
func ParseGitHubPeer(peer string) (repo string, number int, ok bool) {
	m := peerRE.FindStringSubmatch(strings.TrimSpace(peer))
	if m == nil {
		return "", 0, false
	}
	n := 0
	for _, c := range m[3] {
		n = n*10 + int(c-'0')
	}
	if n == 0 {
		return "", 0, false
	}
	return m[1] + "/" + m[2], n, true
}
