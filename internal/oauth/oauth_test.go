package oauth_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/oauth"
	"github.com/renatobardi/hanko/internal/oauth/oauthtest"
)

func TestPKCE(t *testing.T) {
	v, c, err := oauth.NewPKCE()
	if err != nil {
		t.Fatal(err)
	}
	if len(v) != 43 || c != oauth.ChallengeS256(v) || strings.ContainsAny(c, "=+/") {
		t.Fatalf("pkce inválido: %q %q", v, c)
	}
	// vetor do RFC 7636 apêndice B
	if got := oauth.ChallengeS256("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"); got != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Fatalf("S256 = %s", got)
	}
	v2, _, _ := oauth.NewPKCE()
	if v == v2 {
		t.Fatal("verifier repetido")
	}
	s1, _ := oauth.NewState()
	s2, _ := oauth.NewState()
	if s1 == s2 || len(s1) < 40 {
		t.Fatal("state fraco")
	}
}

func newClient() *oauth.Client { return &oauth.Client{HTTP: http.DefaultClient} }

func TestFluxoCompleto(t *testing.T) {
	f := oauthtest.New()
	defer f.Close()
	ctx := context.Background()
	c := newClient()

	d, err := c.Discover(ctx, f.MCPURL())
	if err != nil {
		t.Fatal(err)
	}
	if d.Resource != f.URL+"/mcp" || d.Metadata.TokenEndpoint != f.URL+"/token" || len(d.Scopes) != 2 {
		t.Fatalf("descoberta: %+v", d)
	}
	reg, err := c.Register(ctx, d.Metadata, "Hanko", "https://h.example/oauth/callback")
	if err != nil || reg.ClientID != "client-abc" {
		t.Fatalf("registro: %+v %v", reg, err)
	}
	if got := f.Registered[0]["token_endpoint_auth_method"]; got != "none" {
		t.Fatalf("cliente deve ser público, veio %v", got)
	}
	verifier, challenge, _ := oauth.NewPKCE()
	state, _ := oauth.NewState()
	au, err := oauth.AuthURL(d.Metadata, reg.ClientID, "https://h.example/oauth/callback", []string{"read"}, state, challenge, d.Resource)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(au)
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("response_type") != "code" || q.Get("resource") != d.Resource || q.Get("state") != state {
		t.Fatalf("auth url: %s", au)
	}
	st, ch, _, _ := oauthtest.ParseAuthURL(au)
	if st != state || ch != challenge {
		t.Fatal("state/challenge não viajaram")
	}
	code := f.IssueCode(ch)

	// verifier errado: o servidor recusa (PKCE vale de verdade)
	if _, err := c.Exchange(ctx, d.Metadata, reg, code, "errado", "https://h.example/oauth/callback", d.Resource); !errors.Is(err, oauth.ErrInvalidGrant) {
		t.Fatalf("verifier errado deveria dar invalid_grant: %v", err)
	}
	code = f.IssueCode(ch)
	tok, err := c.Exchange(ctx, d.Metadata, reg, code, verifier, "https://h.example/oauth/callback", d.Resource)
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken == "" || tok.RefreshToken == "" || tok.ExpiresAt == nil || time.Until(*tok.ExpiresAt) < 59*time.Minute {
		t.Fatalf("token: %+v", tok)
	}
	// o código é de uso único
	if _, err := c.Exchange(ctx, d.Metadata, reg, code, verifier, "x", ""); !errors.Is(err, oauth.ErrInvalidGrant) {
		t.Fatalf("código reusado: %v", err)
	}

	if st, err := c.Probe(ctx, f.MCPURL(), tok.AccessToken); err != nil || st != oauth.ProbeOK {
		t.Fatalf("probe ok: %v %v", st, err)
	}
	if st, _ := c.Probe(ctx, f.MCPURL(), "velho"); st != oauth.ProbeUnauthorized {
		t.Fatalf("probe 401: %v", st)
	}

	tok2, err := c.Refresh(ctx, d.Metadata, reg, tok.RefreshToken, d.Resource)
	if err != nil || tok2.AccessToken == tok.AccessToken || tok2.RefreshToken == "" {
		t.Fatalf("refresh: %+v %v", tok2, err)
	}
	f.Revoked = true
	if _, err := c.Refresh(ctx, d.Metadata, reg, tok2.RefreshToken, d.Resource); !errors.Is(err, oauth.ErrInvalidGrant) {
		t.Fatalf("revogado: %v", err)
	}
}

func TestDescobertaSemMetadadosUsaPadrao(t *testing.T) {
	f := oauthtest.New()
	defer f.Close()
	f.NoMetadata = true
	d, err := newClient().Discover(context.Background(), f.MCPURL())
	if err != nil {
		t.Fatal(err)
	}
	if d.Metadata.AuthorizationEndpoint != f.URL+"/authorize" || d.Metadata.TokenEndpoint != f.URL+"/token" || d.Metadata.RegistrationEndpoint != f.URL+"/register" {
		t.Fatalf("padrão: %+v", d.Metadata)
	}
}

func TestSemPKCEEhRecusado(t *testing.T) {
	f := oauthtest.New()
	defer f.Close()
	f.NoPKCE = true
	if _, err := newClient().Discover(context.Background(), f.MCPURL()); !errors.Is(err, oauth.ErrNoPKCE) {
		t.Fatalf("esperava ErrNoPKCE: %v", err)
	}
}

func TestSemRegistroDinamico(t *testing.T) {
	f := oauthtest.New()
	defer f.Close()
	f.NoDCR = true
	c := newClient()
	d, err := c.Discover(context.Background(), f.MCPURL())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Register(context.Background(), d.Metadata, "Hanko", "https://h/cb"); !errors.Is(err, oauth.ErrNoRegistration) {
		t.Fatalf("esperava ErrNoRegistration: %v", err)
	}
}

func TestValidateRecusaEndereco(t *testing.T) {
	f := oauthtest.New()
	defer f.Close()
	c := &oauth.Client{HTTP: http.DefaultClient, Validate: func(string) error { return errors.New("interno") }}
	_, err := c.Discover(context.Background(), f.MCPURL())
	var re *oauth.RefusedError
	if !errors.As(err, &re) {
		t.Fatalf("esperava RefusedError: %v", err)
	}
	if strings.Contains(err.Error(), "/mcp") {
		t.Fatalf("a mensagem não deve vazar o caminho: %v", err)
	}
}

func TestSondaForaDoArEErro(t *testing.T) {
	c := newClient()
	if st, err := c.Probe(context.Background(), "http://127.0.0.1:1/mcp", "x"); err == nil || st != oauth.ProbeUnreachable {
		t.Fatalf("fora do ar: %v %v", st, err)
	}
	e := &oauth.Error{Code: "invalid_client", Description: "d"}
	if !strings.Contains(e.Error(), "invalid_client") || errors.Is(e, oauth.ErrInvalidGrant) {
		t.Fatal("Error")
	}
}
