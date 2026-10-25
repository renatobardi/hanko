package api

import (
	"context"
	"encoding/base64"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Operações da fatia 1 do cofre (#56) que ligam o service: campos (criar, listar, mudar marca e posição, trocar valor,
// revelar, apagar, histórico e restaurar) e coleções (criar, listar, obter, mudar, apagar, pôr e tirar segredos,
// concessões). As ações sensíveis (revelar, apagar campo, histórico, restaurar, conceder e revogar) exigem
// reautenticação recente (ADR-0070): o service pede a prova e responde 401 reauth_required sem ela.

// vaultBackend é o subconjunto do service do cofre da fatia 1; opcional como secretsBackend.
type vaultBackend interface {
	CreateSecretField(ctx context.Context, actor domain.Actor, companyID domain.ID, scope domain.SecretScope, secretName string, in service.CreateSecretFieldInput) (service.SecretFieldView, error)
	ListSecretFields(ctx context.Context, companyID domain.ID, scope domain.SecretScope, secretName string) ([]service.SecretFieldView, error)
	UpdateSecretFieldMeta(ctx context.Context, actor domain.Actor, companyID domain.ID, scope domain.SecretScope, secretName, fieldName string, sensitive *bool, position *int) (service.SecretFieldView, error)
	RotateSecretField(ctx context.Context, actor domain.Actor, companyID domain.ID, scope domain.SecretScope, secretName, fieldName string, in service.SecretFieldValueInput) (service.SecretFieldView, error)
	RevealSecretField(ctx context.Context, actor domain.Actor, companyID domain.ID, scope domain.SecretScope, secretName, fieldName string) (service.SecretFieldView, error)
	DeleteSecretField(ctx context.Context, actor domain.Actor, companyID domain.ID, scope domain.SecretScope, secretName, fieldName string) error
	ListSecretFieldHistory(ctx context.Context, actor domain.Actor, companyID domain.ID, scope domain.SecretScope, secretName, fieldName string) ([]domain.SecretFieldVersion, error)
	RevealSecretFieldVersion(ctx context.Context, actor domain.Actor, companyID domain.ID, scope domain.SecretScope, secretName, fieldName string, version int) (domain.SecretFieldVersion, string, error)
	RestoreSecretFieldVersion(ctx context.Context, actor domain.Actor, companyID domain.ID, scope domain.SecretScope, secretName, fieldName string, version int) (service.SecretFieldView, error)
	CreateCollection(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CollectionInput) (service.CollectionView, error)
	ListCollections(ctx context.Context, companyID domain.ID) ([]service.CollectionView, error)
	GetCollection(ctx context.Context, companyID domain.ID, idOrName string) (service.CollectionView, error)
	UpdateCollection(ctx context.Context, actor domain.Actor, companyID domain.ID, idOrName string, in service.CollectionInput) (service.CollectionView, error)
	DeleteCollection(ctx context.Context, actor domain.Actor, companyID domain.ID, idOrName string) error
	ListCollectionSecrets(ctx context.Context, companyID domain.ID, idOrName string) ([]domain.Secret, error)
	AddSecretToCollection(ctx context.Context, actor domain.Actor, companyID domain.ID, idOrName, secretName string) error
	RemoveSecretFromCollection(ctx context.Context, actor domain.Actor, companyID domain.ID, idOrName, secretName string) error
	ListCollectionGrants(ctx context.Context, companyID domain.ID, idOrName string) ([]service.CollectionGrantView, error)
	CreateCollectionGrant(ctx context.Context, actor domain.Actor, companyID domain.ID, idOrName string, in service.GrantInput) (service.CollectionGrantView, error)
	DeleteCollectionGrant(ctx context.Context, actor domain.Actor, companyID domain.ID, idOrName string, grantID domain.ID) error
	GenerateValue(ctx context.Context, o domain.GenOptions) (domain.Generated, error)
	VaultHealth(ctx context.Context, companyID domain.ID) (service.HealthReport, error)
	UpdateSecretMeta(ctx context.Context, actor domain.Actor, companyID domain.ID, name string, in service.SecretMetaInput) (service.SecretView, error)
	PreviewEnvImport(ctx context.Context, companyID domain.ID, in service.EnvImportInput) (service.EnvImportPlan, error)
	ImportEnv(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.EnvImportInput) (service.VaultImportResult, error)
	ExportVault(ctx context.Context, actor domain.Actor, companyID domain.ID, password string) (service.VaultExportResult, error)
	ImportVault(ctx context.Context, actor domain.Actor, companyID domain.ID, file []byte, password string) (service.VaultImportResult, error)
	CreateSecretDelegation(ctx context.Context, actor domain.Actor, companyID domain.ID, name string, in service.CreateDelegationInput) (service.DelegationView, error)
	ListMyDelegations(ctx context.Context, companyID domain.ID, limit int) ([]service.DelegationView, error)
	RevokeSecretDelegation(ctx context.Context, actor domain.Actor, companyID, id domain.ID) error
	ListUserSecretUsage(ctx context.Context, companyID domain.ID, name string, limit int) ([]service.SecretAccessView, error)
	ListSecretUsage(ctx context.Context, companyID domain.ID, name string, limit int) ([]service.SecretAccessView, error)
}

// vaultOp põe a guarda do cofre (run token, backend, empresa e ator) antes de cada operação da API.
func vaultOp[I interface{ companyRef() string }, B any](s *server, run func(ctx context.Context, vb vaultBackend, sc scope, in *I) (*B, error)) func(context.Context, *I) (*B, error) {
	return func(ctx context.Context, in *I) (*B, error) {
		vb, sc, err := s.vaultCall(ctx, (*in).companyRef())
		if err != nil {
			return nil, err
		}
		return run(ctx, vb, sc, in)
	}
}

// companyRef devolve a empresa do caminho, comum a todas as entradas do cofre.
func (p CompanyPath) companyRef() string { return p.Company }

// vaultDone devolve a resposta sem corpo das operações do cofre (apagar, pôr, tirar).
func (s *server) vaultDone(ctx context.Context, err error) (*struct{}, error) {
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	return &struct{}{}, nil
}

// vaultItem devolve o item das operações do cofre; o erro do service vira problema da API.
func vaultItem[T any](ctx context.Context, s *server, body T, err error) (*ItemOut[T], error) {
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	return &ItemOut[T]{Body: body}, nil
}

// vaultCall faz o que toda operação do cofre precisa antes do service: barra run token, acha o backend e resolve
// a empresa e o ator.
func (s *server) vaultCall(ctx context.Context, company string) (vaultBackend, scope, error) {
	if err := forbidPersona(ctx); err != nil {
		return nil, scope{}, err
	}
	vb, err := s.vault()
	if err != nil {
		return nil, scope{}, err
	}
	sc, err := s.scope(ctx, company)
	return vb, sc, err
}

func (s *server) vault() (vaultBackend, error) {
	b, ok := s.svc.(vaultBackend)
	if !ok {
		return nil, problem(http.StatusNotImplemented, "cofre indisponível neste servidor")
	}
	return b, nil
}

// secretFieldOut mostra um campo sem valor; o valor sai só quando o service o devolveu (gerado ou revelado).
func secretFieldOut(v service.SecretFieldView, secretName string, scope domain.SecretScope) SecretField {
	ref := "${secret:" + secretName + "#" + v.Name + "}"
	if scope == domain.SecretScopeUser {
		ref = "${secret:user/" + secretName + "#" + v.Name + "}"
	}
	return SecretField{ID: idStr(v.ID), Name: v.Name, Reference: ref, Kind: string(v.Kind), Sensitive: v.Sensitive,
		Provider: string(v.Provider), ExternalRef: v.ExternalRef, FileName: v.FileName, ContentType: v.ContentType,
		SizeBytes: v.SizeBytes, Position: v.Position, Version: v.Version, Revealed: v.RevealedAt != nil,
		VisibleText: v.VisibleText, Value: v.RevealedValue, LastUsedAt: v.LastUsedAt, RotatedAt: v.RotatedAt,
		CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
}

// fieldVersionOut mostra uma versão anterior de um campo; o valor só sai quando o service o revelou.
func fieldVersionOut(v domain.SecretFieldVersion, value string) SecretFieldVersion {
	return SecretFieldVersion{Version: v.Version, Provider: string(v.Provider), Revealed: v.RevealedAt != nil,
		Value: value, ReplacedBy: actorRef(v.ReplacedBy), CreatedAt: v.CreatedAt, ReplacedAt: v.ReplacedAt,
		RevealedAt: v.RevealedAt}
}

func fieldValueIn(v SecretFieldValue) service.SecretFieldValueInput {
	return service.SecretFieldValueInput{Provider: domain.SecretProvider(v.Provider), Value: v.Value,
		Generate: v.Generate, Gen: v.Generator.opts(), ExternalRef: v.ExternalRef}
}

// maxFieldBodyBytes é o corpo que cabe um campo arquivo de 1 MiB em base64 (ADR-0068 §5.1), com folga para os demais
// campos. O padrão do huma é 1 MiB e recusaria o arquivo de 1 MiB com 413.
var maxFieldBodyBytes = base64.StdEncoding.EncodedLen(domain.MaxSecretFieldFileBytes) + 64<<10

// registerFieldWrites registra as operações de campo que já ligam o service (criar, listar, mudar, trocar valor).
func (s *server) registerFieldWrites(api huma.API, secretScope domain.SecretScope, seg, opPrefix, what string) {
	base := "/companies/{company}/" + seg + "/{name}/fields"
	tag := "vault"

	createField := created(op("create-"+opPrefix+"-field", http.MethodPost, base, tag, "Cria um campo num "+what))
	createField.MaxBodyBytes = int64(maxFieldBodyBytes)
	huma.Register(api, createField,
		vaultOp(s, func(ctx context.Context, vb vaultBackend, sc scope, in *createSecretFieldIn) (*ItemOut[SecretField], error) {
			b := in.Body
			kind := domain.SecretFieldKind(b.Kind)
			fi := service.CreateSecretFieldInput{Name: b.Name, Kind: kind, Sensitive: b.Sensitive, Position: b.Position,
				SecretFieldValueInput: fieldValueIn(b.SecretFieldValue), FileName: b.FileName, ContentType: b.ContentType}
			if b.ContentBase64 != "" { // o conteúdo de campo arquivo (ADR-0068 §5.1): até 1 MiB, cifrado antes do driver
				if kind != domain.SecretFieldFile {
					return nil, problem(http.StatusUnprocessableEntity, "content_base64 só vale para campo do tipo file")
				}
				content, err := base64.StdEncoding.DecodeString(b.ContentBase64)
				if err != nil {
					return nil, problem(http.StatusUnprocessableEntity, "content_base64 não é base64 válido")
				}
				fi.File = content
			}
			v, err := vb.CreateSecretField(ctx, sc.Actor, sc.Company.ID, secretScope, in.Name, fi)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[SecretField]{Body: secretFieldOut(v, in.Name, secretScope)}, nil
		}))

	huma.Register(api, op("list-"+opPrefix+"-fields", http.MethodGet, base, tag,
		"Lista os campos de um "+what+" (metadados; valor só dos campos visíveis)"),
		vaultOp(s, func(ctx context.Context, vb vaultBackend, sc scope, in *listSecretFieldsIn) (*ListOut[SecretField], error) {
			list, err := vb.ListSecretFields(ctx, sc.Company.ID, secretScope, in.Name)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listOf(list, in.Page, func(v service.SecretFieldView) SecretField { return secretFieldOut(v, in.Name, secretScope) }), nil
		}))

	huma.Register(api, op("update-"+opPrefix+"-field", http.MethodPatch, base+"/{field}", tag,
		"Muda marca de visível e posição de um campo"),
		vaultOp(s, func(ctx context.Context, vb vaultBackend, sc scope, in *updateSecretFieldIn) (*ItemOut[SecretField], error) {
			v, err := vb.UpdateSecretFieldMeta(ctx, sc.Actor, sc.Company.ID, secretScope, in.Name, in.Field, in.Body.Sensitive, in.Body.Position)
			return vaultItem(ctx, s, secretFieldOut(v, in.Name, secretScope), err)
		}))

	huma.Register(api, op("rotate-"+opPrefix+"-field", http.MethodPost, base+"/{field}/rotate", tag,
		"Troca o valor de um campo; o anterior vai para o histórico"),
		vaultOp(s, func(ctx context.Context, vb vaultBackend, sc scope, in *rotateSecretFieldIn) (*ItemOut[SecretField], error) {
			if in.Body.ContentBase64 != "" {
				return nil, problem(http.StatusUnprocessableEntity, "campo arquivo não se troca: apague o campo e crie outro com o arquivo novo")
			}
			v, err := vb.RotateSecretField(ctx, sc.Actor, sc.Company.ID, secretScope, in.Name, in.Field, fieldValueIn(in.Body))
			return vaultItem(ctx, s, secretFieldOut(v, in.Name, secretScope), err)
		}))
}

// collectionOut mostra uma coleção com a contagem de segredos.
func collectionOut(v service.CollectionView) SecretCollection {
	return SecretCollection{ID: idStr(v.ID), Name: v.Name, Description: v.Description, IsDefault: v.IsDefault,
		RotateAfterDays: v.RotateAfterDays, Secrets: v.Secrets, CreatedBy: actorRef(v.CreatedBy),
		CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
}

func collectionInput(b collectionBody) service.CollectionInput {
	return service.CollectionInput{Name: b.Name, Description: b.Description, RotateAfterDays: b.RotateAfterDays}
}

// grantOut mostra uma concessão com o nome do sujeito (slug da persona ou e-mail do humano).
func grantOut(g service.CollectionGrantView) CollectionGrant {
	o := CollectionGrant{ID: idStr(g.ID), SubjectType: string(g.SubjectType), SubjectName: g.SubjectName,
		Permission: string(g.Permission), CreatedBy: actorRef(g.CreatedBy), CreatedAt: g.CreatedAt}
	if g.SubjectID != nil {
		o.SubjectID = idStr(*g.SubjectID)
	}
	return o
}

// registerCollectionOps registra as operações de coleção que ligam o service (sem as concessões, que pedem
// reautenticação).
func (s *server) registerCollectionOps(api huma.API) {
	base := "/companies/{company}/secret-collections"
	tag := "vault"

	huma.Register(api, created(op("create-secret-collection", http.MethodPost, base, tag, "Cria uma coleção de segredos (admin)")),
		vaultOp(s, func(ctx context.Context, vb vaultBackend, sc scope, in *createCollectionIn) (*ItemOut[SecretCollection], error) {
			v, err := vb.CreateCollection(ctx, sc.Actor, sc.Company.ID, collectionInput(in.Body))
			return vaultItem(ctx, s, collectionOut(v), err)
		}))

	huma.Register(api, op("list-secret-collections", http.MethodGet, base, tag, "Lista as coleções que o usuário alcança"),
		vaultOp(s, func(ctx context.Context, vb vaultBackend, sc scope, in *listCollectionsIn) (*ListOut[SecretCollection], error) {
			list, err := vb.ListCollections(ctx, sc.Company.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listOf(list, in.Page, collectionOut), nil
		}))

	huma.Register(api, op("get-secret-collection", http.MethodGet, base+"/{collection}", tag, "Obtém uma coleção"),
		vaultOp(s, func(ctx context.Context, vb vaultBackend, sc scope, in *CollectionPath) (*ItemOut[SecretCollection], error) {
			v, err := vb.GetCollection(ctx, sc.Company.ID, in.Collection)
			return vaultItem(ctx, s, collectionOut(v), err)
		}))

	huma.Register(api, op("update-secret-collection", http.MethodPatch, base+"/{collection}", tag,
		"Muda nome, descrição e prazo de rotação de uma coleção (admin)"),
		vaultOp(s, func(ctx context.Context, vb vaultBackend, sc scope, in *updateCollectionIn) (*ItemOut[SecretCollection], error) {
			v, err := vb.UpdateCollection(ctx, sc.Actor, sc.Company.ID, in.Collection, collectionInput(in.Body))
			return vaultItem(ctx, s, collectionOut(v), err)
		}))

	huma.Register(api, noContent(op("delete-secret-collection", http.MethodDelete, base+"/{collection}", tag,
		"Apaga uma coleção; os segredos continuam existindo (admin; a padrão não se apaga)")),
		vaultOp(s, func(ctx context.Context, vb vaultBackend, sc scope, in *CollectionPath) (*struct{}, error) {
			return s.vaultDone(ctx, vb.DeleteCollection(ctx, sc.Actor, sc.Company.ID, in.Collection))
		}))

	huma.Register(api, op("list-collection-secrets", http.MethodGet, base+"/{collection}/secrets", tag, "Lista os segredos de uma coleção"),
		vaultOp(s, func(ctx context.Context, vb vaultBackend, sc scope, in *listCollectionPageIn) (*ListOut[Secret], error) {
			list, err := vb.ListCollectionSecrets(ctx, sc.Company.ID, in.Collection)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listOf(list, in.Page, func(d domain.Secret) Secret { return secretOut(service.SecretView{Secret: d}) }), nil
		}))

	huma.Register(api, noContent(op("add-secret-to-collection", http.MethodPut, base+"/{collection}/secrets/{name}", tag,
		"Põe um segredo da empresa na coleção (admin ou manage)")),
		vaultOp(s, func(ctx context.Context, vb vaultBackend, sc scope, in *collectionSecretPath) (*struct{}, error) {
			return s.vaultDone(ctx, vb.AddSecretToCollection(ctx, sc.Actor, sc.Company.ID, in.Collection, in.Name))
		}))

	huma.Register(api, noContent(op("remove-secret-from-collection", http.MethodDelete, base+"/{collection}/secrets/{name}", tag,
		"Tira um segredo da coleção (admin ou manage)")),
		vaultOp(s, func(ctx context.Context, vb vaultBackend, sc scope, in *collectionSecretPath) (*struct{}, error) {
			return s.vaultDone(ctx, vb.RemoveSecretFromCollection(ctx, sc.Actor, sc.Company.ID, in.Collection, in.Name))
		}))

	huma.Register(api, op("list-collection-grants", http.MethodGet, base+"/{collection}/grants", tag, "Lista quem alcança a coleção"),
		vaultOp(s, func(ctx context.Context, vb vaultBackend, sc scope, in *listCollectionPageIn) (*ListOut[CollectionGrant], error) {
			list, err := vb.ListCollectionGrants(ctx, sc.Company.ID, in.Collection)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listOf(list, in.Page, grantOut), nil
		}))
}

// registerFieldSensitive liga as ações sensíveis do campo (revelar, apagar, histórico e restaurar). Cada uma exige
// reautenticação recente (ADR-0070 §1): o service pede a prova e a API responde 401 reauth_required sem ela.
func (s *server) registerFieldSensitive(api huma.API, secretScope domain.SecretScope, seg, opPrefix string) {
	base := "/companies/{company}/" + seg + "/{name}/fields"
	tag := "vault"

	huma.Register(api, op("reveal-"+opPrefix+"-field", http.MethodPost, base+"/{field}/reveal", tag,
		"Mostra o valor de um campo UMA vez por versão (exige reautenticação recente)"),
		vaultOp(s, func(ctx context.Context, vb vaultBackend, sc scope, in *SecretFieldPath) (*ItemOut[SecretField], error) {
			v, err := vb.RevealSecretField(ctx, sc.Actor, sc.Company.ID, secretScope, in.Name, in.Field)
			return vaultItem(ctx, s, secretFieldOut(v, in.Name, secretScope), err)
		}))

	huma.Register(api, noContent(op("delete-"+opPrefix+"-field", http.MethodDelete, base+"/{field}", tag,
		"Apaga um campo e o seu histórico (exige reautenticação recente)")),
		vaultOp(s, func(ctx context.Context, vb vaultBackend, sc scope, in *SecretFieldPath) (*struct{}, error) {
			return s.vaultDone(ctx, vb.DeleteSecretField(ctx, sc.Actor, sc.Company.ID, secretScope, in.Name, in.Field))
		}))

	huma.Register(api, op("list-"+opPrefix+"-field-history", http.MethodGet, base+"/{field}/history", tag,
		"Lista as versões anteriores de um campo (exige reautenticação recente)"),
		vaultOp(s, func(ctx context.Context, vb vaultBackend, sc scope, in *SecretFieldPath) (*ListOut[SecretFieldVersion], error) {
			list, err := vb.ListSecretFieldHistory(ctx, sc.Actor, sc.Company.ID, secretScope, in.Name, in.Field)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(list, Page{Limit: len(list)}, func(v domain.SecretFieldVersion) SecretFieldVersion {
				return fieldVersionOut(v, "")
			}), nil
		}))

	huma.Register(api, op("reveal-"+opPrefix+"-field-version", http.MethodPost,
		base+"/{field}/history/{version}/reveal", tag, "Mostra uma versão anterior UMA vez (exige reautenticação recente)"),
		vaultOp(s, func(ctx context.Context, vb vaultBackend, sc scope, in *secretFieldVersionPath) (*ItemOut[SecretFieldVersion], error) {
			v, val, err := vb.RevealSecretFieldVersion(ctx, sc.Actor, sc.Company.ID, secretScope, in.Name, in.Field, in.Version)
			return vaultItem(ctx, s, fieldVersionOut(v, val), err)
		}))

	huma.Register(api, op("restore-"+opPrefix+"-field-version", http.MethodPost,
		base+"/{field}/history/{version}/restore", tag, "Restaura uma versão anterior como versão nova (exige reautenticação recente)"),
		vaultOp(s, func(ctx context.Context, vb vaultBackend, sc scope, in *secretFieldVersionPath) (*ItemOut[SecretField], error) {
			v, err := vb.RestoreSecretFieldVersion(ctx, sc.Actor, sc.Company.ID, secretScope, in.Name, in.Field, in.Version)
			return vaultItem(ctx, s, secretFieldOut(v, in.Name, secretScope), err)
		}))
}

// registerGrantOps liga conceder e revogar concessão de coleção (admin), que exigem reautenticação recente
// (ADR-0070 §1).
func (s *server) registerGrantOps(api huma.API) {
	base := "/companies/{company}/secret-collections"
	tag := "vault"

	huma.Register(api, created(op("create-collection-grant", http.MethodPost, base+"/{collection}/grants", tag,
		"Concede a coleção a persona, subárvore, todas as personas ou humano (admin; exige reautenticação recente)")),
		vaultOp(s, func(ctx context.Context, vb vaultBackend, sc scope, in *createGrantIn) (*ItemOut[CollectionGrant], error) {
			g, err := vb.CreateCollectionGrant(ctx, sc.Actor, sc.Company.ID, in.Collection, service.GrantInput{
				SubjectType: domain.CollectionSubject(in.Body.SubjectType), Subject: in.Body.Subject,
				Permission: domain.CollectionPermission(in.Body.Permission)})
			return vaultItem(ctx, s, grantOut(g), err)
		}))

	huma.Register(api, noContent(op("delete-collection-grant", http.MethodDelete, base+"/{collection}/grants/{grant}", tag,
		"Revoga uma concessão (admin; exige reautenticação recente)")),
		vaultOp(s, func(ctx context.Context, vb vaultBackend, sc scope, in *grantPath) (*struct{}, error) {
			gid, err := domain.ParseID(in.Grant)
			if err != nil {
				return nil, s.fail(ctx, domain.Invalid("grant", "não é um uuid"))
			}
			return s.vaultDone(ctx, vb.DeleteCollectionGrant(ctx, sc.Actor, sc.Company.ID, in.Collection, gid))
		}))
}
