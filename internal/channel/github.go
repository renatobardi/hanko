package channel

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/renatobardi/hanko/internal/domain"
)

// GitHub é o driver do GitHub: a entrada são os webhooks do repositório (comentários em PR e issue, assinados com
// X-Hub-Signature-256); a saída é um comentário ou uma review pela API REST com o token do canal.
type GitHub struct{}

// DefaultGitHubBase é a raiz da API REST.
const DefaultGitHubBase = "https://api.github.com"

const githubMaxBody = 60000 // o GitHub aceita 65536; sobra para o rodapé

// Kind implementa Driver.
func (GitHub) Kind() domain.ChannelKind { return domain.ChannelGitHub }

func (GitHub) base(c Client) string {
	if c.Config.APIBase != "" {
		return c.Config.APIBase
	}
	return DefaultGitHubBase
}

// Verify confere X-Hub-Signature-256 (HMAC-SHA256 do corpo) em tempo constante.
func (GitHub) Verify(h http.Header, body []byte, secret string) error {
	got := strings.TrimPrefix(h.Get("X-Hub-Signature-256"), "sha256=")
	if secret == "" || got == "" {
		return ErrUnauthorized
	}
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	want := hex.EncodeToString(m.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(strings.ToLower(got))) {
		return ErrUnauthorized
	}
	return nil
}

type ghUser struct {
	Login string `json:"login"`
	Type  string `json:"type"`
}

type ghThing struct {
	Number      int              `json:"number"`
	Title       string           `json:"title"`
	HTMLURL     string           `json:"html_url"`
	Body        string           `json:"body"`
	PullRequest *json.RawMessage `json:"pull_request"`
	User        ghUser           `json:"user"`
}

type ghPayload struct {
	Action      string   `json:"action"`
	Issue       *ghThing `json:"issue"`
	PullRequest *ghThing `json:"pull_request"`
	Comment     *struct {
		ID      int64  `json:"id"`
		Body    string `json:"body"`
		HTMLURL string `json:"html_url"`
		User    ghUser `json:"user"`
	} `json:"comment"`
	Review *struct {
		ID      int64  `json:"id"`
		Body    string `json:"body"`
		HTMLURL string `json:"html_url"`
		User    ghUser `json:"user"`
	} `json:"review"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Sender ghUser `json:"sender"`
}

// mentionRegexp casa a citação como palavra (não dentro de um e-mail nem de outro nome).
func mentionRegexp(mention string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)(^|[^A-Za-z0-9_@-])` + regexp.QuoteMeta(mention) + `($|[^A-Za-z0-9_-])`)
}

// Parse normaliza um evento do GitHub. Só vira InText/InCommand o que cita a menção do canal (ou começa com /hanko), num
// repositório da lista, escrito por uma pessoa (não um bot).
func (GitHub) Parse(h http.Header, body []byte, cfg domain.ChannelConfig) ([]Inbound, error) {
	event := h.Get("X-GitHub-Event")
	delivery := h.Get("X-GitHub-Delivery")
	if delivery == "" {
		sum := sha256.Sum256(body)
		delivery = "sha-" + hex.EncodeToString(sum[:8])
	}
	ignore := func(reason string) []Inbound {
		return []Inbound{{ExternalID: delivery, Kind: InIgnore, Reason: reason}}
	}
	if event == "ping" {
		return ignore("ping de configuração"), nil
	}
	var p ghPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, errors.New("github: corpo não é um evento válido")
	}
	var (
		thing  *ghThing
		text   string
		author ghUser
		curl   string
	)
	switch {
	case event == "issue_comment" && p.Action == "created" && p.Issue != nil && p.Comment != nil:
		thing, text, author, curl = p.Issue, p.Comment.Body, p.Comment.User, p.Comment.HTMLURL
	case event == "pull_request_review_comment" && p.Action == "created" && p.PullRequest != nil && p.Comment != nil:
		thing, text, author, curl = p.PullRequest, p.Comment.Body, p.Comment.User, p.Comment.HTMLURL
	case event == "pull_request_review" && p.Action == "submitted" && p.PullRequest != nil && p.Review != nil:
		thing, text, author, curl = p.PullRequest, p.Review.Body, p.Review.User, p.Review.HTMLURL
	default:
		return ignore("evento " + event + " (" + p.Action + ") não tratado"), nil
	}
	repo := p.Repository.FullName
	if !domain.RepoAllowed(cfg.Repos, repo) {
		return ignore("repositório fora da lista do canal"), nil
	}
	if author.Type == "Bot" || strings.HasSuffix(strings.ToLower(author.Login), "[bot]") {
		return ignore("autor é um bot"), nil
	}
	login := strings.ToLower(author.Login)
	if login == "" {
		return ignore("autor desconhecido"), nil
	}
	isPR := event != "issue_comment" || thing.PullRequest != nil
	in := Inbound{ExternalID: delivery, Peer: repo + "#" + strconv.Itoa(thing.Number), Sender: login, SenderLabel: author.Login,
		Meta: map[string]string{"repo": repo, "number": strconv.Itoa(thing.Number), "title": thing.Title, "url": thing.HTMLURL,
			"comment_url": curl, "event": event, "is_pr": strconv.FormatBool(isPR)}}
	text = strings.TrimSpace(text)
	if low := strings.ToLower(text); strings.HasPrefix(low, "/hanko") && (len(low) == 6 || low[6] == ' ' || low[6] == '\n') {
		rest := strings.TrimSpace(text[6:])
		first, arg, _ := strings.Cut(rest, " ")
		if strings.EqualFold(first, "vincular") {
			in.Kind, in.Command, in.Arg = InCommand, "vincular", strings.TrimSpace(arg)
			return []Inbound{in}, nil
		}
		in.Kind, in.Text = InText, rest
		if in.Text == "" {
			return ignore("comando vazio"), nil
		}
		return []Inbound{in}, nil
	}
	mention := cfg.Mention
	if mention == "" {
		mention = domain.DefaultGitHubMention
	}
	if !mentionRegexp(mention).MatchString(text) {
		return ignore("sem menção"), nil
	}
	in.Kind, in.Text = InText, text
	return []Inbound{in}, nil
}

func (GitHub) headers(c Client) map[string]string {
	return map[string]string{"Authorization": "Bearer " + c.Token, "Accept": "application/vnd.github+json",
		"X-GitHub-Api-Version": "2022-11-28", "User-Agent": "Hanko-Channel/1"}
}

// Send publica um comentário (issues/{n}/comments) ou, com Review, uma review do PR (pulls/{n}/reviews).
func (g GitHub) Send(ctx context.Context, c Client, out Outbound) (string, error) {
	repo, n, ok := domain.ParseGitHubPeer(out.Peer)
	if !ok {
		return "", &PermanentError{Err: errors.New("github: destino inválido (use dono/repo#N)")}
	}
	if !domain.RepoAllowed(c.Config.Repos, repo) {
		return "", &PermanentError{Err: errors.New("github: repositório fora da lista do canal")}
	}
	if c.Token == "" {
		return "", &PermanentError{Err: errors.New("github: o canal não tem token")}
	}
	text := clip(strings.TrimSpace(out.Text), githubMaxBody)
	if text == "" {
		return "", &PermanentError{Err: errors.New("github: mensagem vazia")}
	}
	var path string
	var body map[string]any
	if out.Review != nil {
		if !out.Review.Event.Valid() {
			return "", &PermanentError{Err: errors.New("github: veredito de review inválido")}
		}
		path = fmt.Sprintf("/repos/%s/pulls/%d/reviews", repo, n)
		body = map[string]any{"event": string(out.Review.Event), "body": text}
	} else {
		path = fmt.Sprintf("/repos/%s/issues/%d/comments", repo, n)
		body = map[string]any{"body": text}
	}
	status, b, err := call(ctx, c.HTTP, http.MethodPost, g.base(c)+path, g.headers(c), body)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK && status != http.StatusCreated {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(b, &e)
		cerr := classify(status, "github")
		// 403 por limite de uso se resolve esperando; 403/404 por permissão ou destino não
		if status == http.StatusForbidden && strings.Contains(strings.ToLower(e.Message), "rate limit") {
			cerr = fmt.Errorf("canal: github: limite de uso do provedor (%d)", status)
		}
		if e.Message != "" {
			cerr = fmt.Errorf("%w (%s)", cerr, clip(scrub(e.Message, c.Token), 200))
		}
		return "", cerr
	}
	var r struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(b, &r)
	return strconv.FormatInt(r.ID, 10), nil
}

// Fetch não existe no GitHub: os anexos são links no texto.
func (GitHub) Fetch(context.Context, Client, string, int64) ([]byte, error) {
	return nil, ErrUnsupported
}

// Register não existe: o webhook do repositório é configurado por quem administra o repositório.
func (GitHub) Register(context.Context, Client, string, string) error { return ErrUnsupported }

// Ack não existe no GitHub.
func (GitHub) Ack(context.Context, Client, string, string) error { return nil }
