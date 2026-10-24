package passkey

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/renatobardi/hanko/internal/auth/passkey/authtest"
	"github.com/renatobardi/hanko/internal/domain"
)

var testConfig = Config{RPID: "localhost", Origins: []string{"https://localhost:8080"}}

func newService(t *testing.T) *Service {
	t.Helper()
	s, err := testConfig.New()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// creationOptions lê só o que o teste confere das opções de cadastro (publicKey.rp.id).
type creationOptions struct {
	PublicKey struct {
		RP struct {
			ID string `json:"id"`
		} `json:"rp"`
	} `json:"publicKey"`
}

// register faz o cadastro de um autenticador de software para u e devolve a credencial guardada.
func register(t *testing.T, s *Service, a *authtest.Authenticator, u User) Credential {
	t.Helper()
	ch, opts, err := s.RegistrationBegin(u, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := a.Create(opts)
	if err != nil {
		t.Fatal(err)
	}
	cred, err := s.RegistrationFinish(u, ch, nil, resp)
	if err != nil {
		var pe *protocol.Error
		if errors.As(err, &pe) {
			t.Fatalf("RegistrationFinish: %+v", *pe)
		}
		t.Fatalf("RegistrationFinish: %v", err)
	}
	return cred
}

func TestRegisterAndLogin(t *testing.T) {
	s := newService(t)
	a := authtest.New()
	a.Origin = "https://localhost:8080"
	u := User{ID: domain.NewID(), Name: "dono@example.com"}

	ch, opts, err := s.RegistrationBegin(u, nil)
	if err != nil {
		t.Fatal(err)
	}
	var creation creationOptions
	if err := json.Unmarshal(opts, &creation); err != nil {
		t.Fatal(err)
	}
	if creation.PublicKey.RP.ID != "localhost" {
		t.Fatalf("rp.id = %q, quer localhost", creation.PublicKey.RP.ID)
	}
	resp, err := a.Create(opts)
	if err != nil {
		t.Fatal(err)
	}
	cred, err := s.RegistrationFinish(u, ch, nil, resp)
	if err != nil {
		t.Fatalf("RegistrationFinish: %v", err)
	}
	if len(cred.CredentialID) == 0 || len(cred.PublicKey) == 0 {
		t.Fatal("credencial sem id ou chave pública")
	}
	if !cred.BackupEligible || !cred.BackupState {
		t.Fatal("flags de backup do authtest devem estar ligadas")
	}

	// prova: desafio novo, assinatura conferida, contador gravado
	ch2, opts2, err := s.LoginBegin(u, []Credential{cred})
	if err != nil {
		t.Fatal(err)
	}
	resp2, err := a.Get(opts2)
	if err != nil {
		t.Fatal(err)
	}
	used, err := s.LoginFinish(u, ch2, []Credential{cred}, resp2)
	if err != nil {
		t.Fatalf("LoginFinish: %v", err)
	}
	if used.SignCount != 1 {
		t.Fatalf("contador de assinatura = %d, quer 1", used.SignCount)
	}
}

func TestLoginFinishRecusaProvaErrada(t *testing.T) {
	s := newService(t)
	a := authtest.New()
	a.Origin = "https://localhost:8080"
	u := User{ID: domain.NewID(), Name: "dono@example.com"}
	cred := register(t, s, a, u)

	// Resposta assinada para um desafio, apresentada com outro: não casa com o desafio deste pedido.
	_, opts, err := s.LoginBegin(u, []Credential{cred})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := a.Get(opts)
	if err != nil {
		t.Fatal(err)
	}
	chOutro, _, err := s.LoginBegin(u, []Credential{cred})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoginFinish(u, chOutro, []Credential{cred}, resp); !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("LoginFinish com desafio de outro pedido = %v, quer ErrVerificationFailed", err)
	}

	// Clone: o autenticador volta ao contador 0, então a próxima prova sai com 1, que já foi usado.
	ch, opts, err := s.LoginBegin(u, []Credential{cred})
	if err != nil {
		t.Fatal(err)
	}
	resp, err = a.Get(opts)
	if err != nil {
		t.Fatal(err)
	}
	used, err := s.LoginFinish(u, ch, []Credential{cred}, resp)
	if err != nil {
		t.Fatalf("primeira prova: %v", err)
	}
	a.SignCount = 0
	ch, opts, err = s.LoginBegin(u, []Credential{used})
	if err != nil {
		t.Fatal(err)
	}
	resp, err = a.Get(opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoginFinish(u, ch, []Credential{used}, resp); !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("prova com contador repetido = %v, quer ErrVerificationFailed", err)
	}
}

// Um IP de loopback vira localhost (o RP ID é domínio); outro IP é recusado; o domínio segue igual.
func TestParseConfigRPIDNaoEIP(t *testing.T) {
	for _, c := range []struct{ url, rpID string }{
		{"http://127.0.0.1:7420", "localhost"},
		{"http://[::1]:7420", "localhost"},
		{"https://hanko.oute.pro", "hanko.oute.pro"},
	} {
		cfg, err := ParseConfig(c.url)
		if err != nil {
			t.Fatalf("ParseConfig(%q): %v", c.url, err)
		}
		if cfg.RPID != c.rpID {
			t.Fatalf("ParseConfig(%q).RPID = %q, quer %q", c.url, cfg.RPID, c.rpID)
		}
		if _, err := cfg.New(); err != nil {
			t.Fatalf("New com RPID %q: %v", cfg.RPID, err)
		}
	}
	if _, err := ParseConfig("http://10.0.0.5:7420"); err == nil {
		t.Fatal("IP que não é loopback deve ser recusado")
	}
}

func TestParseConfig(t *testing.T) {
	c, err := ParseConfig("https://hanko.example.com:8443/")
	if err != nil {
		t.Fatal(err)
	}
	if c.RPID != "hanko.example.com" {
		t.Fatalf("RPID = %q", c.RPID)
	}
	if len(c.Origins) != 1 || c.Origins[0] != "https://hanko.example.com:8443" {
		t.Fatalf("Origins = %v", c.Origins)
	}
	if _, err := ParseConfig("sem-esquema"); err == nil {
		t.Fatal("URL sem esquema deve falhar")
	}
}
