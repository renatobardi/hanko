package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/netguard"
	"github.com/renatobardi/hanko/internal/secretbox"
	"github.com/renatobardi/hanko/internal/store"
)

// Integrações (ADR-0029): servidores MCP da empresa e webhooks de saída. Leitura é de qualquer membro (a URL
// completa só aparece para admin); criar, editar, testar, apagar e ligar a personas é de admin. Os segredos
// ficam cifrados em repouso e não voltam por nenhuma leitura.

// IntegrationsConfig é a política e o cofre das integrações, definidos pelo operador da instalação.
type IntegrationsConfig struct {
	// AllowStdio libera servidores MCP stdio (executam um comando na máquina do servidor).
	AllowStdio bool
	// AllowPrivate libera destinos internos (loopback, link-local, redes privadas) nos webhooks e testes.
	AllowPrivate bool
	// Secrets cifra os segredos em repouso; sem chave, nada com segredo pode ser salvo.
	Secrets *secretbox.Box
	// HTTP substitui o cliente de saída (testes). Nil usa o cliente com a proteção SSRF de netguard.
	HTTP *http.Client
}

func (c IntegrationsConfig) policy() netguard.Policy {
	return netguard.Policy{AllowPrivate: c.AllowPrivate}
}

// client é o cliente HTTP de saída.
func (c IntegrationsConfig) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return netguard.NewClient(c.policy(), webhookTimeout)
}

// Tetos das integrações.
const (
	maxIntegrationNameRunes = 120
	maxHeaders              = 20
	maxEnvVars              = 20
	maxHeaderValueBytes     = 4096
	maxStdioArgs            = 50
	maxStdioArgBytes        = 1000
	maxURLBytes             = 2000
	maxSecretBytes          = 512
	secretHintLen           = 10 // "whsec_" + 4
)

var (
	headerNameRe = regexp.MustCompile("^[A-Za-z0-9!#$%&'*+.^_`|~-]{1,64}$")
	envNameRe    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
	// reservedIntegrationSlugs são as chaves do mcpServers que o Hanko usa para si.
	reservedIntegrationSlugs = []string{"hanko", "ai-memory", "memory"}
	// reservedUISlugs são segmentos fixos das rotas da UI que um slug de skill ou integração não pode ocupar.
	reservedUISlugs = []string{"nova", "preview"}
)

// integrationSecrets é o conteúdo cifrado: o que nunca sai do servidor.
type integrationSecrets struct {
	Headers map[string]string `json:"headers,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Secret  string            `json:"secret,omitempty"`
}

func (v integrationSecrets) empty() bool {
	return len(v.Headers) == 0 && len(v.Env) == 0 && v.Secret == ""
}

// values lista todos os valores secretos, para a redação de textos livres.
func (v integrationSecrets) values() []string {
	var out []string
	for _, x := range v.Headers {
		out = append(out, x)
		if rest, ok := strings.CutPrefix(x, "Bearer "); ok {
			out = append(out, rest)
		}
	}
	for _, x := range v.Env {
		out = append(out, x)
	}
	if v.Secret != "" {
		out = append(out, v.Secret)
	}
	return out
}

func (s *Service) seal(id uuid.UUID, v integrationSecrets) (string, error) {
	if v.empty() {
		return "", nil
	}
	if !s.Integrations.Secrets.Enabled() {
		return "", domain.Invalid("secrets", "HANKO_SECRET_KEY não está configurada neste servidor; sem ela não há como guardar segredos")
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("marshal integration secrets: %w", err)
	}
	enc, err := s.Integrations.Secrets.Seal(raw, id.String())
	if err != nil {
		return "", fmt.Errorf("seal integration secrets: %w", err)
	}
	return enc, nil
}

func (s *Service) open(id uuid.UUID, enc string) (integrationSecrets, error) {
	var v integrationSecrets
	if enc == "" {
		return v, nil
	}
	raw, err := s.Integrations.Secrets.Open(enc, id.String())
	if err != nil {
		return v, err
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, fmt.Errorf("integration secrets: %w", err)
	}
	return v, nil
}

func parseIntegrationConfig(raw []byte) domain.IntegrationConfig {
	var c domain.IntegrationConfig
	_ = json.Unmarshal(raw, &c)
	return c
}

func integrationFromRow(r store.Integration, showURL bool) domain.Integration {
	cfg := parseIntegrationConfig(r.Config)
	if !showURL {
		cfg.URL = originOnly(cfg.URL)
		cfg.Args = nil
	}
	ev := r.Events
	if ev == nil {
		ev = []string{}
	}
	return domain.Integration{ID: r.ID, CompanyID: r.CompanyID, Kind: domain.IntegrationKind(r.Kind), Slug: r.Slug, Name: r.Name,
		Enabled: r.Enabled, AllPersonas: r.AllPersonas, Config: cfg, Events: ev, SecretSet: r.SecretEnc != "",
		SecretHint: r.SecretHint, CreatedBy: actorOf(r.CreatedByType, r.CreatedByID), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

// originOnly reduz uma URL a esquema://host: a query e o caminho de um webhook costumam levar o token.
func originOnly(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// IntegrationView é uma integração com o que a listagem mostra. RevealedSecret só vem preenchido na resposta
// de criação/rotação de um webhook cujo segredo o servidor gerou: é a única vez que o valor sai (veja o ADR).
type IntegrationView struct {
	domain.Integration
	LastDelivery   *domain.WebhookDelivery `json:"last_delivery,omitempty"`
	RevealedSecret string                  `json:"-"`
}

// CreateIntegrationInput define uma integração nova.
type CreateIntegrationInput struct {
	Kind    domain.IntegrationKind
	Name    string
	Slug    string // derivado do nome quando vazio
	Enabled *bool  // padrão true
	URL     string // MCP http: endereço do servidor; webhook: destino https

	// MCP.
	Transport   string // http (padrão) | stdio
	Command     string
	Args        []string
	Headers     map[string]string // segredos (ex.: Authorization)
	Env         map[string]string // segredos do stdio
	AllPersonas bool
	PersonaIDs  []domain.ID

	// Webhook.
	Events []string
	Secret string // vazio = o servidor gera e revela uma vez
}

// UpdateIntegrationInput muda uma integração; nil/vazio fica como está. Tipo, transporte e slug são fixos.
type UpdateIntegrationInput struct {
	Name         *string
	Enabled      *bool
	URL          *string
	Command      *string
	Args         *[]string
	SetHeaders   map[string]string // define ou troca estes headers
	DropHeaders  []string
	SetEnv       map[string]string
	DropEnv      []string
	AllPersonas  *bool
	PersonaIDs   *[]domain.ID // troca o conjunto de personas do MCP
	Events       *[]string
	Secret       *string // novo segredo do webhook
	RotateSecret bool    // o servidor gera um novo e o revela uma vez
}

func validateName(name string) (string, error) {
	n := strings.TrimSpace(name)
	switch {
	case n == "":
		return "", domain.Invalid("name", "required")
	case utf8.RuneCountInString(n) > maxIntegrationNameRunes:
		return "", domain.Invalid("name", fmt.Sprintf("at most %d characters", maxIntegrationNameRunes))
	}
	return n, nil
}

func validateHeaders(h map[string]string) error {
	if len(h) > maxHeaders {
		return domain.Invalid("headers", fmt.Sprintf("at most %d headers", maxHeaders))
	}
	for k, v := range h {
		if !headerNameRe.MatchString(k) {
			return domain.Invalid("headers", fmt.Sprintf("invalid header name %q", k))
		}
		if len(v) > maxHeaderValueBytes || strings.ContainsAny(v, "\r\n\x00") {
			return domain.Invalid("headers", fmt.Sprintf("invalid value for header %q", k))
		}
	}
	return nil
}

func validateEnv(e map[string]string) error {
	if len(e) > maxEnvVars {
		return domain.Invalid("env", fmt.Sprintf("at most %d variables", maxEnvVars))
	}
	for k, v := range e {
		if !envNameRe.MatchString(k) {
			return domain.Invalid("env", fmt.Sprintf("invalid variable name %q", k))
		}
		if len(v) > maxHeaderValueBytes || strings.ContainsRune(v, 0) {
			return domain.Invalid("env", fmt.Sprintf("invalid value for variable %q", k))
		}
	}
	return nil
}

func validateStdio(command string, args []string) (string, error) {
	command = strings.TrimSpace(command)
	switch {
	case command == "":
		return "", domain.Invalid("command", "required for stdio")
	case len(command) > maxStdioArgBytes || strings.ContainsAny(command, "\r\n\x00"):
		return "", domain.Invalid("command", "invalid")
	case len(args) > maxStdioArgs:
		return "", domain.Invalid("args", fmt.Sprintf("at most %d arguments", maxStdioArgs))
	}
	for _, a := range args {
		if len(a) > maxStdioArgBytes || strings.ContainsRune(a, 0) {
			return "", domain.Invalid("args", "invalid argument")
		}
	}
	return command, nil
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func validateEvents(ev []string) ([]string, error) {
	if len(ev) == 0 {
		return nil, domain.Invalid("events", "choose at least one event")
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(ev))
	for _, e := range ev {
		e = strings.TrimSpace(e)
		if !domain.ValidWebhookEvent(e) {
			return nil, domain.Invalid("events", fmt.Sprintf("unknown event %q", e))
		}
		if !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	slices.Sort(out)
	return out, nil
}

// webhookURL valida o destino de um webhook: https, sem credenciais, e sem destino interno (a menos que o
// operador libere). A verificação de verdade ocorre de novo na conexão (netguard).
func (s *Service) webhookURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) > maxURLBytes {
		return "", domain.Invalid("url", "too long")
	}
	u, err := netguard.ValidateURL(raw, false, s.Integrations.policy())
	if err != nil {
		return "", domain.Invalid("url", err.Error())
	}
	return u.String(), nil
}

// mcpURL valida o endereço de um servidor MCP http. Quem conecta é o agente, então destino interno é
// permitido aqui; o teste de conexão (feito pelo servidor) passa pela proteção SSRF.
func mcpURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) > maxURLBytes {
		return "", domain.Invalid("url", "too long")
	}
	u, err := netguard.ValidateURL(raw, true, netguard.Policy{AllowPrivate: true})
	if err != nil {
		return "", domain.Invalid("url", err.Error())
	}
	return u.String(), nil
}

// newWebhookSecret gera um segredo de webhook e o prefixo que a UI pode mostrar.
func newWebhookSecret() (secret, hint string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("gerar segredo: %w", err)
	}
	secret = "whsec_" + base64.RawURLEncoding.EncodeToString(b)
	return secret, secret[:secretHintLen], nil
}

func (t *txc) checkPersonasInCompany(companyID domain.ID, ids []domain.ID) error {
	for _, id := range ids {
		if _, err := t.q.GetPersonaByID(t.ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: id}); err != nil {
			return domain.Invalid("personas", "persona not found in company")
		}
	}
	return nil
}

// checkIntegrationSlug recusa os slugs que a criação e a importação não podem aceitar: segmentos fixos das
// rotas da UI e, no MCP, as chaves do mcpServers que o Hanko usa para si.
func checkIntegrationSlug(kind domain.IntegrationKind, slug string) error {
	if slices.Contains(reservedUISlugs, slug) {
		return domain.Invalid("slug", fmt.Sprintf("%q is reserved", slug))
	}
	if kind == domain.IntegrationMCP && slices.Contains(reservedIntegrationSlugs, slug) {
		return domain.Invalid("slug", fmt.Sprintf("%q is reserved", slug))
	}
	return nil
}

// checkStdioAllowed é o interruptor do operador (HANKO_INTEGRATIONS_ALLOW_STDIO) para MCP stdio.
func (s *Service) checkStdioAllowed() error {
	if !s.Integrations.AllowStdio {
		return domain.Invalid("transport", "stdio está desligado neste servidor (HANKO_INTEGRATIONS_ALLOW_STDIO)")
	}
	return nil
}

// CreateIntegration cria uma integração. A resposta nunca traz segredo, exceto o segredo de webhook gerado
// pelo servidor (RevealedSecret), mostrado só agora.
func (s *Service) CreateIntegration(ctx context.Context, actor domain.Actor, companyID domain.ID, in CreateIntegrationInput) (IntegrationView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return IntegrationView{}, err
	}
	if !in.Kind.Valid() {
		return IntegrationView{}, domain.Invalid("kind", "must be mcp or webhook")
	}
	name, err := validateName(in.Name)
	if err != nil {
		return IntegrationView{}, err
	}
	slug, err := resolveSlug(in.Slug, name)
	if err != nil {
		return IntegrationView{}, err
	}
	if err := checkIntegrationSlug(in.Kind, slug); err != nil {
		return IntegrationView{}, err
	}
	enabled := in.Enabled == nil || *in.Enabled

	id := domain.NewID()
	cfg := domain.IntegrationConfig{}
	var sec integrationSecrets
	var events []string
	var revealed, hint string
	switch in.Kind {
	case domain.IntegrationMCP:
		cfg.Transport = strings.ToLower(strings.TrimSpace(in.Transport))
		if cfg.Transport == "" {
			cfg.Transport = domain.TransportHTTP
		}
		switch cfg.Transport {
		case domain.TransportHTTP:
			if cfg.URL, err = mcpURL(in.URL); err != nil {
				return IntegrationView{}, err
			}
			if err := validateHeaders(in.Headers); err != nil {
				return IntegrationView{}, err
			}
			if len(in.Env) > 0 || in.Command != "" {
				return IntegrationView{}, domain.Invalid("transport", "command and env only apply to stdio")
			}
			sec.Headers = in.Headers
			cfg.HeaderNames = sortedKeys(in.Headers)
		case domain.TransportStdio:
			if err := s.checkStdioAllowed(); err != nil {
				return IntegrationView{}, err
			}
			if cfg.Command, err = validateStdio(in.Command, in.Args); err != nil {
				return IntegrationView{}, err
			}
			if err := validateEnv(in.Env); err != nil {
				return IntegrationView{}, err
			}
			if in.URL != "" || len(in.Headers) > 0 {
				return IntegrationView{}, domain.Invalid("transport", "url and headers only apply to http")
			}
			cfg.Args = in.Args
			sec.Env = in.Env
			cfg.EnvNames = sortedKeys(in.Env)
		default:
			return IntegrationView{}, domain.Invalid("transport", "must be http or stdio")
		}
		if len(in.Events) > 0 || in.Secret != "" {
			return IntegrationView{}, domain.Invalid("events", "events and secret only apply to webhooks")
		}
	case domain.IntegrationWebhook:
		if cfg.URL, err = s.webhookURL(in.URL); err != nil {
			return IntegrationView{}, err
		}
		if events, err = validateEvents(in.Events); err != nil {
			return IntegrationView{}, err
		}
		if in.Secret != "" {
			if len(in.Secret) < 16 || len(in.Secret) > maxSecretBytes {
				return IntegrationView{}, domain.Invalid("secret", fmt.Sprintf("use 16 to %d characters", maxSecretBytes))
			}
			sec.Secret = in.Secret
		} else {
			if sec.Secret, hint, err = newWebhookSecret(); err != nil {
				return IntegrationView{}, err
			}
			revealed = sec.Secret
		}
		if in.AllPersonas || len(in.PersonaIDs) > 0 || len(in.Headers) > 0 || len(in.Env) > 0 || in.Command != "" {
			return IntegrationView{}, domain.Invalid("kind", "personas, headers and command only apply to MCP servers")
		}
	}
	enc, err := s.seal(id, sec)
	if err != nil {
		return IntegrationView{}, err
	}
	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		return IntegrationView{}, fmt.Errorf("marshal integration config: %w", err)
	}

	var out IntegrationView
	err = s.withTx(ctx, func(t *txc) error {
		if err := t.checkPersonasInCompany(companyID, in.PersonaIDs); err != nil {
			return err
		}
		row, err := t.q.CreateIntegration(ctx, store.CreateIntegrationParams{ID: id, CompanyID: companyID, Kind: string(in.Kind),
			Slug: slug, Name: name, Enabled: enabled, AllPersonas: in.AllPersonas, Config: cfgJSON, Events: nonNil(events),
			SecretEnc: enc, SecretHint: hint, ActorType: string(actor.Type), ActorID: actor.ID, Now: t.now})
		if err != nil {
			return mapErr(err, "create integration")
		}
		for _, pid := range in.PersonaIDs {
			if err := t.q.SetIntegrationPersona(ctx, store.SetIntegrationPersonaParams{CompanyID: companyID, IntegrationID: id, PersonaID: pid}); err != nil {
				return fmt.Errorf("link integration persona: %w", err)
			}
		}
		out = IntegrationView{Integration: integrationFromRow(row, true), RevealedSecret: revealed}
		return t.activity(actor, companyID, "integration.created", "integration", id,
			map[string]any{"slug": row.Slug, "name": row.Name, "kind": row.Kind}, "")
	})
	return out, err
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// UpdateIntegration muda uma integração. Os valores de headers/variáveis/segredo só entram (nunca saem).
func (s *Service) UpdateIntegration(ctx context.Context, actor domain.Actor, companyID, id domain.ID, in UpdateIntegrationInput) (IntegrationView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return IntegrationView{}, err
	}
	var out IntegrationView
	err := s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetIntegrationByIDForUpdate(ctx, store.GetIntegrationByIDForUpdateParams{CompanyID: companyID, ID: id})
		if err != nil {
			return mapErr(err, "get integration")
		}
		kind := domain.IntegrationKind(cur.Kind)
		cfg := parseIntegrationConfig(cur.Config)
		p := store.UpdateIntegrationParams{CompanyID: companyID, ID: id, Name: cur.Name, Enabled: cur.Enabled,
			AllPersonas: cur.AllPersonas, Events: cur.Events, SecretEnc: cur.SecretEnc, SecretHint: cur.SecretHint, Now: t.now}
		var changed []string
		var revealed string

		if in.Name != nil {
			n, err := validateName(*in.Name)
			if err != nil {
				return err
			}
			if n != p.Name {
				p.Name = n
				changed = append(changed, "name")
			}
		}
		if in.Enabled != nil && *in.Enabled != p.Enabled {
			p.Enabled = *in.Enabled
			changed = append(changed, "enabled")
		}

		var sec integrationSecrets
		secChanged := false
		loadSec := func() error {
			if secChanged {
				return nil
			}
			v, err := s.open(id, cur.SecretEnc)
			if err != nil {
				return domain.Invalid("secrets", "não foi possível ler os segredos guardados: "+err.Error())
			}
			sec, secChanged = v, true
			return nil
		}

		switch kind {
		case domain.IntegrationMCP:
			if in.URL != nil {
				if cfg.Transport != domain.TransportHTTP {
					return domain.Invalid("url", "only applies to http servers")
				}
				u, err := mcpURL(*in.URL)
				if err != nil {
					return err
				}
				if u != cfg.URL {
					cfg.URL = u
					changed = append(changed, "url")
				}
			}
			if in.Command != nil || in.Args != nil {
				if cfg.Transport != domain.TransportStdio {
					return domain.Invalid("command", "only applies to stdio servers")
				}
				cmd, args := cfg.Command, cfg.Args
				if in.Command != nil {
					cmd = *in.Command
				}
				if in.Args != nil {
					args = *in.Args
				}
				if cmd, err = validateStdio(cmd, args); err != nil {
					return err
				}
				cfg.Command, cfg.Args = cmd, args
				changed = append(changed, "command")
			}
			if len(in.SetHeaders)+len(in.DropHeaders) > 0 {
				if cfg.Transport != domain.TransportHTTP {
					return domain.Invalid("headers", "only applies to http servers")
				}
				if err := validateHeaders(in.SetHeaders); err != nil {
					return err
				}
				if err := loadSec(); err != nil {
					return err
				}
				if sec.Headers == nil {
					sec.Headers = map[string]string{}
				}
				for _, d := range in.DropHeaders {
					delete(sec.Headers, d)
				}
				for k, v := range in.SetHeaders {
					sec.Headers[k] = v
				}
				if err := validateHeaders(sec.Headers); err != nil {
					return err
				}
				cfg.HeaderNames = sortedKeys(sec.Headers)
				changed = append(changed, "headers")
			}
			if len(in.SetEnv)+len(in.DropEnv) > 0 {
				if cfg.Transport != domain.TransportStdio {
					return domain.Invalid("env", "only applies to stdio servers")
				}
				if err := validateEnv(in.SetEnv); err != nil {
					return err
				}
				if err := loadSec(); err != nil {
					return err
				}
				if sec.Env == nil {
					sec.Env = map[string]string{}
				}
				for _, d := range in.DropEnv {
					delete(sec.Env, d)
				}
				for k, v := range in.SetEnv {
					sec.Env[k] = v
				}
				if err := validateEnv(sec.Env); err != nil {
					return err
				}
				cfg.EnvNames = sortedKeys(sec.Env)
				changed = append(changed, "env")
			}
			if in.AllPersonas != nil && *in.AllPersonas != p.AllPersonas {
				p.AllPersonas = *in.AllPersonas
				changed = append(changed, "all_personas")
			}
			if in.PersonaIDs != nil {
				if err := t.checkPersonasInCompany(companyID, *in.PersonaIDs); err != nil {
					return err
				}
				if err := t.q.ClearIntegrationPersonas(ctx, id); err != nil {
					return fmt.Errorf("clear integration personas: %w", err)
				}
				for _, pid := range *in.PersonaIDs {
					if err := t.q.SetIntegrationPersona(ctx, store.SetIntegrationPersonaParams{CompanyID: companyID, IntegrationID: id, PersonaID: pid}); err != nil {
						return fmt.Errorf("link integration persona: %w", err)
					}
				}
				changed = append(changed, "personas")
			}
			if in.Events != nil || in.Secret != nil || in.RotateSecret {
				return domain.Invalid("events", "events and secret only apply to webhooks")
			}
		case domain.IntegrationWebhook:
			if in.URL != nil {
				u, err := s.webhookURL(*in.URL)
				if err != nil {
					return err
				}
				if u != cfg.URL {
					cfg.URL = u
					changed = append(changed, "url")
				}
			}
			if in.Events != nil {
				ev, err := validateEvents(*in.Events)
				if err != nil {
					return err
				}
				if !slices.Equal(ev, p.Events) {
					p.Events = ev
					changed = append(changed, "events")
				}
			}
			switch {
			case in.RotateSecret && in.Secret != nil:
				return domain.Invalid("secret", "choose between informing a secret and generating one")
			case in.RotateSecret:
				v, hint, err := newWebhookSecret()
				if err != nil {
					return err
				}
				sec, secChanged, revealed, p.SecretHint = integrationSecrets{Secret: v}, true, v, hint
				changed = append(changed, "secret")
			case in.Secret != nil:
				if len(*in.Secret) < 16 || len(*in.Secret) > maxSecretBytes {
					return domain.Invalid("secret", fmt.Sprintf("use 16 to %d characters", maxSecretBytes))
				}
				sec, secChanged, p.SecretHint = integrationSecrets{Secret: *in.Secret}, true, ""
				changed = append(changed, "secret")
			}
			if in.AllPersonas != nil || in.PersonaIDs != nil || len(in.SetHeaders)+len(in.DropHeaders)+len(in.SetEnv)+len(in.DropEnv) > 0 || in.Command != nil || in.Args != nil {
				return domain.Invalid("kind", "personas, headers and command only apply to MCP servers")
			}
			if p.Enabled && cur.SecretEnc == "" && !secChanged {
				return domain.Invalid("enabled", "defina o segredo antes de ativar o webhook")
			}
		}
		if len(changed) == 0 {
			out = IntegrationView{Integration: integrationFromRow(cur, true)}
			return nil
		}
		if secChanged {
			if p.SecretEnc, err = s.seal(id, sec); err != nil {
				return err
			}
		}
		if p.Config, err = json.Marshal(cfg); err != nil {
			return fmt.Errorf("marshal integration config: %w", err)
		}
		row, err := t.q.UpdateIntegration(ctx, p)
		if err != nil {
			return mapErr(err, "update integration")
		}
		out = IntegrationView{Integration: integrationFromRow(row, true), RevealedSecret: revealed}
		return t.activity(actor, companyID, "integration.updated", "integration", id,
			map[string]any{"slug": row.Slug, "fields": changed}, "")
	})
	return out, err
}

// DeleteIntegration apaga a integração (e suas entregas e ligações).
func (s *Service) DeleteIntegration(ctx context.Context, actor domain.Actor, companyID, id domain.ID) error {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return err
	}
	return s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetIntegrationByID(ctx, store.GetIntegrationByIDParams{CompanyID: companyID, ID: id})
		if err != nil {
			return mapErr(err, "get integration")
		}
		if _, err := t.q.DeleteIntegration(ctx, store.DeleteIntegrationParams{CompanyID: companyID, ID: id}); err != nil {
			return fmt.Errorf("delete integration: %w", err)
		}
		return t.activity(actor, companyID, "integration.deleted", "integration", id,
			map[string]any{"slug": cur.Slug, "name": cur.Name, "kind": cur.Kind}, "")
	})
}

// canSeeURL: a URL completa (que pode carregar token na query) é de admin.
func (s *Service) canSeeURL(ctx context.Context, companyID domain.ID) bool {
	role, restricted, err := s.callerRole(ctx, companyID)
	return err == nil && (!restricted || role.AtLeast(domain.MemberAdmin))
}

// ListIntegrations devolve as integrações da empresa com a última entrega de cada webhook.
func (s *Service) ListIntegrations(ctx context.Context, companyID domain.ID) ([]IntegrationView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	show := s.canSeeURL(ctx, companyID)
	q := s.read()
	rows, err := q.ListIntegrations(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list integrations: %w", err)
	}
	last, err := q.LastWebhookDeliveryByIntegration(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("last deliveries: %w", err)
	}
	byID := make(map[uuid.UUID]domain.WebhookDelivery, len(last))
	for _, l := range last {
		d := domain.WebhookDelivery{IntegrationID: l.IntegrationID, Status: l.Status, CreatedAt: l.CreatedAt}
		if l.HttpStatus != nil {
			c := int(*l.HttpStatus)
			d.HTTPStatus = &c
		}
		byID[l.IntegrationID] = d
	}
	out := make([]IntegrationView, len(rows))
	for i, r := range rows {
		out[i] = IntegrationView{Integration: integrationFromRow(r, show)}
		if d, ok := byID[r.ID]; ok {
			dd := d
			out[i].LastDelivery = &dd
		}
	}
	return out, nil
}

// GetIntegration resolve ref como UUID ou slug dentro da empresa.
func (s *Service) GetIntegration(ctx context.Context, companyID domain.ID, ref string) (IntegrationView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return IntegrationView{}, err
	}
	q := s.read()
	var r store.Integration
	var err error
	if id, perr := uuid.Parse(ref); perr == nil {
		r, err = q.GetIntegrationByID(ctx, store.GetIntegrationByIDParams{CompanyID: companyID, ID: id})
	} else {
		r, err = q.GetIntegrationBySlug(ctx, store.GetIntegrationBySlugParams{CompanyID: companyID, Slug: ref})
	}
	if err != nil {
		return IntegrationView{}, mapErr(err, "get integration")
	}
	return IntegrationView{Integration: integrationFromRow(r, s.canSeeURL(ctx, companyID))}, nil
}

// ListIntegrationPersonas devolve as personas da empresa com o estado para um servidor MCP.
func (s *Service) ListIntegrationPersonas(ctx context.Context, companyID, id domain.ID) ([]domain.IntegrationPersona, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	q := s.read()
	if _, err := q.GetIntegrationByID(ctx, store.GetIntegrationByIDParams{CompanyID: companyID, ID: id}); err != nil {
		return nil, mapErr(err, "get integration")
	}
	rows, err := q.ListIntegrationPersonaLinks(ctx, store.ListIntegrationPersonaLinksParams{CompanyID: companyID, IntegrationID: id})
	if err != nil {
		return nil, fmt.Errorf("list integration personas: %w", err)
	}
	out := make([]domain.IntegrationPersona, len(rows))
	for i, r := range rows {
		out[i] = domain.IntegrationPersona{PersonaID: r.PersonaID, Slug: r.Slug, Name: r.Name, Enabled: r.Enabled}
	}
	return out, nil
}

// PersonaIntegration é o estado de um servidor MCP para uma persona.
type PersonaIntegration struct {
	IntegrationID domain.ID
	Slug, Name    string
	Enabled       bool // a integração está ligada
	AllPersonas   bool // vale para todas
	Linked        bool // ligada especificamente a esta persona
}

// ListPersonaIntegrations devolve os servidores MCP da empresa com o estado para a persona.
func (s *Service) ListPersonaIntegrations(ctx context.Context, companyID, personaID domain.ID) ([]PersonaIntegration, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	rows, err := s.read().ListPersonaIntegrationLinks(ctx, store.ListPersonaIntegrationLinksParams{CompanyID: companyID, PersonaID: personaID})
	if err != nil {
		return nil, fmt.Errorf("list persona integrations: %w", err)
	}
	out := make([]PersonaIntegration, len(rows))
	for i, r := range rows {
		out[i] = PersonaIntegration{IntegrationID: r.IntegrationID, Slug: r.Slug, Name: r.Name, Enabled: r.IntegrationEnabled,
			AllPersonas: r.AllPersonas, Linked: r.Linked}
	}
	return out, nil
}

// SetPersonaIntegration liga ou desliga um servidor MCP para uma persona (admin).
func (s *Service) SetPersonaIntegration(ctx context.Context, actor domain.Actor, companyID, personaID, integrationID domain.ID, on bool) error {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return err
	}
	return s.withTx(ctx, func(t *txc) error {
		p, err := t.q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: personaID})
		if err != nil {
			return mapErr(err, "get persona")
		}
		it, err := t.q.GetIntegrationByID(ctx, store.GetIntegrationByIDParams{CompanyID: companyID, ID: integrationID})
		if err != nil {
			return mapErr(err, "get integration")
		}
		if domain.IntegrationKind(it.Kind) != domain.IntegrationMCP {
			return domain.Invalid("integration", "only MCP servers are linked to personas")
		}
		if on {
			err = t.q.SetIntegrationPersona(ctx, store.SetIntegrationPersonaParams{CompanyID: companyID, IntegrationID: integrationID, PersonaID: personaID})
		} else {
			err = t.q.DeleteIntegrationPersona(ctx, store.DeleteIntegrationPersonaParams{IntegrationID: integrationID, PersonaID: personaID})
		}
		if err != nil {
			return fmt.Errorf("link integration persona: %w", err)
		}
		verb := "persona.integration_disabled"
		if on {
			verb = "persona.integration_enabled"
		}
		return t.activity(actor, companyID, verb, "persona", p.ID, map[string]any{"integration": it.Slug, "persona": p.Slug}, "")
	})
}

// RekeyIntegrationSecrets reescreve com a chave atual os segredos cifrados com a anterior (rotação de
// HANKO_SECRET_KEY). É da instalação, não de uma empresa: exige o dono da instalação. Devolve quantos reescreveu.
func (s *Service) RekeyIntegrationSecrets(ctx context.Context) (int, error) {
	if id, ok := auth.IdentityFrom(ctx); ok && id.User.Role != domain.RoleOwner {
		return 0, domain.ErrForbidden
	}
	if !s.Integrations.Secrets.Enabled() {
		return 0, domain.Invalid("secrets", "HANKO_SECRET_KEY não está configurada")
	}
	rows, err := s.read().ListAllSecretIntegrations(ctx)
	if err != nil {
		return 0, fmt.Errorf("list secret integrations: %w", err)
	}
	n := 0
	for _, r := range rows {
		if !s.Integrations.Secrets.NeedsReseal(r.SecretEnc) {
			continue
		}
		plain, err := s.Integrations.Secrets.Open(r.SecretEnc, r.ID.String())
		if err != nil {
			return n, fmt.Errorf("integração %s: %w", r.ID, err)
		}
		enc, err := s.Integrations.Secrets.Seal(plain, r.ID.String())
		if err != nil {
			return n, fmt.Errorf("integração %s: %w", r.ID, err)
		}
		if err := store.New(s.Pool).SetIntegrationSecretEnc(ctx, store.SetIntegrationSecretEncParams{ID: r.ID, SecretEnc: enc}); err != nil {
			return n, fmt.Errorf("integração %s: %w", r.ID, err)
		}
		n++
	}
	return n, nil
}

// redactSecrets mascara em texto livre os segredos da integração e os padrões conhecidos de credencial.
func redactSecrets(text string, v integrationSecrets) string {
	return adapter.Redact(text, v.values()...)
}
