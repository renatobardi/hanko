// Package passkey valida cerimônias WebAuthn (ADR-0070). É o único pacote do Hanko que importa
// github.com/go-webauthn/webauthn: nada fora daqui conhece os tipos da biblioteca. A atestação é
// none (o Hanko não precisa provar o modelo do autenticador) e a verificação de usuário (UV) é
// obrigatória em cadastro e prova. O desafio de uso único e o seu prazo ficam com o chamador
// (tabela webauthn_challenges); aqui só se cria e se verifica.
package passkey

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/renatobardi/hanko/internal/domain"
)

// ErrVerificationFailed: a resposta WebAuthn não passou (desafio, origem, assinatura, contador,
// verificação de usuário…). O detalhe da biblioteca vai no wrap, mas nunca para o cliente.
var ErrVerificationFailed = errors.New("passkey: verificação falhou")

// DisplayName é o nome da parte confiante mostrado pelo navegador no diálogo do autenticador.
const DisplayName = "Hanko"

// Config configura a parte confiante: o id (host da HANKO_PUBLIC_URL, sem esquema nem porta) e as
// origens aceitas (a própria HANKO_PUBLIC_URL).
type Config struct {
	RPID    string
	Origins []string
}

// ParseConfig deriva a configuração da URL pública da instalação.
func ParseConfig(publicURL string) (Config, error) {
	u, err := url.Parse(publicURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return Config{}, fmt.Errorf("passkey: URL pública inválida: %q", publicURL)
	}
	return Config{RPID: u.Hostname(), Origins: []string{u.Scheme + "://" + u.Host}}, nil
}

// Service executa as cerimônias. Zero value não serve: use New.
type Service struct {
	w *webauthn.WebAuthn
}

// New valida e monta o serviço.
func (c Config) New() (*Service, error) {
	if c.RPID == "" {
		return nil, fmt.Errorf("passkey: RPID obrigatório")
	}
	if len(c.Origins) == 0 {
		return nil, fmt.Errorf("passkey: origem obrigatória")
	}
	selection := protocol.AuthenticatorSelection{
		ResidentKey:      protocol.ResidentKeyRequirementPreferred, // passkey quando o autenticador der
		UserVerification: protocol.VerificationRequired,            // ADR-0070: UV obrigatória
	}
	w, err := webauthn.New(&webauthn.Config{
		RPDisplayName:          DisplayName,
		RPID:                   c.RPID,
		RPOrigins:              c.Origins,
		AttestationPreference:  protocol.PreferNoAttestation, // ADR-0070: atestação none
		AuthenticatorSelection: selection,
	})
	if err != nil {
		return nil, fmt.Errorf("passkey: %w", err)
	}
	return &Service{w: w}, nil
}

// User identifica o dono das credenciais; o handle do WebAuthn é o id do usuário no Hanko.
type User struct {
	ID   domain.ID
	Name string // e-mail: só para o navegador mostrar no diálogo
}

// Credential são os dados de uma credencial guardados no banco (sem ids do banco).
type Credential struct {
	CredentialID   []byte
	PublicKey      []byte // COSE
	SignCount      uint32
	AAGUID         []byte
	Transports     []string
	BackupEligible bool
	BackupState    bool
}

// RegistrationBegin cria as opções de cadastro (PublicKeyCredentialCreationOptions em JSON) e o
// desafio de uso único que o chamador guarda com prazo.
func (s *Service) RegistrationBegin(u User, existing []Credential) (challenge []byte, options json.RawMessage, err error) {
	user := adaptUser(u, existing)
	creation, sess, err := s.w.BeginRegistration(user)
	if err != nil {
		return nil, nil, fmt.Errorf("passkey: begin registration: %w", err)
	}
	challenge, err = decodeChallenge(sess.Challenge)
	if err != nil {
		return nil, nil, err
	}
	options, err = json.Marshal(creation)
	if err != nil {
		return nil, nil, fmt.Errorf("passkey: marshal creation: %w", err)
	}
	return challenge, options, nil
}

// RegistrationFinish verifica a resposta de navigator.credentials.create contra o desafio guardado
// e devolve a credencial para o chamador gravar.
func (s *Service) RegistrationFinish(u User, challenge []byte, existing []Credential, response json.RawMessage) (Credential, error) {
	user := adaptUser(u, existing)
	parsed, err := protocol.ParseCredentialCreationResponseBytes(response)
	if err != nil {
		return Credential{}, fmt.Errorf("%w: resposta de cadastro: %w", ErrVerificationFailed, err)
	}
	// CredParams é o conjunto pedido no begin (o padrão daqui); sem ele a verificação recusaria
	// qualquer algoritmo, porque conferiria a chave contra uma lista vazia.
	sess := webauthn.SessionData{Challenge: encodeChallenge(challenge), UserID: user.WebAuthnID(),
		UserVerification: protocol.VerificationRequired, CredParams: webauthn.CredentialParametersDefault()}
	cred, err := s.w.CreateCredential(user, sess, parsed)
	if err != nil {
		return Credential{}, fmt.Errorf("%w: %w", ErrVerificationFailed, err)
	}
	return credentialOut(cred), nil
}

// LoginBegin cria as opções da prova (PublicKeyCredentialRequestOptions em JSON) e o desafio.
func (s *Service) LoginBegin(u User, creds []Credential) (challenge []byte, options json.RawMessage, err error) {
	user := adaptUser(u, creds)
	assertion, sess, err := s.w.BeginLogin(user)
	if err != nil {
		return nil, nil, fmt.Errorf("passkey: begin login: %w", err)
	}
	challenge, err = decodeChallenge(sess.Challenge)
	if err != nil {
		return nil, nil, err
	}
	options, err = json.Marshal(assertion)
	if err != nil {
		return nil, nil, fmt.Errorf("passkey: marshal assertion: %w", err)
	}
	return challenge, options, nil
}

// LoginFinish verifica a resposta de navigator.credentials.get (desafio, origem, assinatura,
// contador de assinatura e UV) e devolve a credencial com o contador novo para o chamador gravar.
func (s *Service) LoginFinish(u User, challenge []byte, creds []Credential, response json.RawMessage) (Credential, error) {
	user := adaptUser(u, creds)
	parsed, err := protocol.ParseCredentialRequestResponseBytes(response)
	if err != nil {
		return Credential{}, fmt.Errorf("%w: resposta de prova: %w", ErrVerificationFailed, err)
	}
	sess := webauthn.SessionData{Challenge: encodeChallenge(challenge), UserID: user.WebAuthnID(),
		UserVerification: protocol.VerificationRequired}
	cred, err := s.w.ValidateLogin(user, sess, parsed)
	if err != nil {
		return Credential{}, fmt.Errorf("%w: %w", ErrVerificationFailed, err)
	}
	// A biblioteca só marca o clone (o contador não subiu); quem decide é a prova (ADR-0070: contador conferido).
	if cred.Authenticator.CloneWarning {
		return Credential{}, fmt.Errorf("%w: contador de assinatura não subiu", ErrVerificationFailed)
	}
	return credentialOut(cred), nil
}

// RPID devolve o id da parte confiante (para o chamador informar na UI, se precisar).
func (s *Service) RPID() string { return s.w.Config.RPID }

func adaptUser(u User, creds []Credential) webauthnUser {
	wu := webauthnUser{id: u.ID[:], name: u.Name}
	for _, c := range creds {
		wu.creds = append(wu.creds, adaptCredential(c))
	}
	return wu
}

func adaptCredential(c Credential) webauthn.Credential {
	t := make([]protocol.AuthenticatorTransport, 0, len(c.Transports)) // nunca nil: JSON vazio, não null
	for _, x := range c.Transports {
		t = append(t, protocol.AuthenticatorTransport(x))
	}
	return webauthn.Credential{ID: c.CredentialID, PublicKey: c.PublicKey,
		Transport: t, Authenticator: webauthn.Authenticator{AAGUID: c.AAGUID, SignCount: c.SignCount},
		Flags: webauthn.CredentialFlags{BackupEligible: c.BackupEligible, BackupState: c.BackupState}}
}

func credentialOut(c *webauthn.Credential) Credential {
	t := make([]string, 0, len(c.Transport))
	for _, x := range c.Transport {
		t = append(t, string(x))
	}
	return Credential{CredentialID: c.ID, PublicKey: c.PublicKey, SignCount: c.Authenticator.SignCount,
		AAGUID: c.Authenticator.AAGUID, Transports: t,
		BackupEligible: c.Flags.BackupEligible, BackupState: c.Flags.BackupState}
}

type webauthnUser struct {
	id    []byte
	name  string
	creds []webauthn.Credential
}

func (u webauthnUser) WebAuthnID() []byte                         { return u.id }
func (u webauthnUser) WebAuthnName() string                       { return u.name }
func (u webauthnUser) WebAuthnDisplayName() string                { return u.name }
func (u webauthnUser) WebAuthnCredentials() []webauthn.Credential { return u.creds }

func encodeChallenge(raw []byte) string { return base64.RawURLEncoding.EncodeToString(raw) }

func decodeChallenge(s string) ([]byte, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("passkey: desafio inválido: %w", err)
	}
	return b, nil
}
