package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/settings"
)

// Segredos (ADR-0032). Não existe leitura de valor: o valor sai UMA vez, na resposta de uma criação/rotação com
// valor gerado pelo servidor ou na de "revelar". Run tokens (personas) nunca chegam aqui.

// secretsBackend é o subconjunto do service para segredos; opcional como integrationsBackend.
type secretsBackend interface {
	CreateSecret(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateSecretInput) (service.SecretView, error)
	RotateSecret(ctx context.Context, actor domain.Actor, companyID domain.ID, scope domain.SecretScope, name string, in service.RotateSecretInput) (service.SecretView, error)
	RevealSecret(ctx context.Context, actor domain.Actor, companyID domain.ID, scope domain.SecretScope, name string) (service.SecretView, error)
	DeleteSecret(ctx context.Context, actor domain.Actor, companyID domain.ID, scope domain.SecretScope, name string, force bool) error
	ListSecrets(ctx context.Context, companyID domain.ID, scope domain.SecretScope, withUsage bool) ([]service.SecretView, error)
	GetSecret(ctx context.Context, companyID domain.ID, scope domain.SecretScope, name string) (service.SecretView, error)
	MigratePersonaEnv(ctx context.Context) (int, error)
}

func (s *server) secrets() (secretsBackend, error) {
	b, ok := s.svc.(secretsBackend)
	if !ok {
		return nil, problem(http.StatusNotImplemented, "segredos indisponíveis neste servidor")
	}
	return b, nil
}

// SecretUse é um lugar que referencia o segredo.
type SecretUse struct {
	Kind string `json:"kind" enum:"persona,integration"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// Secret são os metadados de um segredo (nunca o valor).
type Secret struct {
	ID          string      `json:"id" format:"uuid"`
	CompanyID   string      `json:"company_id" format:"uuid"`
	Scope       string      `json:"scope" enum:"company,user"`
	Name        string      `json:"name" example:"openrouter-key"`
	Reference   string      `json:"reference" doc:"Como referenciar em integrações e no env da persona." example:"${secret:openrouter-key}"`
	Description string      `json:"description,omitempty"`
	Provider    string      `json:"provider" enum:"local,vault" doc:"local: cifrado no Hanko. vault: lido do cofre externo a cada despacho."`
	ExternalRef string      `json:"external_ref,omitempty" doc:"Provider vault: caminho#campo no cofre."`
	Version     int         `json:"version" doc:"Sobe a cada rotação."`
	Revealed    bool        `json:"revealed" doc:"O valor desta versão já foi mostrado (criação gerada ou revelar)."`
	RevealedAt  *time.Time  `json:"revealed_at,omitempty"`
	LastUsedAt  *time.Time  `json:"last_used_at,omitempty"`
	UsedBy      []SecretUse `json:"used_by,omitempty" doc:"Personas e integrações que referenciam o segredo (admin)."`
	Value       string      `json:"value,omitempty" doc:"SÓ na resposta de criação/rotação com valor gerado e na de revelar. Guarde-o: não é possível ler de novo."`
	CreatedBy   ActorRef    `json:"created_by"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

func secretOut(v service.SecretView) Secret {
	ref := "${secret:" + v.Name + "}"
	if v.Scope == domain.SecretScopeUser {
		ref = "${secret:user/" + v.Name + "}"
	}
	o := Secret{ID: idStr(v.ID), CompanyID: idStr(v.CompanyID), Scope: string(v.Scope), Name: v.Name, Reference: ref, Description: v.Description,
		Provider: string(v.Provider), ExternalRef: v.ExternalRef, Version: v.Version, Revealed: v.RevealedAt != nil, RevealedAt: v.RevealedAt,
		LastUsedAt: v.LastUsedAt, Value: v.RevealedValue, CreatedBy: actorRef(v.CreatedBy), CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
	for _, u := range v.UsedBy {
		o.UsedBy = append(o.UsedBy, SecretUse{Kind: u.Kind, Slug: u.Slug, Name: u.Name})
	}
	return o
}

type SecretPath struct {
	CompanyPath
	Name string `path:"name" doc:"Nome do segredo." example:"openrouter-key"`
}

type createSecretIn struct {
	CompanyPath
	Body struct {
		Name        string `json:"name" pattern:"^[A-Za-z][A-Za-z0-9_.-]{0,63}$" example:"openrouter-key"`
		Description string `json:"description,omitempty" maxLength:"300"`
		Provider    string `json:"provider,omitempty" enum:"local,vault" doc:"Padrão local."`
		Value       string `json:"value,omitempty" doc:"Provider local: o valor (até 8 KiB). Cifrado; nunca devolvido."`
		Generate    bool   `json:"generate,omitempty" doc:"Provider local: o servidor gera o valor e o devolve uma vez."`
		ExternalRef string `json:"external_ref,omitempty" doc:"Provider vault: caminho#campo no cofre externo."`
	}
}

type rotateSecretIn struct {
	SecretPath
	Body struct {
		Provider    string  `json:"provider,omitempty" enum:"local,vault"`
		Value       string  `json:"value,omitempty"`
		Generate    bool    `json:"generate,omitempty"`
		ExternalRef string  `json:"external_ref,omitempty"`
		Description *string `json:"description,omitempty" maxLength:"300"`
	}
}

type deleteSecretIn struct {
	SecretPath
	Force bool `query:"force" doc:"Apaga mesmo em uso por persona ou integração."`
}

type listSecretsIn struct {
	CompanyPath
	Page
}

// MigrateEnvResult é o resultado da migração do env em claro das personas.
type MigrateEnvResult struct {
	Personas int `json:"personas" doc:"Quantas personas tiveram o env movido para segredos cifrados."`
}

func (s *server) registerSecrets(api huma.API) {
	s.registerSecretsScope(api, domain.SecretScopeCompany, "secrets", "secret", "segredo da empresa")
	s.registerSecretsScope(api, domain.SecretScopeUser, "user-secrets", "user-secret", "segredo do usuário (só o dono vê e usa)")

	huma.Register(api, op("migrate-persona-env", http.MethodPost, "/secrets/migrate-persona-env", "secrets",
		"Move o env em claro das personas para segredos cifrados (dono da instalação; idempotente)"),
		func(ctx context.Context, _ *struct{}) (*ItemOut[MigrateEnvResult], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sb, err := s.secrets()
			if err != nil {
				return nil, err
			}
			n, err := sb.MigratePersonaEnv(ctx)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[MigrateEnvResult]{Body: MigrateEnvResult{Personas: n}}, nil
		})
}

func (s *server) registerSecretsScope(api huma.API, scope domain.SecretScope, seg, opPrefix, what string) {
	base := "/companies/{company}/" + seg
	tag := "secrets"

	huma.Register(api, created(op("create-"+opPrefix, http.MethodPost, base, tag, "Cria um "+what)),
		func(ctx context.Context, in *createSecretIn) (*ItemOut[Secret], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sb, err := s.secrets()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			b := in.Body
			v, err := sb.CreateSecret(ctx, sc.Actor, sc.Company.ID, service.CreateSecretInput{Scope: scope, Name: b.Name, Description: b.Description,
				Provider: domain.SecretProvider(b.Provider), Value: b.Value, Generate: b.Generate, ExternalRef: b.ExternalRef})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Secret]{Body: secretOut(v)}, nil
		})

	huma.Register(api, op("list-"+opPrefix+"s", http.MethodGet, base, tag, "Lista os "+what+"s (nomes e metadados; nunca valores)"),
		func(ctx context.Context, in *listSecretsIn) (*ListOut[Secret], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sb, err := s.secrets()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			list, err := sb.ListSecrets(ctx, sc.Company.ID, scope, true)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listOf(list, in.Page, secretOut), nil
		})

	huma.Register(api, op("get-"+opPrefix, http.MethodGet, base+"/{name}", tag, "Obtém os metadados de um "+what),
		func(ctx context.Context, in *SecretPath) (*ItemOut[Secret], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sb, err := s.secrets()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			v, err := sb.GetSecret(ctx, sc.Company.ID, scope, in.Name)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Secret]{Body: secretOut(v)}, nil
		})

	huma.Register(api, op("rotate-"+opPrefix, http.MethodPost, base+"/{name}/rotate", tag, "Troca o valor (ou a referência do cofre) de um "+what),
		func(ctx context.Context, in *rotateSecretIn) (*ItemOut[Secret], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sb, err := s.secrets()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			b := in.Body
			v, err := sb.RotateSecret(ctx, sc.Actor, sc.Company.ID, scope, in.Name, service.RotateSecretInput{
				Provider: domain.SecretProvider(b.Provider), Value: b.Value, Generate: b.Generate, ExternalRef: b.ExternalRef, Description: b.Description})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Secret]{Body: secretOut(v)}, nil
		})

	huma.Register(api, op("reveal-"+opPrefix, http.MethodPost, base+"/{name}/reveal", tag, "Mostra o valor de um "+what+" UMA vez por versão (409 nas seguintes)"),
		func(ctx context.Context, in *SecretPath) (*ItemOut[Secret], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sb, err := s.secrets()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			v, err := sb.RevealSecret(ctx, sc.Actor, sc.Company.ID, scope, in.Name)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Secret]{Body: secretOut(v)}, nil
		})

	huma.Register(api, noContent(op("delete-"+opPrefix, http.MethodDelete, base+"/{name}", tag, "Apaga um "+what)),
		func(ctx context.Context, in *deleteSecretIn) (*struct{}, error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sb, err := s.secrets()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			if err := sb.DeleteSecret(ctx, sc.Actor, sc.Company.ID, scope, in.Name, in.Force); err != nil {
				return nil, s.fail(ctx, err)
			}
			return &struct{}{}, nil
		})
}

// ---------- settings da instância ----------

// instanceBackend é o subconjunto do service para settings da instância e armazenamento de blobs.
type instanceBackend interface {
	ListInstanceSettings(ctx context.Context) ([]settings.Entry, error)
	SetInstanceSetting(ctx context.Context, actor domain.Actor, key string, raw json.RawMessage) (settings.Entry, error)
	UnsetInstanceSetting(ctx context.Context, actor domain.Actor, key string) (settings.Entry, error)
	ListInstanceSettingAudit(ctx context.Context, limit int) ([]service.InstanceSettingAudit, error)
	BlobStorageStatus(ctx context.Context) (service.BlobStatus, error)
}

func (s *server) instance() (instanceBackend, error) {
	b, ok := s.svc.(instanceBackend)
	if !ok {
		return nil, problem(http.StatusNotImplemented, "settings da instância indisponíveis neste servidor")
	}
	return b, nil
}

// InstanceSetting é uma chave de configuração da instalação com o valor efetivo.
type InstanceSetting struct {
	Key    string `json:"key" example:"acp.allow"`
	Group  string `json:"group"`
	Kind   string `json:"kind" enum:"bool,int,float,duration,string_list"`
	Label  string `json:"label"`
	Env    string `json:"env" doc:"Variável de ambiente equivalente; definida, ela vence e trava a chave."`
	Value  any    `json:"value" doc:"Valor efetivo."`
	Source string `json:"source" enum:"env,db,default"`
	Locked bool   `json:"locked" doc:"A variável de ambiente está definida: mudar por aqui é recusado (409)."`
	Stored any    `json:"stored,omitempty" doc:"Valor guardado no banco, mesmo quando travado."`
}

func settingOut(e settings.Entry) InstanceSetting {
	return InstanceSetting{Key: e.Key, Group: e.Group, Kind: string(e.Kind), Label: e.Label, Env: e.Env, Value: settingJSON(e.Value),
		Source: string(e.Source), Locked: e.Locked, Stored: settingJSON(e.Stored)}
}

func settingJSON(v any) any {
	if v == nil {
		return nil
	}
	var out any
	if json.Unmarshal(settings.ToJSON(v), &out) != nil {
		return nil
	}
	return out
}

// InstanceSettingAudit é uma mudança de setting.
type InstanceSettingAudit struct {
	ID         string          `json:"id" format:"uuid"`
	Key        string          `json:"key"`
	Old        json.RawMessage `json:"old,omitempty" doc:"Valor anterior (ausente: não havia)."`
	New        json.RawMessage `json:"new,omitempty" doc:"Novo valor (ausente: voltou ao padrão)."`
	ActorType  string          `json:"actor_type"`
	ActorID    string          `json:"actor_id,omitempty"`
	OccurredAt time.Time       `json:"occurred_at"`
}

func auditOut(a service.InstanceSettingAudit) InstanceSettingAudit {
	o := InstanceSettingAudit{ID: idStr(a.ID), Key: a.Key, Old: a.Old, New: a.New, ActorType: a.ActorType, OccurredAt: a.OccurredAt}
	if a.ActorID != nil {
		o.ActorID = idStr(*a.ActorID)
	}
	return o
}

type SettingKeyPath struct {
	Key string `path:"key" doc:"Chave da configuração." example:"acp.allow"`
}

type setSettingIn struct {
	SettingKeyPath
	Body struct {
		Value json.RawMessage `json:"value" doc:"Valor JSON da chave (bool, número, texto de duração ou lista, conforme o tipo)." example:"true"`
	}
}

type auditIn struct {
	Limit int `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

// BlobDriverUsage é o que há guardado em um driver de conteúdo de artefatos.
type BlobDriverUsage struct {
	Driver   string `json:"driver" enum:"postgres,s3"`
	Versions int64  `json:"versions"`
	Bytes    int64  `json:"bytes"`
}

// BlobStatus descreve o armazenamento de conteúdo de artefatos.
type BlobStatus struct {
	Active    string            `json:"active" doc:"Driver das escritas novas."`
	Available []string          `json:"available" doc:"Drivers configurados (leitura e migração)."`
	Usage     []BlobDriverUsage `json:"usage"`
}

func (s *server) registerInstance(api huma.API) {
	huma.Register(api, op("list-instance-settings", http.MethodGet, "/instance/settings", "instance", "Lista as configurações da instalação (dono da instalação)"),
		func(ctx context.Context, _ *struct{}) (*ListOut[InstanceSetting], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			ib, err := s.instance()
			if err != nil {
				return nil, err
			}
			list, err := ib.ListInstanceSettings(ctx)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(list, Page{Limit: len(list)}, settingOut), nil
		})

	huma.Register(api, op("set-instance-setting", http.MethodPut, "/instance/settings/{key}", "instance", "Define uma configuração (409 se a variável de ambiente a trava)"),
		func(ctx context.Context, in *setSettingIn) (*ItemOut[InstanceSetting], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			ib, err := s.instance()
			if err != nil {
				return nil, err
			}
			e, err := ib.SetInstanceSetting(ctx, principalFrom(ctx).Actor, in.Key, in.Body.Value)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[InstanceSetting]{Body: settingOut(e)}, nil
		})

	huma.Register(api, op("unset-instance-setting", http.MethodDelete, "/instance/settings/{key}", "instance", "Remove o valor do banco (volta ao padrão)"),
		func(ctx context.Context, in *SettingKeyPath) (*ItemOut[InstanceSetting], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			ib, err := s.instance()
			if err != nil {
				return nil, err
			}
			e, err := ib.UnsetInstanceSetting(ctx, principalFrom(ctx).Actor, in.Key)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[InstanceSetting]{Body: settingOut(e)}, nil
		})

	huma.Register(api, op("list-instance-settings-audit", http.MethodGet, "/instance/settings-audit", "instance", "Histórico das mudanças de configuração"),
		func(ctx context.Context, in *auditIn) (*ListOut[InstanceSettingAudit], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			ib, err := s.instance()
			if err != nil {
				return nil, err
			}
			list, err := ib.ListInstanceSettingAudit(ctx, in.Limit)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(list, Page{Limit: len(list)}, auditOut), nil
		})

	huma.Register(api, op("get-blob-status", http.MethodGet, "/instance/blobs", "instance", "Onde o conteúdo dos artefatos está guardado (dono da instalação)"),
		func(ctx context.Context, _ *struct{}) (*ItemOut[BlobStatus], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			ib, err := s.instance()
			if err != nil {
				return nil, err
			}
			st, err := ib.BlobStorageStatus(ctx)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			out := BlobStatus{Active: st.Active, Available: st.Available, Usage: []BlobDriverUsage{}}
			for _, u := range st.Usage {
				out.Usage = append(out.Usage, BlobDriverUsage{Driver: u.Driver, Versions: u.Versions, Bytes: u.Bytes})
			}
			return &ItemOut[BlobStatus]{Body: out}, nil
		})
}
