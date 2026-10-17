package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
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
	"github.com/renatobardi/hanko/internal/secretbox"
	"github.com/renatobardi/hanko/internal/secretref"
	"github.com/renatobardi/hanko/internal/store"
	"github.com/renatobardi/hanko/internal/vault"
)

// Segredos como recurso (ADR-0032). O valor só existe em claro (a) na resposta de uma criação/rotação com valor
// gerado pelo servidor, (b) na resposta única de "revelar", e (c) em memória, durante o despacho de uma run. Nada
// disso passa por atividade, log, export, briefing ou OTel.

const (
	maxSecretValueBytes = 8192
	maxSecretDescRunes  = 300
	// maxSecretsPerCompany limita o cofre de uma empresa (escopos company e user somados por consulta de lista).
	maxSecretsPerCompany = 500
)

// SecretView é um segredo com o que a tela mostra. RevealedValue só vem preenchido na resposta de uma criação ou
// rotação com valor gerado e na de "revelar": é a única vez que o valor sai.
type SecretView struct {
	domain.Secret
	UsedBy        []domain.SecretUse `json:"used_by,omitempty"`
	RevealedValue string             `json:"-"`
}

// CreateSecretInput cria um segredo. Local: Value ou Generate. Vault: ExternalRef.
type CreateSecretInput struct {
	Scope       domain.SecretScope // padrão company
	Name        string
	Description string
	Provider    domain.SecretProvider // padrão local
	Value       string
	Generate    bool
	ExternalRef string
}

// RotateSecretInput troca o valor (ou a referência no cofre) de um segredo. Description nil mantém a atual.
type RotateSecretInput struct {
	Provider    domain.SecretProvider // vazio = mantém o atual
	Value       string
	Generate    bool
	ExternalRef string
	Description *string
}

var (
	secretMetricsOnce sync.Once
	secretResolved    metric.Int64Counter
)

func secretCounter() metric.Int64Counter {
	secretMetricsOnce.Do(func() {
		secretResolved, _ = otel.Meter("hanko/secrets").Int64Counter("hanko.secrets.resolved",
			metric.WithDescription("segredos resolvidos no despacho, por provider e resultado (nunca o valor)"))
	})
	return secretResolved
}

func (s *Service) secretBox() *secretbox.Box { return s.Integrations.Secrets }

func secretAAD(id uuid.UUID) string { return "secret/" + id.String() }

func secretFromRow(r store.Secret) domain.Secret {
	return domain.Secret{ID: r.ID, CompanyID: r.CompanyID, Scope: domain.SecretScope(r.Scope), Name: r.Name, Description: r.Description,
		Provider: domain.SecretProvider(r.Provider), ExternalRef: r.ExternalRef, Version: int(r.Version), RevealedAt: r.RevealedAt,
		LastUsedAt: r.LastUsedAt, CreatedBy: actorOf(r.CreatedByType, r.CreatedByID), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

// secretCaller valida quem chama e devolve o usuário (nil no escopo company, ou sem identidade).
func (s *Service) secretCaller(ctx context.Context, actor domain.Actor, companyID domain.ID, scope domain.SecretScope, write bool) (*uuid.UUID, error) {
	if actor.Type == domain.ActorPersona {
		return nil, domain.ErrForbidden // um agente nunca gerencia nem lê segredos
	}
	if !scope.Valid() {
		return nil, domain.Invalid("scope", "must be company or user")
	}
	switch scope {
	case domain.SecretScopeUser:
		if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
			return nil, err
		}
		id, ok := auth.IdentityFrom(ctx)
		if !ok {
			return nil, domain.Invalid("scope", "segredos de usuário exigem uma pessoa autenticada")
		}
		uid := id.User.ID
		return &uid, nil
	default:
		min := domain.MemberMember
		if write {
			min = domain.MemberAdmin
		}
		if err := s.require(ctx, companyID, min); err != nil {
			return nil, err
		}
		return nil, nil
	}
}

func (s *Service) validateSecretName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !secretref.ValidName(name) {
		return "", domain.Invalid("name", "use letras, números, . _ - (começando por letra, até 64 caracteres)")
	}
	return name, nil
}

func validateSecretDescription(d string) (string, error) {
	d = strings.TrimSpace(d)
	if len([]rune(d)) > maxSecretDescRunes {
		return "", domain.Invalid("description", fmt.Sprintf("at most %d characters", maxSecretDescRunes))
	}
	return d, nil
}

func newGeneratedSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("gerar segredo: %w", err)
	}
	return "hk_" + base64.RawURLEncoding.EncodeToString(b), nil
}

// secretMaterial é o que vai para a linha: valor cifrado ou referência no cofre.
type secretMaterial struct {
	provider  domain.SecretProvider
	enc       string
	ext       string
	revealed  bool   // o valor sai na resposta (gerado pelo servidor)
	plaintext string // só quando revealed
}

// prepareMaterial valida a combinação valor/geração/cofre e cifra. id é o do segredo (entra no AAD).
func (s *Service) prepareMaterial(id uuid.UUID, provider domain.SecretProvider, value string, generate bool, ext string) (secretMaterial, error) {
	if provider == "" {
		provider = domain.SecretProviderLocal
	}
	if !provider.Valid() {
		return secretMaterial{}, domain.Invalid("provider", "must be local or vault")
	}
	if provider == domain.SecretProviderVault {
		if value != "" || generate {
			return secretMaterial{}, domain.Invalid("value", "um segredo de cofre guarda só a referência (external_ref)")
		}
		if s.Vault == nil {
			return secretMaterial{}, domain.Invalid("provider", "nenhum cofre externo configurado neste servidor (HANKO_VAULT_ADDR)")
		}
		if _, _, err := vault.SplitRef(ext); err != nil {
			return secretMaterial{}, domain.Invalid("external_ref", err.Error())
		}
		return secretMaterial{provider: provider, ext: strings.TrimSpace(ext)}, nil
	}
	if ext != "" {
		return secretMaterial{}, domain.Invalid("external_ref", "só vale para provider vault")
	}
	if !s.secretBox().Enabled() {
		return secretMaterial{}, domain.Invalid("secrets", "HANKO_SECRET_KEY não está configurada neste servidor; sem ela não há como guardar segredos")
	}
	m := secretMaterial{provider: provider}
	switch {
	case value != "" && generate:
		return secretMaterial{}, domain.Invalid("value", "informe o valor ou peça para gerar, não os dois")
	case generate:
		v, err := newGeneratedSecret()
		if err != nil {
			return secretMaterial{}, err
		}
		value, m.revealed, m.plaintext = v, true, v
	case value == "":
		return secretMaterial{}, domain.Invalid("value", "required (ou use gerar)")
	case len(value) > maxSecretValueBytes || strings.ContainsRune(value, 0):
		return secretMaterial{}, domain.Invalid("value", fmt.Sprintf("at most %d bytes, sem NUL", maxSecretValueBytes))
	}
	enc, err := s.secretBox().Seal([]byte(value), secretAAD(id))
	if err != nil {
		return secretMaterial{}, fmt.Errorf("seal secret: %w", err)
	}
	m.enc = enc
	return m, nil
}

func secretEventData(r store.Secret) map[string]any {
	if r.Scope == string(domain.SecretScopeUser) {
		return map[string]any{"scope": r.Scope, "provider": r.Provider, "version": r.Version} // o nome é privado do dono
	}
	return map[string]any{"name": r.Name, "scope": r.Scope, "provider": r.Provider, "version": r.Version}
}

// CreateSecret cria um segredo. Escopo company: admin. Escopo user: o próprio usuário (member).
func (s *Service) CreateSecret(ctx context.Context, actor domain.Actor, companyID domain.ID, in CreateSecretInput) (SecretView, error) {
	scope := in.Scope
	if scope == "" {
		scope = domain.SecretScopeCompany
	}
	owner, err := s.secretCaller(ctx, actor, companyID, scope, true)
	if err != nil {
		return SecretView{}, err
	}
	name, err := s.validateSecretName(in.Name)
	if err != nil {
		return SecretView{}, err
	}
	desc, err := validateSecretDescription(in.Description)
	if err != nil {
		return SecretView{}, err
	}
	id := domain.NewID()
	m, err := s.prepareMaterial(id, in.Provider, in.Value, in.Generate, in.ExternalRef)
	if err != nil {
		return SecretView{}, err
	}
	var out SecretView
	err = s.withTx(ctx, func(t *txc) error {
		return s.createSecretTx(t, actor, companyID, scope, owner, id, name, desc, m, &out)
	})
	return out, err
}

func (s *Service) createSecretTx(t *txc, actor domain.Actor, companyID domain.ID, scope domain.SecretScope, owner *uuid.UUID,
	id uuid.UUID, name, desc string, m secretMaterial, out *SecretView) error {
	if _, err := t.q.GetCompanyByID(t.ctx, companyID); err != nil {
		return mapErr(err, "company")
	}
	if scope == domain.SecretScopeCompany {
		list, err := t.q.ListCompanySecrets(t.ctx, companyID)
		if err != nil {
			return fmt.Errorf("list secrets: %w", err)
		}
		if len(list) >= maxSecretsPerCompany {
			return domain.Invalid("name", fmt.Sprintf("a empresa já tem %d segredos (o máximo)", maxSecretsPerCompany))
		}
	}
	var revealedAt *time.Time
	if m.revealed {
		n := t.now
		revealedAt = &n
	}
	row, err := t.q.CreateSecret(t.ctx, store.CreateSecretParams{ID: id, CompanyID: companyID, Scope: string(scope), OwnerUserID: owner,
		Name: name, Description: desc, Provider: string(m.provider), ValueEnc: m.enc, ExternalRef: m.ext, RevealedAt: revealedAt,
		CreatedByType: string(actor.Type), CreatedByID: actor.ID, Now: t.now})
	if err != nil {
		return mapErr(err, "create secret")
	}
	*out = SecretView{Secret: secretFromRow(row), RevealedValue: m.plaintext}
	return t.activity(actor, companyID, "secret.created", "secret", row.ID, secretEventData(row), "")
}

// getSecretRow lê o segredo visível ao chamador: o da empresa pelo nome, ou o do próprio usuário.
func getSecretRow(ctx context.Context, q *store.Queries, companyID domain.ID, scope domain.SecretScope, owner *uuid.UUID, name string, forUpdate bool) (store.Secret, error) {
	var r store.Secret
	var err error
	switch {
	case scope == domain.SecretScopeUser && owner != nil && forUpdate:
		r, err = q.GetUserSecretByNameForUpdate(ctx, store.GetUserSecretByNameForUpdateParams{CompanyID: companyID, OwnerUserID: owner, Name: name})
	case scope == domain.SecretScopeUser && owner != nil:
		r, err = q.GetUserSecretByName(ctx, store.GetUserSecretByNameParams{CompanyID: companyID, OwnerUserID: owner, Name: name})
	case forUpdate:
		r, err = q.GetCompanySecretByNameForUpdate(ctx, store.GetCompanySecretByNameForUpdateParams{CompanyID: companyID, Name: name})
	default:
		r, err = q.GetCompanySecretByName(ctx, store.GetCompanySecretByNameParams{CompanyID: companyID, Name: name})
	}
	if err != nil {
		return r, mapErr(err, "get secret")
	}
	return r, nil
}

// RotateSecret troca o valor (ou a referência no cofre). A versão sobe e a marca de "revelado" é zerada.
func (s *Service) RotateSecret(ctx context.Context, actor domain.Actor, companyID domain.ID, scope domain.SecretScope, name string, in RotateSecretInput) (SecretView, error) {
	owner, err := s.secretCaller(ctx, actor, companyID, scope, true)
	if err != nil {
		return SecretView{}, err
	}
	var out SecretView
	err = s.withTx(ctx, func(t *txc) error {
		cur, err := getSecretRow(t.ctx, t.q, companyID, scope, owner, name, true)
		if err != nil {
			return err
		}
		provider := in.Provider
		if provider == "" {
			provider = domain.SecretProvider(cur.Provider)
		}
		m, err := s.prepareMaterial(cur.ID, provider, in.Value, in.Generate, in.ExternalRef)
		if err != nil {
			return err
		}
		desc := cur.Description
		if in.Description != nil {
			if desc, err = validateSecretDescription(*in.Description); err != nil {
				return err
			}
		}
		var revealedAt *time.Time
		if m.revealed {
			n := t.now
			revealedAt = &n
		}
		row, err := t.q.RotateSecret(t.ctx, store.RotateSecretParams{ID: cur.ID, CompanyID: companyID, Provider: string(m.provider),
			ValueEnc: m.enc, ExternalRef: m.ext, RevealedAt: revealedAt, Description: desc, Now: t.now})
		if err != nil {
			return mapErr(err, "rotate secret")
		}
		out = SecretView{Secret: secretFromRow(row), RevealedValue: m.plaintext}
		return t.activity(actor, companyID, "secret.rotated", "secret", row.ID, secretEventData(row), "")
	})
	return out, err
}

// RevealSecret mostra o valor, uma única vez por versão (admin da empresa; o dono, no escopo user). A segunda
// chamada é conflito: para ver de novo, rotacione. Segredo de cofre externo não se revela (o valor mora no cofre).
func (s *Service) RevealSecret(ctx context.Context, actor domain.Actor, companyID domain.ID, scope domain.SecretScope, name string) (SecretView, error) {
	owner, err := s.secretCaller(ctx, actor, companyID, scope, true)
	if err != nil {
		return SecretView{}, err
	}
	var out SecretView
	err = s.withTx(ctx, func(t *txc) error {
		cur, err := getSecretRow(t.ctx, t.q, companyID, scope, owner, name, true)
		if err != nil {
			return err
		}
		if cur.Provider != string(domain.SecretProviderLocal) {
			return domain.Invalid("provider", "o valor deste segredo mora no cofre externo; leia-o lá")
		}
		row, err := t.q.MarkSecretRevealed(t.ctx, store.MarkSecretRevealedParams{ID: cur.ID, CompanyID: companyID, Now: &t.now})
		if isNoRows(err) {
			return fmt.Errorf("segredo %q: o valor desta versão já foi revelado; rotacione para obter outro: %w", name, domain.ErrConflict)
		}
		if err != nil {
			return mapErr(err, "reveal secret")
		}
		plain, err := s.secretBox().Open(cur.ValueEnc, secretAAD(cur.ID))
		if err != nil {
			return domain.Invalid("secrets", "não foi possível decifrar o segredo: "+err.Error())
		}
		out = SecretView{Secret: secretFromRow(row), RevealedValue: string(plain)}
		return t.activity(actor, companyID, "secret.revealed", "secret", row.ID, secretEventData(row), "")
	})
	return out, err
}

// DeleteSecret apaga o segredo. Em uso por persona ou integração, é recusado listando os usos, a menos que force.
func (s *Service) DeleteSecret(ctx context.Context, actor domain.Actor, companyID domain.ID, scope domain.SecretScope, name string, force bool) error {
	owner, err := s.secretCaller(ctx, actor, companyID, scope, true)
	if err != nil {
		return err
	}
	return s.withTx(ctx, func(t *txc) error {
		cur, err := getSecretRow(t.ctx, t.q, companyID, scope, owner, name, true)
		if err != nil {
			return err
		}
		if scope == domain.SecretScopeCompany && !force {
			uses, err := s.secretUsage(t.ctx, t.q, companyID)
			if err != nil {
				return err
			}
			if u := uses[cur.Name]; len(u) > 0 {
				names := make([]string, len(u))
				for i, x := range u {
					names[i] = x.Kind + ":" + x.Slug
				}
				return fmt.Errorf("segredo %q em uso por %s (force para apagar mesmo assim): %w", name, strings.Join(names, ", "), domain.ErrConflict)
			}
		}
		if n, err := t.q.DeleteSecret(t.ctx, store.DeleteSecretParams{ID: cur.ID, CompanyID: companyID}); err != nil || n == 0 {
			return mapErr(err, "delete secret")
		}
		return t.activity(actor, companyID, "secret.deleted", "secret", cur.ID, secretEventData(cur), "")
	})
}

// ListSecrets devolve os segredos da empresa (qualquer membro vê nomes e metadados, nunca valores) e, se pedido e
// houver usuário autenticado, os do próprio chamador. withUsage calcula "usado por" (só admin; exige a chave).
func (s *Service) ListSecrets(ctx context.Context, companyID domain.ID, scope domain.SecretScope, withUsage bool) ([]SecretView, error) {
	owner, err := s.secretCaller(ctx, domain.HumanActor, companyID, scope, false)
	if err != nil {
		return nil, err
	}
	q := s.read()
	var rows []store.Secret
	if scope == domain.SecretScopeUser {
		rows, err = q.ListUserSecrets(ctx, store.ListUserSecretsParams{CompanyID: companyID, OwnerUserID: owner})
	} else {
		rows, err = q.ListCompanySecrets(ctx, companyID)
	}
	if err != nil {
		return nil, fmt.Errorf("list secrets: %w", err)
	}
	var uses map[string][]domain.SecretUse
	if withUsage && scope == domain.SecretScopeCompany && s.require(ctx, companyID, domain.MemberAdmin) == nil {
		if uses, err = s.secretUsage(ctx, q, companyID); err != nil {
			return nil, err
		}
	}
	out := make([]SecretView, len(rows))
	for i, r := range rows {
		out[i] = SecretView{Secret: secretFromRow(r), UsedBy: uses[r.Name]}
	}
	return out, nil
}

// GetSecret devolve os metadados de um segredo (nunca o valor), com onde é usado.
func (s *Service) GetSecret(ctx context.Context, companyID domain.ID, scope domain.SecretScope, name string) (SecretView, error) {
	owner, err := s.secretCaller(ctx, domain.HumanActor, companyID, scope, false)
	if err != nil {
		return SecretView{}, err
	}
	q := s.read()
	r, err := getSecretRow(ctx, q, companyID, scope, owner, name, false)
	if err != nil {
		return SecretView{}, err
	}
	v := SecretView{Secret: secretFromRow(r)}
	if scope == domain.SecretScopeCompany && s.require(ctx, companyID, domain.MemberAdmin) == nil {
		uses, err := s.secretUsage(ctx, q, companyID)
		if err != nil {
			return SecretView{}, err
		}
		v.UsedBy = uses[r.Name]
	}
	return v, nil
}

// SecretNames devolve só os nomes dos segredos da empresa (para referência em formulários); qualquer membro.
func (s *Service) SecretNames(ctx context.Context, companyID domain.ID) ([]string, error) {
	rows, err := s.ListSecrets(ctx, companyID, domain.SecretScopeCompany, false)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Name
	}
	return out, nil
}

// secretUsage mapeia nome do segredo → onde é referenciado (personas e integrações da empresa).
func (s *Service) secretUsage(ctx context.Context, q *store.Queries, companyID domain.ID) (map[string][]domain.SecretUse, error) {
	uses := map[string][]domain.SecretUse{}
	add := func(refs []secretref.Ref, u domain.SecretUse) {
		for _, r := range refs {
			if !r.User && !slices.Contains(uses[r.Name], u) {
				uses[r.Name] = append(uses[r.Name], u)
			}
		}
	}
	ps, err := q.ListPersonasReferencingSecrets(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list persona secret refs: %w", err)
	}
	for _, p := range ps {
		add(secretref.Find(string(p.AdapterConfig)), domain.SecretUse{Kind: "persona", Slug: p.Slug, Name: p.Slug})
	}
	if !s.secretBox().Enabled() {
		return uses, nil
	}
	ints, err := q.ListIntegrations(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list integrations: %w", err)
	}
	for _, it := range ints {
		sec, err := s.open(it.ID, it.SecretEnc)
		if err != nil {
			continue
		}
		var refs []secretref.Ref
		for _, v := range sec.values() {
			refs = append(refs, secretref.Find(v)...)
		}
		add(refs, domain.SecretUse{Kind: "integration", Slug: it.Slug, Name: it.Name})
	}
	for k := range uses {
		slices.SortFunc(uses[k], func(a, b domain.SecretUse) int { return strings.Compare(a.Kind+a.Slug, b.Kind+b.Slug) })
	}
	return uses, nil
}

// checkSecretRefs confere que cada referência de company existe e recusa as de user (configuração compartilhada).
func (s *Service) checkSecretRefs(ctx context.Context, q *store.Queries, companyID domain.ID, field string, texts ...string) error {
	seen := map[string]bool{}
	for _, text := range texts {
		for _, r := range secretref.Find(text) {
			if r.User {
				return domain.Invalid(field, fmt.Sprintf("%s: segredo de usuário não vale em configuração compartilhada", r.String()))
			}
			if seen[r.Name] {
				continue
			}
			seen[r.Name] = true
			if _, err := q.GetCompanySecretByName(ctx, store.GetCompanySecretByNameParams{CompanyID: companyID, Name: r.Name}); err != nil {
				if isNoRows(err) {
					return domain.Invalid(field, fmt.Sprintf("o segredo %q não existe (crie-o em Configurações › Segredos)", r.Name))
				}
				return fmt.Errorf("check secret ref: %w", err)
			}
		}
	}
	return nil
}

// ---------- resolução no despacho ----------

// resolveRef lê o valor de uma referência. Sem autorização por papel: quem chama (despacho, ação do dono) já foi
// decidido. userID é o dono para referências user/ (nil = não permitidas).
func (s *Service) resolveRef(ctx context.Context, companyID domain.ID, r secretref.Ref, userID *uuid.UUID) (val string, err error) {
	ctx, span := otel.Tracer("hanko/secrets").Start(ctx, "hanko.secrets.resolve")
	provider, result := "local", "ok"
	defer func() {
		span.SetAttributes(attribute.String("hanko.company_id", companyID.String()), attribute.String("hanko.secret.provider", provider),
			attribute.Bool("hanko.secret.user_scope", r.User)) // nome e valor ficam fora do span
		if err != nil {
			result = "error"
			span.SetStatus(codes.Error, "secret resolve")
		}
		if c := secretCounter(); c != nil {
			c.Add(ctx, 1, metric.WithAttributes(attribute.String("provider", provider), attribute.String("result", result)))
		}
		span.End()
	}()
	q := s.read()
	var row store.Secret
	switch {
	case r.User && userID == nil:
		return "", errors.New("segredo de usuário não está disponível aqui")
	case r.User:
		row, err = q.GetUserSecretByName(ctx, store.GetUserSecretByNameParams{CompanyID: companyID, OwnerUserID: userID, Name: r.Name})
	default:
		row, err = q.GetCompanySecretByName(ctx, store.GetCompanySecretByNameParams{CompanyID: companyID, Name: r.Name})
	}
	if err != nil {
		if isNoRows(err) {
			return "", errors.New("não existe")
		}
		return "", fmt.Errorf("ler segredo: %w", err)
	}
	provider = row.Provider
	switch domain.SecretProvider(row.Provider) {
	case domain.SecretProviderVault:
		if s.Vault == nil {
			return "", errors.New("cofre externo não configurado")
		}
		val, err = s.Vault.Get(ctx, row.ExternalRef)
		if err != nil {
			return "", fmt.Errorf("cofre: %w", err)
		}
	default:
		plain, oerr := s.secretBox().Open(row.ValueEnc, secretAAD(row.ID))
		if oerr != nil {
			return "", fmt.Errorf("decifrar: %w", oerr)
		}
		val = string(plain)
	}
	_ = q.TouchSecretUsed(ctx, store.TouchSecretUsedParams{ID: row.ID, CompanyID: companyID, Now: ptrTime(s.Now().UTC())})
	return val, nil
}

// ResolveSecretText expande as referências ${secret:NOME} de um texto (só segredos da empresa) e devolve também os
// valores inseridos, para a redação. É o ponto de entrada para quem precisa de um segredo no despacho (credencial
// de repositório, provider de modelo). O erro nomeia o segredo, nunca o valor.
func (s *Service) ResolveSecretText(ctx context.Context, companyID domain.ID, text string) (string, []string, error) {
	return secretref.Expand(text, func(r secretref.Ref) (string, error) { return s.resolveRef(ctx, companyID, r, nil) })
}

// ResolveUserSecretText é ResolveSecretText para uma ação feita pelo próprio dono: também expande
// ${secret:user/NOME} com os segredos do usuário autenticado. Sem identidade, referência user/ falha.
func (s *Service) ResolveUserSecretText(ctx context.Context, companyID domain.ID, text string) (string, []string, error) {
	var uid *uuid.UUID
	if id, ok := auth.IdentityFrom(ctx); ok {
		u := id.User.ID
		uid = &u
		if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
			return "", nil, err
		}
	}
	return secretref.Expand(text, func(r secretref.Ref) (string, error) { return s.resolveRef(ctx, companyID, r, uid) })
}

// ResolveAdapterConfig expande as referências do adapter_config da persona no despacho. Devolve a configuração
// pronta para o adapter e os valores resolvidos (para a redação do log da run). Sem referência, devolve cfg como
// está. Falha cedo com o nome do segredo que faltou.
func (s *Service) ResolveAdapterConfig(ctx context.Context, companyID domain.ID, cfg json.RawMessage) (json.RawMessage, []string, error) {
	if !secretref.Has(string(cfg)) {
		return cfg, nil, nil
	}
	var doc any
	if err := json.Unmarshal(cfg, &doc); err != nil {
		return cfg, nil, fmt.Errorf("adapter_config: %w", err)
	}
	out, used, err := secretref.ExpandJSON(doc, func(r secretref.Ref) (string, error) { return s.resolveRef(ctx, companyID, r, nil) })
	if err != nil {
		return cfg, nil, err
	}
	raw, err := json.Marshal(out) //nolint:gosec // configuração com os segredos resolvidos: vai só para o adapter, em memória
	if err != nil {
		return cfg, nil, fmt.Errorf("adapter_config: %w", err)
	}
	return raw, used, nil
}

// expandIntegrationSecrets resolve referências nos headers/env/segredo de uma integração (RunExtras, teste).
func (s *Service) expandMap(ctx context.Context, companyID domain.ID, m map[string]string) (map[string]string, []string, error) {
	if len(m) == 0 {
		return m, nil, nil
	}
	out := make(map[string]string, len(m))
	var used []string
	for k, v := range m {
		x, u, err := s.ResolveSecretText(ctx, companyID, v)
		if err != nil {
			return nil, nil, err
		}
		out[k] = x
		used = append(used, u...)
	}
	return out, used, nil
}

// ---------- env da persona ----------

// personaSecretName é o nome do segredo que guarda uma variável de env da persona.
func personaSecretName(slug, variable string) string {
	n := "persona." + slug + "." + variable
	if len(n) <= 64 {
		return n
	}
	h := sha256.Sum256([]byte(slug + "." + variable))
	return "persona." + hex.EncodeToString(h[:8]) + "." + variable[:min(len(variable), 30)]
}

// sealPersonaEnv troca os valores em claro de adapter_config.env por referências a segredos da empresa (criando ou
// rotacionando `persona.<slug>.<VARIAVEL>`). Valores que já são referência ficam e são validados. Sem chave de
// cifragem devolve cfg como está (retrocompatível). Devolve a configuração nova e quantos valores moveu.
func (s *Service) sealPersonaEnv(t *txc, actor domain.Actor, companyID domain.ID, slug string, cfg []byte) ([]byte, int, error) {
	var doc map[string]any
	if json.Unmarshal(cfg, &doc) != nil {
		return cfg, 0, nil
	}
	env, ok := doc["env"].(map[string]any)
	if !ok || len(env) == 0 {
		return cfg, 0, nil
	}
	moved := 0
	for _, k := range sortedAnyKeys(env) {
		v, isStr := env[k].(string)
		if !isStr || v == "" {
			continue
		}
		if secretref.Has(v) {
			if err := s.checkSecretRefs(t.ctx, t.q, companyID, "adapter_config.env."+k, v); err != nil {
				return nil, 0, err
			}
			continue
		}
		if !s.secretBox().Enabled() {
			continue // sem chave: continua em claro, como antes
		}
		name := personaSecretName(slug, k)
		if err := s.upsertCompanySecretTx(t, actor, companyID, name, "env "+k+" da persona "+slug, v); err != nil {
			return nil, 0, err
		}
		env[k] = secretref.Ref{Name: name}.String()
		moved++
	}
	if moved == 0 {
		return cfg, 0, nil
	}
	doc["env"] = env
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, 0, fmt.Errorf("marshal adapter_config: %w", err)
	}
	return out, moved, nil
}

func sortedAnyKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// upsertCompanySecretTx cria o segredo local da empresa ou, se já existe, troca o valor (nova versão).
func (s *Service) upsertCompanySecretTx(t *txc, actor domain.Actor, companyID domain.ID, name, desc, value string) error {
	cur, err := t.q.GetCompanySecretByNameForUpdate(t.ctx, store.GetCompanySecretByNameForUpdateParams{CompanyID: companyID, Name: name})
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
	var out SecretView
	return s.createSecretTx(t, actor, companyID, domain.SecretScopeCompany, nil, id, name, desc, m, &out)
}

// MigratePersonaEnv move o env em claro das personas já existentes para segredos da empresa (idempotente). É da
// instalação: roda no boot (se há chave) e por `hanko secret migrate-env` (dono). Devolve quantas personas migrou.
func (s *Service) MigratePersonaEnv(ctx context.Context) (int, error) {
	if id, ok := auth.IdentityFrom(ctx); ok && id.User.Role != domain.RoleOwner {
		return 0, domain.ErrForbidden
	}
	if !s.secretBox().Enabled() {
		return 0, domain.Invalid("secrets", "HANKO_SECRET_KEY não está configurada")
	}
	rows, err := s.read().ListPersonasWithEnv(ctx)
	if err != nil {
		return 0, fmt.Errorf("list personas with env: %w", err)
	}
	n := 0
	for _, r := range rows {
		moved := 0
		err := s.withTx(ctx, func(t *txc) error {
			var cfg []byte
			var err error
			if cfg, moved, err = s.sealPersonaEnv(t, domain.SystemActor, r.CompanyID, r.Slug, r.AdapterConfig); err != nil {
				return err
			}
			if moved == 0 {
				return nil
			}
			if err := t.q.SetPersonaAdapterConfig(t.ctx, store.SetPersonaAdapterConfigParams{CompanyID: r.CompanyID, ID: r.ID, AdapterConfig: cfg, Now: t.now}); err != nil {
				return fmt.Errorf("update persona adapter_config: %w", err)
			}
			return t.activity(domain.SystemActor, r.CompanyID, "persona.env_migrated", "persona", r.ID, map[string]any{"slug": r.Slug, "variables": moved}, "")
		})
		if err != nil {
			return n, fmt.Errorf("persona %s: %w", r.Slug, err)
		}
		if moved > 0 {
			n++
		}
	}
	return n, nil
}

// CountPersonasWithPlainEnv diz quantas personas ainda têm valor de env em claro (aviso do boot sem chave).
func (s *Service) CountPersonasWithPlainEnv(ctx context.Context) (int, error) {
	rows, err := s.read().ListPersonasWithEnv(ctx)
	if err != nil {
		return 0, fmt.Errorf("list personas with env: %w", err)
	}
	n := 0
	for _, r := range rows {
		var doc struct {
			Env map[string]any `json:"env"`
		}
		if json.Unmarshal(r.AdapterConfig, &doc) != nil {
			continue
		}
		for _, v := range doc.Env {
			if str, ok := v.(string); ok && str != "" && !secretref.Has(str) {
				n++
				break
			}
		}
	}
	return n, nil
}

func ptrTime(t time.Time) *time.Time { return &t }

// rekeySecrets reescreve com a chave atual os segredos (empresa e usuário) cifrados com a chave anterior.
func (s *Service) rekeySecrets(ctx context.Context) (int, error) {
	rows, err := s.read().ListAllSecretsForRekey(ctx)
	if err != nil {
		return 0, fmt.Errorf("list secrets: %w", err)
	}
	n := 0
	for _, r := range rows {
		if !s.secretBox().NeedsReseal(r.ValueEnc) {
			continue
		}
		plain, err := s.secretBox().Open(r.ValueEnc, secretAAD(r.ID))
		if err != nil {
			return n, fmt.Errorf("segredo %s: %w", r.ID, err)
		}
		enc, err := s.secretBox().Seal(plain, secretAAD(r.ID))
		if err != nil {
			return n, fmt.Errorf("segredo %s: %w", r.ID, err)
		}
		if err := store.New(s.Pool).SetSecretValueEnc(ctx, store.SetSecretValueEncParams{ID: r.ID, ValueEnc: enc}); err != nil {
			return n, fmt.Errorf("segredo %s: %w", r.ID, err)
		}
		n++
	}
	return n, nil
}
