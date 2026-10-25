package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
	"github.com/renatobardi/hanko/internal/vaultfile"
)

// Export cifrado do cofre e import em outra instância (ADR-0068 §8.2 e §8.3, issue #62). Tudo, inclusive os nomes, vai
// dentro do conteúdo cifrado do .hkv; o cabeçalho leva só sal, parâmetros e nonce. O `hanko export` de empresa
// (ADR-0022) segue levando só referências.

// hkvVersion é a versão do conteúdo (dentro do cifrado), separada da versão do contêiner de vaultfile.
const hkvVersion = 1

type hkvDoc struct {
	Version     int             `json:"version"`
	ExportedAt  time.Time       `json:"exported_at"`
	Secrets     []hkvSecret     `json:"secrets"`
	Collections []hkvCollection `json:"collections"`
}

type hkvSecret struct {
	Name            string     `json:"name"`
	Description     string     `json:"description,omitempty"`
	RotateAfterDays *int       `json:"rotate_after_days,omitempty"`
	Fields          []hkvField `json:"fields"`
}

type hkvField struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Sensitive   bool   `json:"sensitive"`
	Provider    string `json:"provider"`
	Position    int    `json:"position"`
	Value       string `json:"value,omitempty"`
	ExternalRef string `json:"external_ref,omitempty"`
	FileName    string `json:"file_name,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	Content     []byte `json:"content,omitempty"`
}

type hkvCollection struct {
	Name            string     `json:"name"`
	Description     string     `json:"description,omitempty"`
	IsDefault       bool       `json:"is_default,omitempty"`
	RotateAfterDays *int       `json:"rotate_after_days,omitempty"`
	Items           []string   `json:"items"`
	Grants          []hkvGrant `json:"grants"`
}

// hkvGrant é uma concessão pelo nome do sujeito: slug da persona ou e-mail do humano; vazio em all_personas.
type hkvGrant struct {
	SubjectType string `json:"subject_type"`
	Subject     string `json:"subject,omitempty"`
	Permission  string `json:"permission"`
}

// VaultExportResult é o arquivo .hkv e quantos segredos ele leva.
type VaultExportResult struct {
	File    []byte
	Secrets int
}

// ExportVault cifra o cofre da empresa com a senha (admin, reautenticação recente). Leva os segredos da empresa com os
// campos (arquivo inclusive; campo de cofre externo leva só a referência), as coleções e as concessões; segredo de
// usuário, histórico e uso ficam de fora. O valor decifrado só existe em memória durante a chamada.
func (s *Service) ExportVault(ctx context.Context, actor domain.Actor, companyID domain.ID, password string) (VaultExportResult, error) {
	if err := s.requireVaultTransfer(ctx, actor, companyID); err != nil {
		return VaultExportResult{}, err
	}
	if len(password) < vaultfile.MinPasswordLen {
		return VaultExportResult{}, domain.Invalid("password", fmt.Sprintf("a senha do arquivo precisa de pelo menos %d caracteres", vaultfile.MinPasswordLen))
	}
	if !s.secretBox().Enabled() {
		return VaultExportResult{}, domain.Invalid("secrets", "HANKO_SECRET_KEY não está configurada neste servidor; não há cofre para exportar")
	}
	var res VaultExportResult
	err := s.withTx(ctx, func(t *txc) error {
		doc := hkvDoc{Version: hkvVersion, ExportedAt: t.now, Secrets: []hkvSecret{}, Collections: []hkvCollection{}}
		secrets, err := t.q.ListCompanySecrets(t.ctx, companyID)
		if err != nil {
			return fmt.Errorf("list secrets: %w", err)
		}
		for _, sec := range secrets {
			hs, err := s.exportSecret(t, companyID, sec)
			if err != nil {
				return fmt.Errorf("segredo %s: %w", sec.Name, err)
			}
			doc.Secrets = append(doc.Secrets, hs)
		}
		if doc.Collections, err = s.exportCollections(t, companyID); err != nil {
			return err
		}
		plain, err := json.Marshal(doc)
		if err != nil {
			return fmt.Errorf("serializar cofre: %w", err)
		}
		if res.File, err = vaultfile.Seal(plain, password); err != nil {
			return err
		}
		res.Secrets = len(doc.Secrets)
		return t.activity(actor, companyID, "vault.exported", "company", companyID, map[string]any{
			"secrets": len(doc.Secrets), "collections": len(doc.Collections)}, "")
	})
	if err != nil {
		return VaultExportResult{}, err
	}
	return res, nil
}

func (s *Service) exportSecret(t *txc, companyID domain.ID, sec store.Secret) (hkvSecret, error) {
	hs := hkvSecret{Name: sec.Name, Description: sec.Description, RotateAfterDays: rotateDays(sec.RotateAfterDays), Fields: []hkvField{}}
	fields, err := t.q.ListSecretFields(t.ctx, store.ListSecretFieldsParams{CompanyID: companyID, SecretID: sec.ID})
	if err != nil {
		return hs, fmt.Errorf("list fields: %w", err)
	}
	for _, f := range fields {
		hf := hkvField{Name: f.Name, Kind: f.Kind, Sensitive: f.Sensitive, Provider: f.Provider, Position: int(f.Position)}
		switch {
		case f.Provider == string(domain.SecretProviderVault):
			hf.ExternalRef = f.ExternalRef
		default:
			plain, err := s.fieldValue(t.ctx, companyID, sec, f)
			if err != nil {
				return hs, fmt.Errorf("campo %s: %w", f.Name, err)
			}
			if f.Kind == string(domain.SecretFieldFile) {
				hf.Content, hf.FileName, hf.ContentType = plain, f.FileName, f.ContentType
			} else {
				hf.Value = string(plain)
			}
		}
		hs.Fields = append(hs.Fields, hf)
	}
	return hs, nil
}

func (s *Service) exportCollections(t *txc, companyID domain.ID) ([]hkvCollection, error) {
	cols, err := t.q.ListSecretCollections(t.ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list collections: %w", err)
	}
	out := make([]hkvCollection, 0, len(cols))
	for _, c := range cols {
		hc := hkvCollection{Name: c.Name, Description: c.Description, IsDefault: c.IsDefault,
			RotateAfterDays: rotateDays(c.RotateAfterDays), Items: []string{}, Grants: []hkvGrant{}}
		items, err := t.q.ListCollectionSecrets(t.ctx, store.ListCollectionSecretsParams{CompanyID: companyID, CollectionID: c.ID})
		if err != nil {
			return nil, fmt.Errorf("list collection secrets: %w", err)
		}
		for _, it := range items {
			if it.Scope == string(domain.SecretScopeCompany) {
				hc.Items = append(hc.Items, it.Name)
			}
		}
		grants, err := t.q.ListCollectionGrants(t.ctx, store.ListCollectionGrantsParams{CompanyID: companyID, CollectionID: c.ID})
		if err != nil {
			return nil, fmt.Errorf("list collection grants: %w", err)
		}
		for _, g := range grants {
			st := domain.CollectionSubject(g.SubjectType)
			name := s.subjectName(t.ctx, t.q, companyID, st, g.SubjectID)
			if st != domain.CollectionSubjectAllPersonas && name == "" {
				continue // sujeito apagado: a concessão não aponta mais para ninguém
			}
			hc.Grants = append(hc.Grants, hkvGrant{SubjectType: g.SubjectType, Subject: name, Permission: g.Permission})
		}
		out = append(out, hc)
	}
	return out, nil
}

// ImportVault abre um .hkv exportado por outra instância e grava o que ele leva, tudo ou nada (admin, reautenticação
// recente). Segredo que já existe pelo nome fica como está (Existing). Concessão a persona ou humano só vale se o
// sujeito existir no destino, e só em coleção que o import criou; as outras contam em Skipped.
func (s *Service) ImportVault(ctx context.Context, actor domain.Actor, companyID domain.ID, file []byte, password string) (VaultImportResult, error) {
	if err := s.requireVaultTransfer(ctx, actor, companyID); err != nil {
		return VaultImportResult{}, err
	}
	if !s.secretBox().Enabled() {
		return VaultImportResult{}, domain.Invalid("secrets", "HANKO_SECRET_KEY não está configurada neste servidor; sem ela não há como guardar segredos")
	}
	plain, err := vaultfile.Open(file, password)
	switch {
	case errors.Is(err, vaultfile.ErrFormat):
		return VaultImportResult{}, domain.Invalid("file", err.Error())
	case errors.Is(err, vaultfile.ErrPassword):
		return VaultImportResult{}, domain.Invalid("password", err.Error())
	case err != nil:
		return VaultImportResult{}, err
	}
	var doc hkvDoc
	dec := json.NewDecoder(bytes.NewReader(plain))
	if err := dec.Decode(&doc); err != nil {
		return VaultImportResult{}, domain.Invalid("file", "conteúdo do cofre ilegível")
	}
	if doc.Version != hkvVersion {
		return VaultImportResult{}, domain.Invalid("file", fmt.Sprintf("versão %d do cofre não é suportada (esta instância lê a %d)", doc.Version, hkvVersion))
	}
	if err := s.validateHkv(doc); err != nil {
		return VaultImportResult{}, err
	}
	var res VaultImportResult
	err = s.withTx(ctx, func(t *txc) error {
		created := map[string]store.Secret{}
		for _, hs := range doc.Secrets {
			sec, ok, err := s.importSecret(t, actor, companyID, hs, &res)
			if err != nil {
				return fmt.Errorf("segredo %s: %w", hs.Name, err)
			}
			if ok {
				created[hs.Name] = sec
			}
		}
		for _, hc := range doc.Collections {
			if err := s.importCollection(t, actor, companyID, hc, created, &res); err != nil {
				return fmt.Errorf("coleção %s: %w", hc.Name, err)
			}
		}
		return t.activity(actor, companyID, "vault.imported", "company", companyID, map[string]any{
			"secrets": res.Secrets, "fields": res.Fields, "existing": res.Existing, "collections": res.Collections,
			"grants": res.Grants, "skipped": res.Skipped}, "")
	})
	if err != nil {
		return VaultImportResult{}, err
	}
	return res, nil
}

// validateHkv confere o conteúdo antes de abrir a transação: nomes, campos e tamanhos que o banco recusaria.
func (s *Service) validateHkv(doc hkvDoc) error {
	if len(doc.Secrets) > maxSecretsPerCompany {
		return domain.Invalid("file", fmt.Sprintf("o arquivo tem mais de %d segredos", maxSecretsPerCompany))
	}
	seen := map[string]bool{}
	for _, hs := range doc.Secrets {
		name, err := s.validateSecretName(hs.Name)
		if err != nil || name != hs.Name || seen[hs.Name] {
			return domain.Invalid("file", fmt.Sprintf("segredo com nome inválido ou repetido: %q", hs.Name))
		}
		seen[hs.Name] = true
		if _, err := validateSecretDescription(hs.Description); err != nil {
			return domain.Invalid("file", fmt.Sprintf("segredo %s: %v", hs.Name, err))
		}
		if hs.RotateAfterDays != nil && (*hs.RotateAfterDays < 1 || *hs.RotateAfterDays > 3650) {
			return domain.Invalid("file", fmt.Sprintf("segredo %s: prazo de rotação fora de 1 a 3650 dias", hs.Name))
		}
		if len(hs.Fields) == 0 || len(hs.Fields) > domain.MaxSecretFields {
			return domain.Invalid("file", fmt.Sprintf("segredo %s: de 1 a %d campos", hs.Name, domain.MaxSecretFields))
		}
		if err := validateHkvFields(hs); err != nil {
			return err
		}
	}
	return nil
}

func validateHkvFields(hs hkvSecret) error {
	names := map[string]bool{}
	for _, f := range hs.Fields {
		kind, provider := domain.SecretFieldKind(f.Kind), domain.SecretProvider(f.Provider)
		if names[f.Name] {
			return domain.Invalid("file", fmt.Sprintf("segredo %s: campo repetido %q", hs.Name, f.Name))
		}
		names[f.Name] = true
		if err := domain.ValidateSecretField(f.Name, kind, f.Sensitive, provider); err != nil {
			return domain.Invalid("file", fmt.Sprintf("segredo %s, campo %s: %v", hs.Name, f.Name, err))
		}
		if f.Name == domain.DefaultSecretField && kind != domain.SecretFieldText {
			return domain.Invalid("file", fmt.Sprintf("segredo %s: o campo value é texto", hs.Name))
		}
	}
	if !names[domain.DefaultSecretField] {
		return domain.Invalid("file", fmt.Sprintf("segredo %s: falta o campo value", hs.Name))
	}
	return nil
}

// importSecret cria o segredo e os campos do arquivo. Devolve false (sem erro) se o nome já existe no destino.
func (s *Service) importSecret(t *txc, actor domain.Actor, companyID domain.ID, hs hkvSecret, res *VaultImportResult) (store.Secret, bool, error) {
	exists, err := secretExists(t.ctx, t.q, companyID, hs.Name)
	if err != nil {
		return store.Secret{}, false, err
	}
	if exists {
		res.Existing++
		return store.Secret{}, false, nil
	}
	var value hkvField
	for _, f := range hs.Fields {
		if f.Name == domain.DefaultSecretField {
			value = f
		}
	}
	id := domain.NewID()
	m, err := s.prepareMaterial(id, domain.SecretProvider(value.Provider), value.Value, false, domain.GenOptions{}, value.ExternalRef)
	if err != nil {
		return store.Secret{}, false, fmt.Errorf("campo value: %w", err)
	}
	sec, err := s.createSecretRowTx(t, actor, companyID, domain.SecretScopeCompany, nil, id, hs.Name, hs.Description, m)
	if err != nil {
		return store.Secret{}, false, err
	}
	if hs.RotateAfterDays != nil {
		days := i32(*hs.RotateAfterDays)
		if _, err := t.q.UpdateSecretMeta(t.ctx, store.UpdateSecretMetaParams{ID: sec.ID, CompanyID: companyID,
			Description: sec.Description, RotateAfterDays: &days, Now: t.now}); err != nil {
			return store.Secret{}, false, mapErr(err, "update secret meta")
		}
	}
	if err := t.activity(actor, companyID, "secret.created", "secret", sec.ID, secretEventData(sec), ""); err != nil {
		return store.Secret{}, false, err
	}
	res.Secrets++
	res.Fields++
	for _, f := range hs.Fields {
		if f.Name == domain.DefaultSecretField {
			continue
		}
		sensitive := f.Sensitive
		in := CreateSecretFieldInput{Name: f.Name, Kind: domain.SecretFieldKind(f.Kind), Sensitive: &sensitive, Position: f.Position,
			SecretFieldValueInput: SecretFieldValueInput{Provider: domain.SecretProvider(f.Provider), Value: f.Value, ExternalRef: f.ExternalRef},
			File:                  f.Content, FileName: f.FileName, ContentType: f.ContentType}
		if _, err := s.createFieldTx(t, actor, companyID, sec, in); err != nil {
			return store.Secret{}, false, fmt.Errorf("campo %s: %w", f.Name, err)
		}
		res.Fields++
	}
	return sec, true, nil
}

// importCollection acha ou cria a coleção, põe nela os segredos que o import criou e aplica as concessões se a coleção é nova.
func (s *Service) importCollection(t *txc, actor domain.Actor, companyID domain.ID, hc hkvCollection, created map[string]store.Secret, res *VaultImportResult) error {
	col, isNew, err := s.findOrCreateImportedCollection(t, actor, companyID, hc, res)
	if err != nil {
		return err
	}
	for _, name := range hc.Items {
		if sec, ok := created[name]; ok {
			if err := s.addToCollectionTx(t, actor, companyID, col, sec.ID, sec.Name); err != nil {
				return err
			}
		}
	}
	if hc.IsDefault {
		return nil // a padrão já nasce concedida a todas as personas (ADR-0068 §2.6)
	}
	if !isNew {
		res.Skipped += len(hc.Grants)
		return nil
	}
	for _, g := range hc.Grants {
		if err := s.importGrant(t, actor, companyID, col, g, res); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) findOrCreateImportedCollection(t *txc, actor domain.Actor, companyID domain.ID, hc hkvCollection, res *VaultImportResult) (store.SecretCollection, bool, error) {
	if hc.IsDefault {
		col, err := s.ensureDefaultCollectionTx(t, actor, companyID)
		return col, false, err
	}
	if col, err := t.q.GetSecretCollectionByName(t.ctx, store.GetSecretCollectionByNameParams{CompanyID: companyID, Name: hc.Name}); err == nil {
		return col, false, nil
	} else if !isNoRows(err) {
		return store.SecretCollection{}, false, fmt.Errorf("get collection: %w", err)
	}
	name, desc, err := validateCollection(CollectionInput{Name: hc.Name, Description: hc.Description})
	if err != nil {
		return store.SecretCollection{}, false, err
	}
	var rot *int32
	if hc.RotateAfterDays != nil {
		rot = ptr(i32(*hc.RotateAfterDays))
	}
	col, err := t.q.CreateSecretCollection(t.ctx, store.CreateSecretCollectionParams{ID: domain.NewID(), CompanyID: companyID,
		Name: name, Description: desc, RotateAfterDays: rot, CreatedByType: string(actor.Type), CreatedByID: actor.ID, Now: t.now})
	if err != nil {
		return store.SecretCollection{}, false, mapErr(err, "create secret collection")
	}
	res.Collections++
	return col, true, t.activity(actor, companyID, "collection.created", "collection", col.ID, map[string]any{"name": col.Name}, "")
}

// importGrant aplica uma concessão do arquivo; sujeito que não existe no destino conta em Skipped.
func (s *Service) importGrant(t *txc, actor domain.Actor, companyID domain.ID, col store.SecretCollection, g hkvGrant, res *VaultImportResult) error {
	in := GrantInput{SubjectType: domain.CollectionSubject(g.SubjectType), Subject: g.Subject, Permission: domain.CollectionPermission(g.Permission)}
	subject, err := s.resolveGrantSubject(t.ctx, t.q, companyID, in)
	if err != nil {
		if errors.Is(err, domain.ErrInvalid) {
			res.Skipped++
			return nil
		}
		return err
	}
	if err := domain.ValidateCollectionGrant(in.SubjectType, subject != nil, in.Permission); err != nil {
		return err
	}
	if _, err := t.q.CreateCollectionGrant(t.ctx, store.CreateCollectionGrantParams{ID: domain.NewID(), CompanyID: companyID,
		CollectionID: col.ID, SubjectType: g.SubjectType, SubjectID: subject, Permission: g.Permission,
		CreatedByType: string(actor.Type), CreatedByID: actor.ID, Now: t.now}); err != nil {
		return mapErr(err, "create collection grant")
	}
	res.Grants++
	return t.activity(actor, companyID, "collection.granted", "collection", col.ID,
		map[string]any{"subject_type": g.SubjectType, "subject": g.Subject, "permission": g.Permission}, "")
}
