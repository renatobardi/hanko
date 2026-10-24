package service

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/hkdf"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/secretref"
	"github.com/renatobardi/hanko/internal/store"
)

// Envio por link com a chave no fragmento (ADR-0069, fatia 4, #59). O servidor guarda só o texto cifrado; a chave
// de 32 bytes vai só no fragmento do link, que o navegador não envia: não chega ao log, ao span, ao evento nem ao
// banco. Quem cria por humano gera a chave e cifra no navegador (ou na CLI); a persona não digita o valor, então o
// servidor cifra e descarta a chave depois de montar o link.

// Parâmetros acordados entre o cliente (WebCrypto) e o servidor (ADR-0069 §3 e §5).
const (
	// sendHKDFInfo é o info do HKDF que deriva a chave AES-256 da chave do link (e da senha derivada, se houver).
	sendHKDFInfo = "hanko-send-v1"
	// sendPurgeBatch é quantos envios vencidos o job apaga por execução.
	sendPurgeBatch = 200
	// sendCipherOverhead são os bytes que o AES-GCM acrescenta ao texto claro: nonce (12) e tag (16).
	sendCipherOverhead = 28
)

// Erros de abrir um envio. Vencido, apagado ou sem acessos vira "não encontrado" para quem abre (sem dizer qual).
var (
	ErrSendGone     = fmt.Errorf("envio indisponível: venceu, atingiu o limite de acessos ou foi apagado: %w", domain.ErrNotFound)
	ErrSendLocked   = fmt.Errorf("envio bloqueado por senha errada repetida; tente de novo mais tarde: %w", domain.ErrConflict)
	ErrSendPassword = fmt.Errorf("senha do envio incorreta: %w", domain.ErrNotAllowed)
)

func b64u(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func unb64u(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
}

// newSendKey gera a chave do link (32 bytes).
func newSendKey() ([]byte, error) {
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, fmt.Errorf("gerar chave do envio: %w", err)
	}
	return k, nil
}

// newSendPublicID gera o id público do envio (16 bytes em base64url) para quem o cria no servidor (persona).
func newSendPublicID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("gerar id público do envio: %w", err)
	}
	return b64u(b), nil
}

// validPublicID confere o id público: 16 bytes em base64url, que é o que entra no AAD do texto cifrado.
func validPublicID(id string) error {
	if b, err := unb64u(id); err != nil || len(b) != 16 || len(id) != 22 {
		return domain.Invalid("public_id", "id público do envio: 16 bytes em base64url (22 caracteres)")
	}
	return nil
}

// sendAESKey deriva a chave AES-256-GCM: HKDF-SHA256(ikm = chave do link [+ senha derivada], salt = sal, info =
// hanko-send-v1). Sem senha, o sal é vazio.
func sendAESKey(linkKey, pwdDerived, salt []byte) ([]byte, error) {
	ikm := linkKey
	if pwdDerived != nil {
		ikm = append(append([]byte{}, linkKey...), pwdDerived...)
	}
	k := make([]byte, 32)
	h := hkdf.New(sha256.New, ikm, salt, []byte(sendHKDFInfo))
	if _, err := io.ReadFull(h, k); err != nil {
		return nil, fmt.Errorf("derivar chave do envio: %w", err)
	}
	return k, nil
}

// sealSend cifra o conteúdo com AES-256-GCM (AAD = "send/<public_id>") e devolve base64url(nonce || cifra).
func sealSend(aesKey []byte, publicID string, plaintext []byte) (string, error) {
	gcm, err := newGCM(aesKey)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("gerar nonce do envio: %w", err)
	}
	return b64u(gcm.Seal(nonce, nonce, plaintext, []byte("send/"+publicID))), nil
}

func newGCM(aesKey []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// sendProofOK confere a prova da senha (HKDF(senha derivada, hanko-send-auth)) contra o verificador guardado
// (SHA-256 da prova). O servidor não vê a senha nem a chave do link.
func sendProofOK(row store.SecretSend, proof string) bool {
	want, err := unb64u(row.PasswordVerifier)
	if err != nil || len(want) != sha256.Size {
		return false
	}
	got, err := unb64u(proof)
	if err != nil || len(got) == 0 {
		return false
	}
	sum := sha256.Sum256(got)
	return subtle.ConstantTimeCompare(sum[:], want) == 1
}

// CreateSendInput é o envio criado por humano: o conteúdo já chega cifrado no cliente (UI/CLI), com o id público
// gerado no cliente (entra no AAD). O servidor nunca recebe a chave do link nem a senha.
type CreateSendInput struct {
	PublicID         string
	Kind             domain.SendKind
	Ciphertext       string // texto: base64url(nonce||cifra), até MaxSendTextBytes em claro
	Label            string
	KdfSalt          string // com senha: sal do PBKDF2 (base64url); sem senha: vazio
	PasswordVerifier string // com senha: base64url(SHA-256 da prova)
	ExpiresIn        time.Duration
	MaxAccesses      *int
}

// PersonaSendInput é o envio criado por persona: aponta para um campo do cofre, sem valor digitado.
type PersonaSendInput struct {
	Item        string
	Field       string
	ExpiresIn   time.Duration
	MaxAccesses *int
}

// SendView é um envio visto por quem criou. Link só sai quando o servidor montou o link (persona).
type SendView struct {
	domain.SecretSend
	Link string
}

// PublicSendView é o que a página pública precisa antes de abrir (nunca chave, senha nem conteúdo).
type PublicSendView struct {
	Kind        domain.SendKind
	HasPassword bool
	KdfSalt     string
	ExpiresAt   time.Time
	LockedUntil *time.Time
	Openable    bool
}

// OpenedSendView é o conteúdo cifrado entregue a quem abriu (decifra no navegador).
type OpenedSendView struct {
	Kind        domain.SendKind
	Ciphertext  string
	AccessCount int
}

// validateSendInput confere o que o banco e o domínio exigem antes de gravar um envio de texto.
func validateSendInput(in CreateSendInput) error {
	if err := domain.ValidateSend(in.Kind, in.ExpiresIn, in.MaxAccesses); err != nil {
		return err
	}
	if err := validPublicID(in.PublicID); err != nil {
		return err
	}
	if (in.KdfSalt == "") != (in.PasswordVerifier == "") {
		return domain.Invalid("kdf_salt", "sal e verificador da senha andam juntos")
	}
	if in.KdfSalt != "" {
		if s, err := unb64u(in.KdfSalt); err != nil || len(s) != 16 {
			return domain.Invalid("kdf_salt", "sal da senha inválido (16 bytes em base64url)")
		}
		if v, err := unb64u(in.PasswordVerifier); err != nil || len(v) != sha256.Size {
			return domain.Invalid("password_verifier", "verificador inválido (32 bytes em base64url)")
		}
	}
	if in.Kind != domain.SendText {
		return domain.Invalid("kind", "envio de arquivo chega com o upload do blob, na entrega seguinte do #59")
	}
	if in.Ciphertext == "" {
		return domain.Invalid("ciphertext", "envio de texto exige o conteúdo cifrado")
	}
	raw, err := unb64u(in.Ciphertext)
	if err != nil || len(raw) > domain.MaxSendTextBytes+sendCipherOverhead {
		return domain.Invalid("ciphertext", "conteúdo cifrado inválido ou maior que 64 KiB")
	}
	return nil
}

// sendFromRow mapeia a linha do banco para o domínio.
func sendFromRow(r store.SecretSend) domain.SecretSend {
	s := domain.SecretSend{
		ID: r.ID, CompanyID: r.CompanyID, PublicID: r.PublicID, Kind: domain.SendKind(r.Kind),
		SizeBytes: int(r.SizeBytes), Label: r.Label, HasPassword: r.PasswordVerifier != "",
		AccessCount: int(r.AccessCount), ExpiresAt: r.ExpiresAt,
		LastAccessedAt: r.LastAccessedAt, PurgedAt: r.PurgedAt, SourceField: r.SourceField, RunID: r.RunID,
		CreatedAt: r.CreatedAt,
	}
	if r.MaxAccesses != nil {
		m := int(*r.MaxAccesses)
		s.MaxAccesses = &m
	}
	if r.SourceSecretID != nil {
		id := domain.ID(*r.SourceSecretID)
		s.SourceSecretID = &id
	}
	switch r.CreatedByType {
	case "human":
		s.CreatedBy = domain.Actor{Type: domain.ActorHuman, ID: r.CreatedByID}
	case "persona":
		s.CreatedBy = domain.Actor{Type: domain.ActorPersona, ID: r.CreatedByID}
	default:
		s.CreatedBy = domain.SystemActor
	}
	return s
}

// sendEventData nunca leva chave, senha, prova nem conteúdo (ADR-0069 §9).
func sendEventData(s domain.SecretSend) map[string]any {
	d := map[string]any{"kind": string(s.Kind), "expires_at": s.ExpiresAt.Format(time.RFC3339)}
	if s.MaxAccesses != nil {
		d["max_accesses"] = *s.MaxAccesses
	}
	if s.SourceSecretID != nil {
		d["source_item"] = s.SourceSecretID.String()
		d["source_field"] = s.SourceField
	}
	return d
}

// requireSendCreator confere quem cria um envio por humano: pessoa autenticada, com reautenticação recente
// (ADR-0070) e papel member na empresa.
func (s *Service) requireSendCreator(ctx context.Context, actor domain.Actor, companyID domain.ID) error {
	if actor.Type != domain.ActorHuman {
		return domain.ErrForbidden
	}
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return err
	}
	// Sem identidade no contexto é o modo confiável (instalação sem login): RequireReauth passa, como nas outras
	// ações sensíveis (ADR-0070 §5).
	id, _ := auth.IdentityFrom(ctx)
	_, err := s.RequireReauth(id)
	return err
}

// CreateSend grava um envio criado por humano. O conteúdo já veio cifrado pelo cliente; a chave do link nunca
// passa por aqui.
func (s *Service) CreateSend(ctx context.Context, actor domain.Actor, companyID domain.ID, in CreateSendInput) (SendView, error) {
	if err := s.requireSendCreator(ctx, actor, companyID); err != nil {
		return SendView{}, err
	}
	if err := validateSendInput(in); err != nil {
		return SendView{}, err
	}
	createdBy, createdByID := sendCreator(actor)
	id := domain.NewID()
	var out SendView
	err := s.withTx(ctx, func(t *txc) error {
		row, err := t.q.CreateSecretSend(t.ctx, store.CreateSecretSendParams{
			ID: id, CompanyID: companyID, PublicID: in.PublicID, Kind: string(in.Kind),
			Ciphertext: in.Ciphertext, Label: in.Label, KdfSalt: in.KdfSalt, PasswordVerifier: in.PasswordVerifier,
			MaxAccesses: i32p(in.MaxAccesses), ExpiresAt: t.now.Add(in.ExpiresIn),
			CreatedByType: createdBy, CreatedByID: createdByID, Now: t.now,
		})
		if err != nil {
			return mapErr(err, "criar envio")
		}
		out = SendView{SecretSend: sendFromRow(row)}
		return t.activity(actor, companyID, "send.created", "secret_send", id, sendEventData(out.SecretSend), "")
	})
	return out, err
}

// CreatePersonaSend cria um envio a partir de um campo que a persona alcança (ADR-0069 §4). O servidor lê o valor
// no cofre, cifra com uma chave nova, monta o link e descarta a chave: o link é a única cópia dela.
func (s *Service) CreatePersonaSend(ctx context.Context, actor domain.Actor, companyID domain.ID, in PersonaSendInput) (SendView, error) {
	if actor.Type != domain.ActorPersona || actor.ID == nil {
		return SendView{}, domain.ErrForbidden
	}
	personaID := *actor.ID
	if err := domain.ValidateSend(domain.SendText, in.ExpiresIn, in.MaxAccesses); err != nil {
		return SendView{}, err
	}
	if !secretref.ValidName(in.Item) {
		return SendView{}, domain.Invalid("item", "nome do item inválido")
	}
	ref := secretref.Ref{Name: in.Item, Field: in.Field}
	if err := s.requirePersonaUse(ctx, companyID, personaID, ref); err != nil {
		return SendView{}, err
	}
	sec, field, err := s.personaSendField(ctx, companyID, ref)
	if err != nil {
		return SendView{}, err
	}
	base := strings.TrimRight(s.PublicURL, "/")
	if base == "" {
		return SendView{}, domain.Invalid("public_url", "HANKO_PUBLIC_URL não está definido: o link do envio precisa dela")
	}
	plain, err := s.resolverFor(ctx, companyID, &personaID)(ref)
	if err != nil {
		return SendView{}, err
	}
	if len(plain) > domain.MaxSendTextBytes {
		return SendView{}, domain.Invalid("value", "o campo passa de 64 KiB: não cabe num envio de texto")
	}
	linkKey, err := newSendKey()
	if err != nil {
		return SendView{}, err
	}
	defer clear(linkKey)
	publicID, err := newSendPublicID()
	if err != nil {
		return SendView{}, err
	}
	aesKey, err := sendAESKey(linkKey, nil, nil)
	if err != nil {
		return SendView{}, err
	}
	defer clear(aesKey)
	ciphertext, err := sealSend(aesKey, publicID, []byte(plain))
	if err != nil {
		return SendView{}, err
	}
	id := domain.NewID()
	var out SendView
	err = s.withTx(ctx, func(t *txc) error {
		row, err := t.q.CreateSecretSend(t.ctx, store.CreateSecretSendParams{
			ID: id, CompanyID: companyID, PublicID: publicID, Kind: string(domain.SendText),
			Ciphertext: ciphertext, Label: in.Item + "#" + field.Name, MaxAccesses: i32p(in.MaxAccesses),
			ExpiresAt: t.now.Add(in.ExpiresIn), SourceSecretID: &sec.ID, SourceField: field.Name,
			CreatedByType: string(domain.ActorPersona), CreatedByID: &personaID, Now: t.now,
		})
		if err != nil {
			return mapErr(err, "criar envio")
		}
		out = SendView{SecretSend: sendFromRow(row)}
		return t.activity(actor, companyID, "send.created", "secret_send", id, sendEventData(out.SecretSend), "")
	})
	if err != nil {
		return SendView{}, err
	}
	out.Link = base + "/s/" + publicID + "#" + b64u(linkKey)
	return out, nil
}

// personaSendField lê o segredo e o campo de uma referência de envio. Semente TOTP e arquivo não saem por envio:
// a semente não vale depois do código atual, e o arquivo chega com o upload do blob (entrega seguinte do #59).
func (s *Service) personaSendField(ctx context.Context, companyID domain.ID, ref secretref.Ref) (store.Secret, store.SecretField, error) {
	q := s.read()
	sec, err := q.GetCompanySecretByName(ctx, store.GetCompanySecretByNameParams{CompanyID: companyID, Name: ref.Name})
	if err != nil {
		return store.Secret{}, store.SecretField{}, mapErr(err, "ler segredo")
	}
	f, err := q.GetSecretField(ctx, store.GetSecretFieldParams{CompanyID: companyID, SecretID: sec.ID, Name: ref.FieldName()})
	if err != nil {
		return store.Secret{}, store.SecretField{}, mapErr(err, "ler campo")
	}
	switch domain.SecretFieldKind(f.Kind) {
	case domain.SecretFieldTOTP:
		return store.Secret{}, store.SecretField{}, domain.Invalid("field", "semente TOTP não sai por envio; envie o código pela persona")
	case domain.SecretFieldFile:
		return store.Secret{}, store.SecretField{}, domain.Invalid("field", "campo arquivo não sai por envio nesta entrega (#59)")
	}
	return sec, f, nil
}

// PublicSendInfo diz à página pública o que pedir antes de abrir o envio. Não conta acesso.
func (s *Service) PublicSendInfo(ctx context.Context, publicID string) (PublicSendView, error) {
	row, err := s.read().GetSecretSendByPublicID(ctx, publicID)
	if err != nil {
		return PublicSendView{}, mapErr(err, "envio")
	}
	now := s.Now()
	return PublicSendView{
		Kind: domain.SendKind(row.Kind), HasPassword: row.KdfSalt != "", KdfSalt: row.KdfSalt,
		ExpiresAt: row.ExpiresAt, LockedUntil: row.LockedUntil, Openable: sendOpenable(row, now),
	}, nil
}

// sendOpenable diz se o envio ainda entrega conteúdo: não apagado, no prazo, com acesso sobrando e sem bloqueio.
func sendOpenable(row store.SecretSend, now time.Time) bool {
	if row.PurgedAt != nil || !row.ExpiresAt.After(now) {
		return false
	}
	if row.MaxAccesses != nil && row.AccessCount >= *row.MaxAccesses {
		return false
	}
	return row.LockedUntil == nil || !row.LockedUntil.After(now)
}

// OpenPublicSend abre o envio: confere a prova da senha (se houver), conta o acesso de forma atômica e devolve o
// texto cifrado. Senha errada não conta acesso e, depois de SendLockAfterFails erros seguidos, bloqueia o envio.
// No último acesso permitido, ou no prazo, o conteúdo é apagado logo depois de entregue.
func (s *Service) OpenPublicSend(ctx context.Context, publicID, passwordProof string) (OpenedSendView, error) {
	now := s.Now()
	row, err := s.read().GetSecretSendByPublicID(ctx, publicID)
	if err != nil {
		return OpenedSendView{}, mapErr(err, "abrir envio")
	}
	if err := s.checkSendAccess(ctx, row, passwordProof, now); err != nil {
		return OpenedSendView{}, err
	}
	var out OpenedSendView
	err = s.withTx(ctx, func(t *txc) error {
		opened, err := t.q.OpenSecretSend(t.ctx, store.OpenSecretSendParams{Now: now, PublicID: publicID})
		if isNoRows(err) {
			return ErrSendGone // a corrida: outro acesso levou o último lugar ou o prazo venceu agora
		}
		if err != nil {
			return mapErr(err, "abrir envio")
		}
		out = OpenedSendView{Kind: domain.SendKind(opened.Kind), Ciphertext: opened.Ciphertext, AccessCount: int(opened.AccessCount)}
		if err := t.activity(domain.SystemActor, opened.CompanyID, "send.opened", "secret_send", opened.ID,
			map[string]any{"access_count": opened.AccessCount}, ""); err != nil {
			return err
		}
		reason := sendSpentReason(opened, now)
		if reason == "" {
			return nil
		}
		if _, err := t.q.PurgeSecretSend(t.ctx, store.PurgeSecretSendParams{Now: &now, CompanyID: opened.CompanyID, ID: opened.ID}); err != nil && !isNoRows(err) {
			return mapErr(err, "apagar envio")
		}
		return t.activity(domain.SystemActor, opened.CompanyID, "send.expired", "secret_send", opened.ID,
			map[string]any{"reason": reason}, "")
	})
	return out, err
}

// checkSendAccess confere se o envio pode ser aberto agora: sem bloqueio, no prazo com acesso sobrando e, com senha,
// com a prova certa. Senha errada conta a falha.
func (s *Service) checkSendAccess(ctx context.Context, row store.SecretSend, passwordProof string, now time.Time) error {
	if row.LockedUntil != nil && row.LockedUntil.After(now) {
		return ErrSendLocked
	}
	if !sendOpenable(row, now) {
		return ErrSendGone
	}
	if row.KdfSalt != "" && !sendProofOK(row, passwordProof) {
		return s.failSendPassword(ctx, row.PublicID, now)
	}
	return nil
}

// sendSpentReason diz por que o envio deve sair do ar depois desta entrega: "limit" ou "expired". Vazio = segue.
func sendSpentReason(r store.SecretSend, now time.Time) string {
	if r.MaxAccesses != nil && r.AccessCount >= *r.MaxAccesses {
		return "limit"
	}
	if !r.ExpiresAt.After(now) {
		return "expired"
	}
	return ""
}

// failSendPassword conta uma senha errada e devolve o erro para quem abriu. Não diz quantas faltam.
func (s *Service) failSendPassword(ctx context.Context, publicID string, now time.Time) error {
	lock := now.Add(domain.SendLockFor)
	if _, err := s.read().FailSecretSendPassword(ctx, store.FailSecretSendPasswordParams{
		MaxFails: int32(domain.SendLockAfterFails), LockUntil: &lock, PublicID: publicID}); err != nil && !isNoRows(err) {
		return mapErr(err, "contar senha errada")
	}
	return ErrSendPassword
}

// PurgeExpiredSends apaga o conteúdo dos envios vencidos ou no limite de acessos (job send_purge_tick, ADR-0069 §7).
// Devolve quantos envios foram apagados nesta execução.
func (s *Service) PurgeExpiredSends(ctx context.Context) (int, error) {
	now := s.Now()
	rows, err := s.read().ListSecretSendsToPurge(ctx, store.ListSecretSendsToPurgeParams{Now: now, Lim: sendPurgeBatch})
	if err != nil {
		return 0, mapErr(err, "listar envios vencidos")
	}
	purged := 0
	for _, r := range rows {
		done := false
		err := s.withTx(ctx, func(t *txc) error {
			_, err := t.q.PurgeSecretSend(t.ctx, store.PurgeSecretSendParams{Now: &now, CompanyID: r.CompanyID, ID: r.ID})
			if isNoRows(err) {
				return nil // outro passo já apagou
			}
			if err != nil {
				return mapErr(err, "apagar envio")
			}
			done = true
			return t.activity(domain.SystemActor, r.CompanyID, "send.expired", "secret_send", r.ID,
				map[string]any{"reason": "job"}, "")
		})
		if err != nil {
			return purged, err
		}
		if done {
			purged++
		}
	}
	return purged, nil
}

// sendCreator decompõe o ator para as colunas created_by_*.
func sendCreator(actor domain.Actor) (string, *uuid.UUID) {
	return string(actor.Type), actor.ID
}

// i32p converte um *int opcional para *int32 (coluna do banco).
func i32p(p *int) *int32 {
	if p == nil {
		return nil
	}
	v := i32(*p) // o limite já foi conferido por ValidateSend (1 a 1000)
	return &v
}
