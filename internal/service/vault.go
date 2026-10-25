package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/secretref"
	"github.com/renatobardi/hanko/internal/settings"
	"github.com/renatobardi/hanko/internal/store"
)

// Cofre para agentes, fatia 1 (ADR-0068 §1–§3, issue #56): campos de um segredo, histórico por campo com
// restaurar, coleções com concessões use/view/manage e a retenção do histórico. Como em secrets.go, o valor só
// existe em claro na resposta de revelar/gerar e em memória no despacho; nada disso passa por atividade, log ou OTel.

// ---------- campos ----------

// SecretFieldView é um campo com o que a tela mostra. VisibleText vem só de campo visível (member lê);
// RevealedValue só nas respostas de revelar e de criar/rotacionar com valor gerado.
type SecretFieldView struct {
	domain.SecretField
	VisibleText   string `json:"-"`
	RevealedValue string `json:"-"`
}

// SecretFieldValueInput é o material de um campo: texto (ou semente TOTP), valor gerado ou referência do
// provider externo. Provider vazio = local.
type SecretFieldValueInput struct {
	Provider    domain.SecretProvider
	Value       string
	Generate    bool
	Gen         domain.GenOptions // opções do gerador (ADR-0068 §7.1); só com Generate
	ExternalRef string
}

// CreateSecretFieldInput cria um campo num segredo (ADR-0068 §1.1). Campo arquivo (§5.1) leva o conteúdo em File, com
// o nome e o tipo do arquivo; o valor de texto fica vazio.
type CreateSecretFieldInput struct {
	Name      string
	Kind      domain.SecretFieldKind // vazio = text
	Sensitive *bool                  // nil = true
	Password  bool                   // marca de senha (ADR-0074): só texto secreto
	Position  int
	SecretFieldValueInput
	File        []byte
	FileName    string
	ContentType string
}

// fieldAAD é o AAD de um campo (aad_version 1): copiar o texto cifrado entre campos não decifra (ADR-0068 §1.5).
func fieldAAD(secretID, fieldID uuid.UUID) string {
	return "secret/" + secretID.String() + "/field/" + fieldID.String()
}

// fieldVersionAAD é o AAD de uma linha do histórico (ADR-0068 §3.1).
func fieldVersionAAD(secretID, fieldID uuid.UUID, version int) string {
	return fieldAAD(secretID, fieldID) + "/v/" + itoa(version)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// openField decifra o valor de um campo conforme o aad_version: 0 é o AAD legado `secret/<secret_id>` do
// segredo migrado (ADR-0068 §1.7); 1 é o AAD por campo.
func (s *Service) openField(r store.SecretField, secretID uuid.UUID) ([]byte, error) {
	return s.secretBox().Open(r.ValueEnc, fieldAADFor(r.AadVersion, secretID, r.ID))
}

// openFieldVersion decifra uma linha do histórico; mesmo significado do aad_version do campo de onde veio.
func (s *Service) openFieldVersion(v store.SecretFieldVersion, secretID, fieldID uuid.UUID) ([]byte, error) {
	return s.secretBox().Open(v.ValueEnc, fieldVersionAADFor(v.AadVersion, secretID, fieldID, int(v.Version)))
}

// fieldAADFor é o AAD do valor de um campo conforme o aad_version (0 = legado do segredo migrado, 1 = por campo).
func fieldAADFor(aadVersion int16, secretID, fieldID uuid.UUID) string {
	if aadVersion == 0 {
		return secretAAD(secretID)
	}
	return fieldAAD(secretID, fieldID)
}

// fieldVersionAADFor é o AAD de uma linha do histórico conforme o aad_version do campo de onde ela veio.
func fieldVersionAADFor(aadVersion int16, secretID, fieldID uuid.UUID, version int) string {
	if aadVersion == 0 {
		return secretAAD(secretID)
	}
	return fieldVersionAAD(secretID, fieldID, version)
}

// secretFieldFromRow mapeia a linha do banco para o domínio.
func secretFieldFromRow(r store.SecretField) domain.SecretField {
	return domain.SecretField{ID: r.ID, SecretID: r.SecretID, Name: r.Name, Kind: domain.SecretFieldKind(r.Kind),
		Sensitive: r.Sensitive, Provider: domain.SecretProvider(r.Provider), ExternalRef: r.ExternalRef,
		FileName: r.FileName, ContentType: r.ContentType, SizeBytes: int(r.SizeBytes), Position: int(r.Position),
		Version: int(r.Version), RevealedAt: r.RevealedAt, LastUsedAt: r.LastUsedAt, RotatedAt: r.RotatedAt,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, Password: r.Password}
}

func fieldEventData(f store.SecretField) map[string]any {
	return map[string]any{"field": f.Name, "kind": f.Kind, "sensitive": f.Sensitive, "provider": f.Provider, "version": f.Version}
}

// fieldRow lê o campo de um segredo pelo nome (forUpdate tranca a linha na transação da rotação).
func fieldRow(ctx context.Context, q *store.Queries, companyID, secretID domain.ID, name string, forUpdate bool) (store.SecretField, error) {
	var r store.SecretField
	var err error
	if forUpdate {
		r, err = q.GetSecretFieldForUpdate(ctx, store.GetSecretFieldForUpdateParams{CompanyID: companyID, SecretID: secretID, Name: name})
	} else {
		r, err = q.GetSecretField(ctx, store.GetSecretFieldParams{CompanyID: companyID, SecretID: secretID, Name: name})
	}
	if err != nil {
		return r, mapErr(err, "get secret field")
	}
	return r, nil
}

// requireFieldWriter confere quem pode mudar os campos do segredo: admin sempre; um member com `manage` em
// alguma coleção que contém o segredo também (ADR-0068 §2.3); no escopo user, só o dono. Roda com a Queries
// da transação, para a concessão vista ser a da mesma foto.
func (s *Service) requireFieldWriter(ctx context.Context, q *store.Queries, actor domain.Actor, companyID domain.ID, secretRow store.Secret) error {
	if secretRow.Scope == string(domain.SecretScopeUser) {
		uid, ok := s.callerUserID(ctx, companyID)
		if !ok || secretRow.OwnerUserID == nil || *uid != *secretRow.OwnerUserID {
			return domain.ErrForbidden
		}
		return nil
	}
	if s.require(ctx, companyID, domain.MemberAdmin) == nil {
		return nil
	}
	uid, ok := s.callerUserID(ctx, companyID)
	if !ok {
		return domain.ErrForbidden
	}
	perms, err := q.UserCollectionPermissions(ctx, store.UserCollectionPermissionsParams{
		CompanyID: companyID, SecretID: secretRow.ID, UserID: *uid})
	if err != nil {
		return fmt.Errorf("collection permissions: %w", err)
	}
	for _, p := range perms {
		if p == string(domain.CollectionManage) {
			return nil
		}
	}
	return domain.ErrForbidden
}

// requireFieldSensitive confere quem revela, apaga, vê o histórico ou restaura um campo: o mesmo dono de
// requireFieldWriter, mais a reautenticação recente da ação sensível (ADR-0070 §1). Sem identidade no contexto é o
// modo confiável (instalação sem login): RequireReauth passa, como nas outras ações sensíveis (ADR-0070 §5).
func (s *Service) requireFieldSensitive(ctx context.Context, q *store.Queries, actor domain.Actor, companyID domain.ID, secretRow store.Secret) error {
	if err := s.requireFieldWriter(ctx, q, actor, companyID, secretRow); err != nil {
		return err
	}
	id, _ := auth.IdentityFrom(ctx)
	_, err := s.RequireReauth(id)
	return err
}

// callerUserID devolve o usuário autenticado (member da empresa) ou falso.
func (s *Service) callerUserID(ctx context.Context, companyID domain.ID) (*uuid.UUID, bool) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return nil, false
	}
	return s.identityUserID(ctx)
}

// identityUserID devolve o id do usuário autenticado no contexto, sem checar papel.
func (s *Service) identityUserID(ctx context.Context) (*uuid.UUID, bool) {
	id, ok := auth.IdentityFrom(ctx)
	if !ok {
		return nil, false
	}
	uid := id.User.ID
	return &uid, true
}

// prepareFieldMaterial valida o material de um campo texto ou totp e cifra com o AAD do campo (aad_version 1). Campo
// arquivo não passa por aqui: a criação grava o conteúdo com createFileField, e a troca de conteúdo não existe (o
// campo se apaga e se cria de novo).
func (s *Service) prepareFieldMaterial(secretID, fieldID uuid.UUID, in SecretFieldValueInput, kind domain.SecretFieldKind) (secretMaterial, error) {
	if kind == domain.SecretFieldFile {
		return secretMaterial{}, domain.Invalid("kind", "campo arquivo não se troca: apague o campo e crie outro com o arquivo novo")
	}
	if providerOf(in) == domain.SecretProviderVault && kind == domain.SecretFieldTOTP {
		return secretMaterial{}, domain.Invalid("provider", "semente TOTP é guardada cifrada no Hanko (provider local)")
	}
	m, err := s.prepareMaterialAAD(fieldAAD(secretID, fieldID), in.Provider, in.Value, in.Generate, in.Gen, in.ExternalRef)
	if err != nil {
		return m, err
	}
	if kind == domain.SecretFieldTOTP && !m.revealed && m.provider == domain.SecretProviderLocal {
		if e := validTOTPSeed(in.Value); e != nil {
			return secretMaterial{}, e
		}
	}
	return m, nil
}

var (
	base32SeedRe = regexp.MustCompile(`^[A-Za-z2-7]+=*$`) //nolint:gosec // formato de semente, não credencial
	otpauthRe    = regexp.MustCompile(`^otpauth://totp/`)
)

// validTOTPSeed aceita semente base32 ou URI otpauth:// (ADR-0068 §4.3).
func validTOTPSeed(v string) error {
	if v == "" || len(v) > domain.MaxSecretFieldTextBytes {
		return domain.Invalid("value", "semente TOTP: base32 ou URI otpauth://, até 8 KiB")
	}
	if otpauthRe.MatchString(v) {
		return nil
	}
	if !base32SeedRe.MatchString(v) {
		return domain.Invalid("value", "semente TOTP: base32 (A-Z, 2-7, =) ou URI otpauth://")
	}
	return nil
}

// CreateSecretField cria um campo num segredo da empresa ou do usuário.
func (s *Service) CreateSecretField(ctx context.Context, actor domain.Actor, companyID domain.ID, scope domain.SecretScope, secretName string, in CreateSecretFieldInput) (SecretFieldView, error) {
	owner, err := s.secretCaller(ctx, actor, companyID, scope, false)
	if err != nil {
		return SecretFieldView{}, err
	}
	var out SecretFieldView
	err = s.withTx(ctx, func(t *txc) error {
		sec, err := getSecretRow(t.ctx, t.q, companyID, scope, owner, secretName, true)
		if err != nil {
			return err
		}
		if err := s.requireFieldWriter(ctx, t.q, actor, companyID, sec); err != nil {
			return err
		}
		out, err = s.createFieldTx(t, actor, companyID, sec, in)
		return err
	})
	return out, err
}

// createFieldTx cria o campo na transação, com o segredo já lido e a permissão já conferida por quem chama: limite de
// campos, regras do campo, cifra e fato `secret.field_created`.
func (s *Service) createFieldTx(t *txc, actor domain.Actor, companyID domain.ID, sec store.Secret, in CreateSecretFieldInput) (SecretFieldView, error) {
	fields, err := t.q.ListSecretFields(t.ctx, store.ListSecretFieldsParams{CompanyID: companyID, SecretID: sec.ID})
	if err != nil {
		return SecretFieldView{}, fmt.Errorf("list fields: %w", err)
	}
	if len(fields) >= domain.MaxSecretFields {
		return SecretFieldView{}, domain.Invalid("name", fmt.Sprintf("o segredo já tem %d campos (o máximo)", domain.MaxSecretFields))
	}
	kind := in.Kind
	if kind == "" {
		kind = domain.SecretFieldText
	}
	sensitive := true
	if in.Sensitive != nil {
		sensitive = *in.Sensitive
	}
	if err := domain.ValidateSecretField(in.Name, kind, sensitive, providerOf(in.SecretFieldValueInput)); err != nil {
		return SecretFieldView{}, err
	}
	if err := domain.ValidatePasswordMark(kind, sensitive, in.Password); err != nil {
		return SecretFieldView{}, err
	}
	pos := in.Position
	if pos == 0 {
		pos = len(fields)
	}
	id := domain.NewID()
	var row store.SecretField
	var plain string
	if kind == domain.SecretFieldFile {
		row, err = s.createFileField(t, companyID, sec, id, in, pos, sensitive)
		if err != nil {
			return SecretFieldView{}, err
		}
	} else {
		m, err := s.prepareFieldMaterial(sec.ID, id, in.SecretFieldValueInput, kind)
		if err != nil {
			return SecretFieldView{}, err
		}
		var revealedAt *time.Time
		if m.revealed {
			n := t.now
			revealedAt = &n
		}
		row, err = t.q.CreateSecretField(t.ctx, store.CreateSecretFieldParams{ID: id, CompanyID: companyID, SecretID: sec.ID,
			Name: in.Name, Kind: string(kind), Sensitive: sensitive, Provider: string(m.provider), ValueEnc: m.enc,
			ExternalRef: m.ext, ValueHmac: m.mac, AadVersion: 1, Position: i32(pos), RevealedAt: revealedAt, Now: t.now,
			Password: in.Password})
		if err != nil {
			return SecretFieldView{}, mapErr(err, "create secret field")
		}
		plain = m.plaintext
	}
	out := SecretFieldView{SecretField: secretFieldFromRow(row), RevealedValue: plain}
	return out, t.activity(actor, companyID, "secret.field_created", "secret", sec.ID, fieldEventData(row), "")
}

// providerOf devolve o provider do input com o padrão local.
func providerOf(in SecretFieldValueInput) domain.SecretProvider {
	if in.Provider == "" {
		return domain.SecretProviderLocal
	}
	return in.Provider
}

// ListSecretFields devolve os campos do segredo: metadados para qualquer membro (escopo user: o dono) e o
// texto dos campos visíveis (ADR-0068 §1.3). Valor de campo secreto nunca sai por aqui.
func (s *Service) ListSecretFields(ctx context.Context, companyID domain.ID, scope domain.SecretScope, secretName string) ([]SecretFieldView, error) {
	owner, err := s.secretCaller(ctx, domain.HumanActor, companyID, scope, false)
	if err != nil {
		return nil, err
	}
	q := s.read()
	sec, err := getSecretRow(ctx, q, companyID, scope, owner, secretName, false)
	if err != nil {
		return nil, err
	}
	rows, err := q.ListSecretFields(ctx, store.ListSecretFieldsParams{CompanyID: companyID, SecretID: sec.ID})
	if err != nil {
		return nil, fmt.Errorf("list secret fields: %w", err)
	}
	out := make([]SecretFieldView, 0, len(rows))
	for _, r := range rows {
		v := SecretFieldView{SecretField: secretFieldFromRow(r)}
		if !r.Sensitive && r.Provider == string(domain.SecretProviderLocal) && r.ValueEnc != "" {
			plain, err := s.openField(r, sec.ID)
			if err != nil {
				return nil, domain.Invalid("secrets", "não foi possível decifrar um campo visível: "+err.Error())
			}
			v.VisibleText = string(plain)
		}
		out = append(out, v)
	}
	return out, nil
}

// UpdateSecretFieldMeta muda a marca de visível, a marca de senha (ADR-0074) e a posição de um campo. Tirar o visível
// não exige chave; marcar visível exige (o texto passa a ser lido na listagem).
func (s *Service) UpdateSecretFieldMeta(ctx context.Context, actor domain.Actor, companyID domain.ID, scope domain.SecretScope, secretName, fieldName string, sensitive, password *bool, position *int) (SecretFieldView, error) {
	owner, err := s.secretCaller(ctx, actor, companyID, scope, false)
	if err != nil {
		return SecretFieldView{}, err
	}
	var out SecretFieldView
	err = s.withTx(ctx, func(t *txc) error {
		sec, f, err := s.lockField(t, companyID, scope, owner, secretName, fieldName)
		if err != nil {
			return err
		}
		if err := s.requireFieldWriter(ctx, t.q, actor, companyID, sec); err != nil {
			return err
		}
		ns, np, nw := f.Sensitive, int(f.Position), f.Password
		if sensitive != nil {
			if !*sensitive && domain.SecretFieldKind(f.Kind).AlwaysSensitive() {
				return domain.Invalid("sensitive", "campo "+f.Kind+" é sempre secreto")
			}
			if !*sensitive && f.Name == domain.DefaultSecretField {
				return domain.Invalid("sensitive", "o campo value é o próprio segredo: não vira visível")
			}
			ns = *sensitive
		}
		if password != nil {
			nw = *password
		}
		if err := domain.ValidatePasswordMark(domain.SecretFieldKind(f.Kind), ns, nw); err != nil {
			return err
		}
		if position != nil {
			np = *position
		}
		row, err := t.q.UpdateSecretFieldMeta(t.ctx, store.UpdateSecretFieldMetaParams{ID: f.ID, CompanyID: companyID,
			Sensitive: ns, Position: i32(np), Password: nw, Now: t.now})
		if err != nil {
			return mapErr(err, "update secret field")
		}
		out = SecretFieldView{SecretField: secretFieldFromRow(row)}
		return t.activity(actor, companyID, "secret.field_updated", "secret", sec.ID, fieldEventData(row), "")
	})
	return out, err
}

// lockField traz o segredo e o campo dele travados para escrita.
func (s *Service) lockField(t *txc, companyID domain.ID, scope domain.SecretScope, owner *uuid.UUID, secretName, fieldName string) (store.Secret, store.SecretField, error) {
	sec, err := getSecretRow(t.ctx, t.q, companyID, scope, owner, secretName, true)
	if err != nil {
		return sec, store.SecretField{}, err
	}
	f, err := fieldRow(t.ctx, t.q, companyID, sec.ID, fieldName, true)
	if err != nil {
		return sec, f, err
	}
	return sec, f, nil
}

// RotateSecretField troca o valor de um campo; o anterior vai para o histórico cifrado (ADR-0068 §3.1).
func (s *Service) RotateSecretField(ctx context.Context, actor domain.Actor, companyID domain.ID, scope domain.SecretScope, secretName, fieldName string, in SecretFieldValueInput) (SecretFieldView, error) {
	owner, err := s.secretCaller(ctx, actor, companyID, scope, false)
	if err != nil {
		return SecretFieldView{}, err
	}
	var out SecretFieldView
	err = s.withTx(ctx, func(t *txc) error {
		sec, f, err := s.lockField(t, companyID, scope, owner, secretName, fieldName)
		if err != nil {
			return err
		}
		if err := s.requireFieldWriter(ctx, t.q, actor, companyID, sec); err != nil {
			return err
		}
		m, err := s.prepareFieldMaterial(sec.ID, f.ID, in, domain.SecretFieldKind(f.Kind))
		if err != nil {
			return err
		}
		if m.provider == domain.SecretProviderLocal {
			if m.enc, err = s.sealMaterialFor(m, fieldAAD(sec.ID, f.ID)); err != nil {
				return err
			}
		}
		row, err := s.rotateFieldTx(t, actor, companyID, f, m)
		if err != nil {
			return err
		}
		if f.Name == domain.DefaultSecretField {
			if err := s.mirrorValueTx(t, companyID, sec, m, row.RevealedAt); err != nil {
				return err
			}
		}
		out = SecretFieldView{SecretField: secretFieldFromRow(row), RevealedValue: m.plaintext}
		return t.activity(actor, companyID, "secret.field_updated", "secret", sec.ID, fieldEventData(row), "")
	})
	return out, err
}

// rotateFieldTx guarda o valor atual no histórico e grava o material novo (mesma transação). m.enc já vem cifrado
// com o AAD do campo; quem chama emite o fato e espelha o `value` quando for o caso. O valor anterior é recifrado
// com o AAD da versão (`.../field/<campo>/v/<versão>`, ADR-0068 §3.1): copiar a linha do campo não decifraria no histórico.
func (s *Service) rotateFieldTx(t *txc, actor domain.Actor, companyID domain.ID, f store.SecretField, m secretMaterial) (store.SecretField, error) {
	archived := f.ValueEnc
	if f.Provider == string(domain.SecretProviderLocal) {
		plain, err := s.openField(f, f.SecretID)
		if err != nil {
			return store.SecretField{}, domain.Invalid("secrets", "não foi possível decifrar o valor anterior para o histórico: "+err.Error())
		}
		if archived, err = s.secretBox().Seal(plain, fieldVersionAAD(f.SecretID, f.ID, int(f.Version))); err != nil {
			return store.SecretField{}, fmt.Errorf("seal field version: %w", err)
		}
	}
	if _, err := t.q.CreateSecretFieldVersion(t.ctx, store.CreateSecretFieldVersionParams{
		ID: domain.NewID(), CompanyID: companyID, FieldID: f.ID, Version: f.Version, Provider: f.Provider,
		ValueEnc: archived, ExternalRef: f.ExternalRef, BlobKey: f.BlobKey, FileName: f.FileName,
		ContentType: f.ContentType, SizeBytes: f.SizeBytes, AadVersion: 1, RevealedAt: f.RevealedAt,
		ReplacedByType: string(actor.Type), ReplacedByID: actor.ID, CreatedAt: f.RotatedAt, Now: t.now}); err != nil {
		return store.SecretField{}, mapErr(err, "archive secret field")
	}
	var revealedAt *time.Time
	if m.revealed {
		n := t.now
		revealedAt = &n
	}
	row, err := t.q.RotateSecretField(t.ctx, store.RotateSecretFieldParams{ID: f.ID, CompanyID: companyID,
		Provider: string(m.provider), ValueEnc: m.enc, ExternalRef: m.ext, ValueHmac: m.mac, AadVersion: 1, RevealedAt: revealedAt, Now: t.now})
	if err != nil {
		return store.SecretField{}, mapErr(err, "rotate secret field")
	}
	return row, nil
}

// sealMaterialFor cifra o valor de um material local com o AAD dado; o do cofre externo não tem texto cifrado.
func (s *Service) sealMaterialFor(m secretMaterial, aad string) (string, error) {
	if m.provider != domain.SecretProviderLocal {
		return "", nil
	}
	enc, err := s.secretBox().Seal(m.plain, aad)
	if err != nil {
		return "", fmt.Errorf("seal field: %w", err)
	}
	return enc, nil
}

// mirrorValueTx põe o valor atual de `value` também nas colunas de secrets, cifrado com o AAD legado do segredo.
// Enquanto a migration que tira essas colunas não sai (ADR-0068 §1.7), os dois lugares guardam o mesmo valor, e o
// histórico guarda só o anterior: nenhuma cópia antiga fica fora da retenção.
func (s *Service) mirrorValueTx(t *txc, companyID domain.ID, sec store.Secret, m secretMaterial, revealedAt *time.Time) error {
	legacy, err := s.sealMaterialFor(m, secretAAD(sec.ID))
	if err != nil {
		return err
	}
	if _, err := t.q.RotateSecret(t.ctx, store.RotateSecretParams{ID: sec.ID, CompanyID: companyID,
		Provider: string(m.provider), ValueEnc: legacy, ExternalRef: m.ext, RevealedAt: revealedAt,
		Description: sec.Description, Now: t.now}); err != nil {
		return mapErr(err, "rotate secret")
	}
	return nil
}

// RevealSecretField mostra o valor de um campo UMA vez por versão (ADR-0050 §6); exige reautenticação recente
// (ADR-0070 §1).
func (s *Service) RevealSecretField(ctx context.Context, actor domain.Actor, companyID domain.ID, scope domain.SecretScope, secretName, fieldName string) (SecretFieldView, error) {
	owner, err := s.secretCaller(ctx, actor, companyID, scope, false)
	if err != nil {
		return SecretFieldView{}, err
	}
	var out SecretFieldView
	err = s.withTx(ctx, func(t *txc) error {
		sec, f, err := s.lockField(t, companyID, scope, owner, secretName, fieldName)
		if err != nil {
			return err
		}
		if err := s.requireFieldSensitive(ctx, t.q, actor, companyID, sec); err != nil {
			return err
		}
		if f.Provider != string(domain.SecretProviderLocal) {
			return domain.Invalid("provider", "o valor deste campo mora no cofre externo; leia-o lá")
		}
		if f.Kind == string(domain.SecretFieldFile) {
			return domain.Invalid("kind", "campo arquivo não se revela: a persona recebe o arquivo por ${secretfile:} ou hanko_secret_get")
		}
		row, err := t.q.MarkSecretFieldRevealed(t.ctx, store.MarkSecretFieldRevealedParams{ID: f.ID, CompanyID: companyID, Now: &t.now})
		if isNoRows(err) {
			return fmt.Errorf("campo %q de %q: o valor desta versão já foi revelado; rotacione para obter outro: %w", fieldName, secretName, domain.ErrConflict)
		}
		if err != nil {
			return mapErr(err, "reveal secret field")
		}
		plain, err := s.openField(f, sec.ID)
		if err != nil {
			return domain.Invalid("secrets", "não foi possível decifrar o campo: "+err.Error())
		}
		if f.Name == domain.DefaultSecretField {
			if _, err := t.q.MarkSecretRevealed(t.ctx, store.MarkSecretRevealedParams{ID: sec.ID, CompanyID: companyID, Now: &t.now}); err != nil && !isNoRows(err) {
				return mapErr(err, "reveal secret")
			}
		}
		out = SecretFieldView{SecretField: secretFieldFromRow(row), RevealedValue: string(plain)}
		return t.activity(actor, companyID, "secret.revealed", "secret", sec.ID, fieldEventData(row), "")
	})
	return out, err
}

// DeleteSecretField apaga um campo e o histórico dele; exige reautenticação recente (ADR-0070 §1). O campo `value` não se apaga: é o padrão da referência
// ${secret:X}; apagar o item é o caminho.
func (s *Service) DeleteSecretField(ctx context.Context, actor domain.Actor, companyID domain.ID, scope domain.SecretScope, secretName, fieldName string) error {
	owner, err := s.secretCaller(ctx, actor, companyID, scope, false)
	if err != nil {
		return err
	}
	return s.withTx(ctx, func(t *txc) error {
		sec, f, err := s.lockField(t, companyID, scope, owner, secretName, fieldName)
		if err != nil {
			return err
		}
		if err := s.requireFieldSensitive(ctx, t.q, actor, companyID, sec); err != nil {
			return err
		}
		if f.Name == domain.DefaultSecretField {
			return domain.Invalid("field", "o campo value é o padrão da referência ${secret:X}; apague o segredo")
		}
		n, err := t.q.DeleteSecretField(t.ctx, store.DeleteSecretFieldParams{ID: f.ID, CompanyID: companyID})
		if err != nil {
			return mapErr(err, "delete secret field")
		}
		if n == 0 {
			return fmt.Errorf("delete secret field: %w", domain.ErrNotFound)
		}
		if f.Kind == string(domain.SecretFieldFile) {
			t.dropFieldBlobAfterCommit(companyID, f.ID)
		}
		return t.activity(actor, companyID, "secret.field_deleted", "secret", sec.ID, fieldEventData(f), "")
	})
}

// ---------- histórico ----------

// ListSecretFieldHistory devolve as versões anteriores de um campo (metadados; o valor só em RevealSecretFieldVersion).
// Exige reautenticação recente (ADR-0070 §1).
func (s *Service) ListSecretFieldHistory(ctx context.Context, actor domain.Actor, companyID domain.ID, scope domain.SecretScope, secretName, fieldName string) ([]domain.SecretFieldVersion, error) {
	owner, err := s.secretCaller(ctx, actor, companyID, scope, false)
	if err != nil {
		return nil, err
	}
	q := s.read()
	sec, err := getSecretRow(ctx, q, companyID, scope, owner, secretName, false)
	if err != nil {
		return nil, err
	}
	if err := s.requireFieldSensitive(ctx, q, actor, companyID, sec); err != nil {
		return nil, err
	}
	f, err := fieldRow(ctx, q, companyID, sec.ID, fieldName, false)
	if err != nil {
		return nil, err
	}
	rows, err := q.ListSecretFieldVersions(ctx, store.ListSecretFieldVersionsParams{CompanyID: companyID, FieldID: f.ID})
	if err != nil {
		return nil, fmt.Errorf("list secret field versions: %w", err)
	}
	out := make([]domain.SecretFieldVersion, 0, len(rows))
	for _, r := range rows {
		out = append(out, domain.SecretFieldVersion{ID: r.ID, FieldID: r.FieldID, Version: int(r.Version),
			Provider: domain.SecretProvider(r.Provider), RevealedAt: r.RevealedAt,
			ReplacedBy: actorOf(r.ReplacedByType, r.ReplacedByID), CreatedAt: r.CreatedAt, ReplacedAt: r.ReplacedAt})
	}
	return out, nil
}

// RevealSecretFieldVersion mostra uma versão anterior UMA vez (ADR-0068 §3.4); exige reautenticação recente
// (ADR-0070 §1).
func (s *Service) RevealSecretFieldVersion(ctx context.Context, actor domain.Actor, companyID domain.ID, scope domain.SecretScope, secretName, fieldName string, version int) (domain.SecretFieldVersion, string, error) {
	owner, err := s.secretCaller(ctx, actor, companyID, scope, false)
	if err != nil {
		return domain.SecretFieldVersion{}, "", err
	}
	var out domain.SecretFieldVersion
	var val string
	err = s.withTx(ctx, func(t *txc) error {
		sec, f, err := s.lockField(t, companyID, scope, owner, secretName, fieldName)
		if err != nil {
			return err
		}
		if err := s.requireFieldSensitive(ctx, t.q, actor, companyID, sec); err != nil {
			return err
		}
		v, err := t.q.GetSecretFieldVersion(t.ctx, store.GetSecretFieldVersionParams{CompanyID: companyID, FieldID: f.ID, Version: i32(version)})
		if err != nil {
			return mapErr(err, "get secret field version")
		}
		if v.Provider != string(domain.SecretProviderLocal) {
			return domain.Invalid("provider", "o valor desta versão mora no cofre externo")
		}
		row, err := t.q.MarkSecretFieldVersionRevealed(t.ctx, store.MarkSecretFieldVersionRevealedParams{ID: v.ID, CompanyID: companyID, Now: &t.now})
		if isNoRows(err) {
			return fmt.Errorf("versão %d do campo %q já foi revelada: %w", version, fieldName, domain.ErrConflict)
		}
		if err != nil {
			return mapErr(err, "reveal secret field version")
		}
		plain, err := s.openFieldVersion(v, sec.ID, f.ID)
		if err != nil {
			return domain.Invalid("secrets", "não foi possível decifrar a versão: "+err.Error())
		}
		out = domain.SecretFieldVersion{ID: row.ID, FieldID: row.FieldID, Version: int(row.Version),
			Provider: domain.SecretProvider(row.Provider), RevealedAt: row.RevealedAt,
			ReplacedBy: actorOf(row.ReplacedByType, row.ReplacedByID), CreatedAt: row.CreatedAt, ReplacedAt: row.ReplacedAt}
		val = string(plain)
		return t.activity(actor, companyID, "secret.revealed", "secret", sec.ID,
			map[string]any{"field": f.Name, "version": row.Version, "historic": true}, "")
	})
	return out, val, err
}

// RestoreSecretFieldVersion copia uma versão anterior como versão NOVA do campo (ADR-0068 §3.3): nada é apagado.
// Exige reautenticação recente (ADR-0070 §1).
func (s *Service) RestoreSecretFieldVersion(ctx context.Context, actor domain.Actor, companyID domain.ID, scope domain.SecretScope, secretName, fieldName string, version int) (SecretFieldView, error) {
	owner, err := s.secretCaller(ctx, actor, companyID, scope, false)
	if err != nil {
		return SecretFieldView{}, err
	}
	var out SecretFieldView
	err = s.withTx(ctx, func(t *txc) error {
		sec, f, err := s.lockField(t, companyID, scope, owner, secretName, fieldName)
		if err != nil {
			return err
		}
		if err := s.requireFieldSensitive(ctx, t.q, actor, companyID, sec); err != nil {
			return err
		}
		v, err := t.q.GetSecretFieldVersion(t.ctx, store.GetSecretFieldVersionParams{CompanyID: companyID, FieldID: f.ID, Version: i32(version)})
		if err != nil {
			return mapErr(err, "get secret field version")
		}
		m := secretMaterial{provider: domain.SecretProvider(v.Provider), ext: v.ExternalRef}
		if m.provider == domain.SecretProviderLocal {
			plain, err := s.openFieldVersion(v, sec.ID, f.ID)
			if err != nil {
				return domain.Invalid("secrets", "não foi possível decifrar a versão: "+err.Error())
			}
			m.plain = plain
			if m.enc, err = s.sealMaterialFor(m, fieldAAD(sec.ID, f.ID)); err != nil {
				return err
			}
		}
		row, err := s.rotateFieldTx(t, actor, companyID, f, m)
		if err != nil {
			return err
		}
		if f.Name == domain.DefaultSecretField {
			if err := s.mirrorValueTx(t, companyID, sec, m, row.RevealedAt); err != nil {
				return err
			}
		}
		out = SecretFieldView{SecretField: secretFieldFromRow(row)}
		return t.activity(actor, companyID, "secret.field_restored", "secret", sec.ID,
			map[string]any{"field": f.Name, "from_version": version, "version": row.Version}, "")
	})
	return out, err
}

// PruneSecretFieldHistory apaga o histórico que passou da retenção (ADR-0068 §3.2): as últimas N versões OU as
// de até D dias, o que guardar menos. Roda como job periódico; devolve quantas versões saíram.
func (s *Service) PruneSecretFieldHistory(ctx context.Context) (int, error) {
	keepVersions := domain.DefaultHistoryKeepVersions
	keepDays := domain.DefaultHistoryKeepDays
	if s.Settings != nil {
		keepVersions = int(s.Settings.Int(settings.SecretsHistoryVersions))
		keepDays = int(s.Settings.Int(settings.SecretsHistoryDays))
	}
	q := s.read()
	fields, err := q.ListFieldsWithHistory(ctx)
	if err != nil {
		return 0, fmt.Errorf("list fields with history: %w", err)
	}
	total := 0
	for _, f := range fields {
		rows, err := q.ListSecretFieldVersions(ctx, store.ListSecretFieldVersionsParams{CompanyID: f.CompanyID, FieldID: f.FieldID})
		if err != nil {
			return total, fmt.Errorf("list versions: %w", err)
		}
		at := make([]time.Time, len(rows))
		for i, r := range rows {
			at[i] = r.ReplacedAt
		}
		drop := domain.PruneHistory(at, s.Now().UTC(), keepVersions, keepDays)
		if len(drop) == 0 {
			continue
		}
		ids := make([]uuid.UUID, 0, len(drop))
		for _, i := range drop {
			ids = append(ids, rows[i].ID)
		}
		err = s.withTx(ctx, func(t *txc) error {
			n, err := t.q.DeleteSecretFieldVersions(t.ctx, store.DeleteSecretFieldVersionsParams{CompanyID: f.CompanyID, Ids: ids})
			if err != nil {
				return mapErr(err, "delete secret field versions")
			}
			total += int(n)
			return t.activity(domain.SystemActor, f.CompanyID, "secret.history_pruned", "secret", f.SecretID,
				map[string]any{"removed": n}, "")
		})
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// ---------- coleções ----------

// CollectionView é uma coleção com quantos segredos tem.
type CollectionView struct {
	domain.SecretCollection
	Secrets int `json:"secrets"`
}

// CollectionGrantView é uma concessão com o nome do sujeito (slug da persona ou e-mail do humano).
type CollectionGrantView struct {
	domain.CollectionGrant
	SubjectName string `json:"subject_name,omitempty"`
}

// CollectionInput cria ou muda uma coleção.
type CollectionInput struct {
	Name            string
	Description     string
	RotateAfterDays *int
}

// GrantInput concede a coleção a um sujeito (ADR-0068 §2.2).
type GrantInput struct {
	SubjectType domain.CollectionSubject
	Subject     string // slug ou id da persona; e-mail ou id do humano; vazio em all_personas
	Permission  domain.CollectionPermission
}

func collectionFromRow(r store.SecretCollection) domain.SecretCollection {
	return domain.SecretCollection{ID: r.ID, CompanyID: r.CompanyID, Name: r.Name, Description: r.Description,
		IsDefault: r.IsDefault, RotateAfterDays: rotateDays(r.RotateAfterDays), CreatedBy: actorOf(r.CreatedByType, r.CreatedByID),
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

func rotateDays(v *int32) *int {
	if v == nil {
		return nil
	}
	n := int(*v)
	return &n
}

// resolveCollection acha a coleção por nome ou id.
func (s *Service) resolveCollection(ctx context.Context, q *store.Queries, companyID domain.ID, idOrName string) (store.SecretCollection, error) {
	if id, err := uuid.Parse(idOrName); err == nil {
		r, err := q.GetSecretCollection(ctx, store.GetSecretCollectionParams{CompanyID: companyID, ID: id})
		if err != nil {
			return r, mapErr(err, "get secret collection")
		}
		return r, nil
	}
	r, err := q.GetSecretCollectionByName(ctx, store.GetSecretCollectionByNameParams{CompanyID: companyID, Name: idOrName})
	if err != nil {
		return r, mapErr(err, "get secret collection")
	}
	return r, nil
}

// validateCollection confere nome e prazo da coleção (a mesma constraint do banco).
func validateCollection(in CollectionInput) (string, string, error) {
	name, desc := trimSpace(in.Name), trimSpace(in.Description)
	if name == "" || len(name) > 64 {
		return "", "", domain.Invalid("name", "nome da coleção: 1 a 64 caracteres")
	}
	if len(desc) > 300 {
		return "", "", domain.Invalid("description", "descrição: até 300 caracteres")
	}
	if in.RotateAfterDays != nil && (*in.RotateAfterDays < 1 || *in.RotateAfterDays > 3650) {
		return "", "", domain.Invalid("rotate_after_days", "prazo de rotação: 1 a 3650 dias")
	}
	return name, desc, nil
}

func trimSpace(s string) string { return strings.TrimSpace(s) }

// CreateCollection cria uma coleção (admin).
func (s *Service) CreateCollection(ctx context.Context, actor domain.Actor, companyID domain.ID, in CollectionInput) (CollectionView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return CollectionView{}, err
	}
	name, desc, err := validateCollection(in)
	if err != nil {
		return CollectionView{}, err
	}
	var out CollectionView
	err = s.withTx(ctx, func(t *txc) error {
		if _, err := t.q.GetCompanyByID(t.ctx, companyID); err != nil {
			return mapErr(err, "company")
		}
		var rot *int32
		if in.RotateAfterDays != nil {
			rot = ptr(i32(*in.RotateAfterDays))
		}
		row, err := t.q.CreateSecretCollection(t.ctx, store.CreateSecretCollectionParams{ID: domain.NewID(), CompanyID: companyID,
			Name: name, Description: desc, IsDefault: false, RotateAfterDays: rot,
			CreatedByType: string(actor.Type), CreatedByID: actor.ID, Now: t.now})
		if err != nil {
			return mapErr(err, "create secret collection")
		}
		out = CollectionView{SecretCollection: collectionFromRow(row)}
		return t.activity(actor, companyID, "collection.created", "collection", row.ID,
			map[string]any{"name": row.Name}, "")
	})
	return out, err
}

// ListCollections devolve as coleções da empresa com a contagem de segredos (member).
func (s *Service) ListCollections(ctx context.Context, companyID domain.ID) ([]CollectionView, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return nil, err
	}
	rows, err := s.read().ListSecretCollectionsWithCounts(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list secret collections: %w", err)
	}
	out := make([]CollectionView, len(rows))
	for i, r := range rows {
		c := store.SecretCollection{ID: r.ID, CompanyID: r.CompanyID, Name: r.Name, Description: r.Description,
			IsDefault: r.IsDefault, RotateAfterDays: r.RotateAfterDays, CreatedByType: r.CreatedByType,
			CreatedByID: r.CreatedByID, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
		out[i] = CollectionView{SecretCollection: collectionFromRow(c), Secrets: int(r.SecretsCount)}
	}
	return out, nil
}

// GetCollection devolve uma coleção (member).
func (s *Service) GetCollection(ctx context.Context, companyID domain.ID, idOrName string) (CollectionView, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return CollectionView{}, err
	}
	q := s.read()
	r, err := s.resolveCollection(ctx, q, companyID, idOrName)
	if err != nil {
		return CollectionView{}, err
	}
	items, err := q.ListCollectionSecrets(ctx, store.ListCollectionSecretsParams{CompanyID: companyID, CollectionID: r.ID})
	if err != nil {
		return CollectionView{}, fmt.Errorf("list collection secrets: %w", err)
	}
	return CollectionView{SecretCollection: collectionFromRow(r), Secrets: len(items)}, nil
}

// UpdateCollection muda nome, descrição e prazo de rotação (admin). A padrão só muda descrição e prazo.
func (s *Service) UpdateCollection(ctx context.Context, actor domain.Actor, companyID domain.ID, idOrName string, in CollectionInput) (CollectionView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return CollectionView{}, err
	}
	name, desc, err := validateCollection(in)
	if err != nil {
		return CollectionView{}, err
	}
	var out CollectionView
	err = s.withTx(ctx, func(t *txc) error {
		cur, err := s.resolveCollection(t.ctx, t.q, companyID, idOrName)
		if err != nil {
			return err
		}
		if cur.IsDefault && name != cur.Name {
			return domain.Invalid("name", "o nome da coleção padrão não muda")
		}
		var rot *int32
		if in.RotateAfterDays != nil {
			rot = ptr(i32(*in.RotateAfterDays))
		}
		row, err := t.q.UpdateSecretCollection(t.ctx, store.UpdateSecretCollectionParams{CompanyID: companyID, ID: cur.ID,
			Name: name, Description: desc, RotateAfterDays: rot, Now: t.now})
		if err != nil {
			return mapErr(err, "update secret collection")
		}
		out = CollectionView{SecretCollection: collectionFromRow(row)}
		return t.activity(actor, companyID, "collection.updated", "collection", row.ID,
			map[string]any{"name": row.Name}, "")
	})
	return out, err
}

// DeleteCollection apaga uma coleção; os segredos continuam. A padrão não se apaga (ADR-0068 §2.6).
func (s *Service) DeleteCollection(ctx context.Context, actor domain.Actor, companyID domain.ID, idOrName string) error {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return err
	}
	return s.withTx(ctx, func(t *txc) error {
		cur, err := s.resolveCollection(t.ctx, t.q, companyID, idOrName)
		if err != nil {
			return err
		}
		if cur.IsDefault {
			return domain.Invalid("collection", "a coleção padrão não se apaga")
		}
		if n, err := t.q.DeleteSecretCollection(t.ctx, store.DeleteSecretCollectionParams{CompanyID: companyID, ID: cur.ID}); err != nil || n == 0 {
			return mapErr(err, "delete secret collection")
		}
		return t.activity(actor, companyID, "collection.deleted", "collection", cur.ID,
			map[string]any{"name": cur.Name}, "")
	})
}

// AddSecretToCollection põe um segredo da empresa na coleção (admin ou manage nela).
func (s *Service) AddSecretToCollection(ctx context.Context, actor domain.Actor, companyID domain.ID, idOrName, secretName string) error {
	if err := s.requireCollectionManager(ctx, companyID, idOrName); err != nil {
		return err
	}
	return s.withTx(ctx, func(t *txc) error {
		col, err := s.resolveCollection(t.ctx, t.q, companyID, idOrName)
		if err != nil {
			return err
		}
		sec, err := getSecretRow(t.ctx, t.q, companyID, domain.SecretScopeCompany, nil, secretName, true)
		if err != nil {
			return err
		}
		if err := t.q.AddSecretToCollection(t.ctx, store.AddSecretToCollectionParams{CompanyID: companyID,
			CollectionID: col.ID, SecretID: sec.ID, Now: t.now}); err != nil {
			return mapErr(err, "add secret to collection")
		}
		return t.activity(actor, companyID, "collection.item_added", "collection", col.ID,
			map[string]any{"secret": sec.Name}, "")
	})
}

// RemoveSecretFromCollection tira um segredo da coleção (admin ou manage nela).
func (s *Service) RemoveSecretFromCollection(ctx context.Context, actor domain.Actor, companyID domain.ID, idOrName, secretName string) error {
	if err := s.requireCollectionManager(ctx, companyID, idOrName); err != nil {
		return err
	}
	return s.withTx(ctx, func(t *txc) error {
		col, err := s.resolveCollection(t.ctx, t.q, companyID, idOrName)
		if err != nil {
			return err
		}
		sec, err := getSecretRow(t.ctx, t.q, companyID, domain.SecretScopeCompany, nil, secretName, true)
		if err != nil {
			return err
		}
		if n, err := t.q.RemoveSecretFromCollection(t.ctx, store.RemoveSecretFromCollectionParams{CompanyID: companyID,
			CollectionID: col.ID, SecretID: sec.ID}); err != nil || n == 0 {
			return mapErr(err, "remove secret from collection")
		}
		return t.activity(actor, companyID, "collection.item_removed", "collection", col.ID,
			map[string]any{"secret": sec.Name}, "")
	})
}

// requireCollectionManager confere admin ou `manage` na coleção para mexer nos itens dela.
func (s *Service) requireCollectionManager(ctx context.Context, companyID domain.ID, idOrName string) error {
	if s.require(ctx, companyID, domain.MemberAdmin) == nil {
		return nil
	}
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return err
	}
	uid, ok := s.identityUserID(ctx)
	if !ok {
		return domain.ErrForbidden
	}
	col, err := s.resolveCollection(ctx, s.read(), companyID, idOrName)
	if err != nil {
		return err
	}
	grants, err := s.read().ListCollectionGrants(ctx, store.ListCollectionGrantsParams{CompanyID: companyID, CollectionID: col.ID})
	if err != nil {
		return fmt.Errorf("list collection grants: %w", err)
	}
	for _, g := range grants {
		if g.SubjectType == string(domain.CollectionSubjectUser) && g.SubjectID != nil && *g.SubjectID == *uid &&
			g.Permission == string(domain.CollectionManage) {
			return nil
		}
	}
	return domain.ErrForbidden
}

// ListCollectionSecrets devolve os segredos de uma coleção (member).
func (s *Service) ListCollectionSecrets(ctx context.Context, companyID domain.ID, idOrName string) ([]domain.Secret, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return nil, err
	}
	q := s.read()
	col, err := s.resolveCollection(ctx, q, companyID, idOrName)
	if err != nil {
		return nil, err
	}
	rows, err := q.ListCollectionSecrets(ctx, store.ListCollectionSecretsParams{CompanyID: companyID, CollectionID: col.ID})
	if err != nil {
		return nil, fmt.Errorf("list collection secrets: %w", err)
	}
	out := make([]domain.Secret, len(rows))
	for i, r := range rows {
		out[i] = secretFromRow(r)
	}
	return out, nil
}

// ListCollectionGrants devolve quem alcança a coleção, com o nome do sujeito (admin).
func (s *Service) ListCollectionGrants(ctx context.Context, companyID domain.ID, idOrName string) ([]CollectionGrantView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return nil, err
	}
	q := s.read()
	col, err := s.resolveCollection(ctx, q, companyID, idOrName)
	if err != nil {
		return nil, err
	}
	rows, err := q.ListCollectionGrants(ctx, store.ListCollectionGrantsParams{CompanyID: companyID, CollectionID: col.ID})
	if err != nil {
		return nil, fmt.Errorf("list collection grants: %w", err)
	}
	out := make([]CollectionGrantView, 0, len(rows))
	for _, r := range rows {
		g := CollectionGrantView{CollectionGrant: domain.CollectionGrant{ID: r.ID, CollectionID: r.CollectionID,
			SubjectType: domain.CollectionSubject(r.SubjectType), SubjectID: r.SubjectID,
			Permission: domain.CollectionPermission(r.Permission), CreatedBy: actorOf(r.CreatedByType, r.CreatedByID),
			CreatedAt: r.CreatedAt}}
		g.SubjectName = s.subjectName(ctx, q, companyID, g.SubjectType, r.SubjectID)
		out = append(out, g)
	}
	return out, nil
}

// subjectName resolve o slug da persona ou o e-mail do humano.
func (s *Service) subjectName(ctx context.Context, q *store.Queries, companyID domain.ID, st domain.CollectionSubject, id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	switch st {
	case domain.CollectionSubjectUser:
		if u, err := q.GetUserByID(ctx, *id); err == nil {
			return u.Email
		}
	default:
		if p, err := q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: *id}); err == nil {
			return p.Slug
		}
	}
	return ""
}

// requireGrantAction confere quem concede ou revoga uma concessão de coleção: admin da empresa e reautenticação
// recente (ADR-0070 §1). Sem identidade no contexto é o modo confiável: RequireReauth passa (ADR-0070 §5).
func (s *Service) requireGrantAction(ctx context.Context, companyID domain.ID) error {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return err
	}
	id, _ := auth.IdentityFrom(ctx)
	_, err := s.RequireReauth(id)
	return err
}

// CreateCollectionGrant concede a coleção (admin, com reautenticação recente, ADR-0070 §1). Subject resolve
// slug/id da persona ou e-mail/id do humano.
func (s *Service) CreateCollectionGrant(ctx context.Context, actor domain.Actor, companyID domain.ID, idOrName string, in GrantInput) (CollectionGrantView, error) {
	if err := s.requireGrantAction(ctx, companyID); err != nil {
		return CollectionGrantView{}, err
	}
	var out CollectionGrantView
	err := s.withTx(ctx, func(t *txc) error {
		col, err := s.resolveCollection(t.ctx, t.q, companyID, idOrName)
		if err != nil {
			return err
		}
		subject, err := s.resolveGrantSubject(t.ctx, t.q, companyID, in)
		if err != nil {
			return err
		}
		if err := domain.ValidateCollectionGrant(in.SubjectType, subject != nil, in.Permission); err != nil {
			return err
		}
		row, err := t.q.CreateCollectionGrant(t.ctx, store.CreateCollectionGrantParams{ID: domain.NewID(), CompanyID: companyID,
			CollectionID: col.ID, SubjectType: string(in.SubjectType), SubjectID: subject, Permission: string(in.Permission),
			CreatedByType: string(actor.Type), CreatedByID: actor.ID, Now: t.now})
		if err != nil {
			return mapErr(err, "create collection grant")
		}
		g := CollectionGrantView{CollectionGrant: domain.CollectionGrant{ID: row.ID, CollectionID: row.CollectionID,
			SubjectType: domain.CollectionSubject(row.SubjectType), SubjectID: row.SubjectID,
			Permission: domain.CollectionPermission(row.Permission), CreatedBy: actorOf(row.CreatedByType, row.CreatedByID),
			CreatedAt: row.CreatedAt}}
		g.SubjectName = s.subjectName(t.ctx, t.q, companyID, g.SubjectType, row.SubjectID)
		out = g
		return t.activity(actor, companyID, "collection.granted", "collection", col.ID,
			map[string]any{"subject_type": row.SubjectType, "subject": g.SubjectName, "permission": row.Permission}, "")
	})
	return out, err
}

// resolveGrantSubject devolve o id do sujeito (nil em all_personas).
func (s *Service) resolveGrantSubject(ctx context.Context, q *store.Queries, companyID domain.ID, in GrantInput) (*uuid.UUID, error) {
	if in.SubjectType == domain.CollectionSubjectAllPersonas {
		return nil, nil
	}
	subject := trimSpace(in.Subject)
	switch in.SubjectType {
	case domain.CollectionSubjectUser:
		if id, err := uuid.Parse(subject); err == nil {
			if _, err := q.GetUserByID(ctx, id); err != nil {
				return nil, mapErr(err, "get user")
			}
			return &id, nil
		}
		u, err := q.GetUserByEmail(ctx, subject)
		if err != nil {
			return nil, domain.Invalid("subject", "usuário desconhecido nesta instância")
		}
		return &u.ID, nil
	default:
		if id, err := uuid.Parse(subject); err == nil {
			if _, err := q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: id}); err != nil {
				return nil, mapErr(err, "get persona")
			}
			return &id, nil
		}
		p, err := q.GetPersonaBySlug(ctx, store.GetPersonaBySlugParams{CompanyID: companyID, Slug: subject})
		if err != nil {
			return nil, domain.Invalid("subject", "persona desconhecida nesta empresa")
		}
		return &p.ID, nil
	}
}

// DeleteCollectionGrant revoga uma concessão (admin, com reautenticação recente, ADR-0070 §1).
func (s *Service) DeleteCollectionGrant(ctx context.Context, actor domain.Actor, companyID domain.ID, idOrName string, grantID domain.ID) error {
	if err := s.requireGrantAction(ctx, companyID); err != nil {
		return err
	}
	return s.withTx(ctx, func(t *txc) error {
		col, err := s.resolveCollection(t.ctx, t.q, companyID, idOrName)
		if err != nil {
			return err
		}
		n, err := t.q.DeleteCollectionGrant(t.ctx, store.DeleteCollectionGrantParams{CompanyID: companyID,
			CollectionID: col.ID, ID: grantID})
		if err != nil {
			return mapErr(err, "delete collection grant")
		}
		if n == 0 { // já revogada: mapErr(nil) devolve nil e a revogação parecia ok (204 no lugar de 404)
			return fmt.Errorf("delete collection grant: %w", domain.ErrNotFound)
		}
		return t.activity(actor, companyID, "collection.revoked", "collection", col.ID,
			map[string]any{"grant": grantID.String()}, "")
	})
}

// ensureDefaultCollectionTx devolve a coleção padrão da empresa, criando com a concessão a all_personas se
// ainda não existe (empresas criadas depois da migration 00204; ADR-0068 §2.6).
func (s *Service) ensureDefaultCollectionTx(t *txc, actor domain.Actor, companyID domain.ID) (store.SecretCollection, error) {
	if c, err := t.q.GetDefaultSecretCollection(t.ctx, companyID); err == nil {
		return c, nil
	} else if !isNoRows(err) {
		return store.SecretCollection{}, fmt.Errorf("get default collection: %w", err)
	}
	row, err := t.q.CreateSecretCollection(t.ctx, store.CreateSecretCollectionParams{ID: domain.NewID(), CompanyID: companyID,
		Name: domain.DefaultCollectionName, Description: "Todas as personas da empresa a usam", IsDefault: true,
		CreatedByType: string(actor.Type), CreatedByID: actor.ID, Now: t.now})
	if err != nil {
		return store.SecretCollection{}, mapErr(err, "create default collection")
	}
	if err := t.activity(actor, companyID, "collection.created", "collection", row.ID,
		map[string]any{"name": row.Name, "is_default": true}, ""); err != nil {
		return store.SecretCollection{}, err
	}
	if _, err := t.q.CreateCollectionGrant(t.ctx, store.CreateCollectionGrantParams{ID: domain.NewID(), CompanyID: companyID,
		CollectionID: row.ID, SubjectType: string(domain.CollectionSubjectAllPersonas), Permission: string(domain.CollectionUse),
		CreatedByType: string(actor.Type), CreatedByID: actor.ID, Now: t.now}); err != nil {
		return store.SecretCollection{}, mapErr(err, "grant default collection")
	}
	return row, t.activity(actor, companyID, "collection.granted", "collection", row.ID,
		map[string]any{"subject_type": "all_personas", "permission": "use"}, "")
}

// ---------- uso pela persona ----------

// resolverFor devolve o leitor de referências de uma run. Com persona (despacho da run), o item só é lido se uma
// coleção que o contém foi concedida à persona (use), à cadeia dela ou a todas (ADR-0068 §2.4). Sem persona (uso do
// servidor: canal, modelo, git), lê direto, como antes.
func (s *Service) resolverFor(ctx context.Context, companyID domain.ID, personaID *domain.ID) func(secretref.Ref) (string, error) {
	return func(r secretref.Ref) (string, error) {
		if personaID != nil {
			if err := s.requirePersonaUse(ctx, companyID, *personaID, r); err != nil {
				return "", err
			}
		}
		return s.resolveRef(ctx, companyID, r, nil)
	}
}

// requirePersonaUse confere que a persona alcança o item da referência. O erro nomeia a persona e a referência,
// nunca o valor; sem o item, deixa resolveRef dizer que não existe.
func (s *Service) requirePersonaUse(ctx context.Context, companyID, personaID domain.ID, r secretref.Ref) error {
	if r.User {
		return nil // resolveRef recusa referência de usuário sem o dono (delegação: ADR-0068 §6)
	}
	q := s.read()
	sec, err := q.GetCompanySecretByName(ctx, store.GetCompanySecretByNameParams{CompanyID: companyID, Name: r.Name})
	if isNoRows(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("ler segredo: %w", err)
	}
	ok, err := q.PersonaCanUseSecret(ctx, store.PersonaCanUseSecretParams{CompanyID: companyID, PersonaID: personaID, SecretID: sec.ID})
	if err != nil {
		return fmt.Errorf("checar coleções da persona: %w", err)
	}
	if !ok {
		slug := personaID.String()
		if p, err := q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: personaID}); err == nil {
			slug = p.Slug
		}
		return fmt.Errorf("a persona %q não alcança %s: nenhuma coleção que contém o item foi concedida a ela (ADR-0068 §2)", slug, r.String())
	}
	// o alcance é do item, mas a referência lê um campo: o campo tem de existir (sem vazar o valor)
	if _, err := q.GetSecretField(ctx, store.GetSecretFieldParams{CompanyID: companyID, SecretID: sec.ID, Name: r.FieldName()}); err != nil {
		if isNoRows(err) {
			return fmt.Errorf("o campo %q não existe em %s", r.FieldName(), r.Name)
		}
		return fmt.Errorf("ler campo: %w", err)
	}
	return nil
}
