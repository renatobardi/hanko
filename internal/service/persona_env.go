package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/secretref"
	"github.com/renatobardi/hanko/internal/store"
)

// Variáveis de ambiente da persona pela tela (#117 fatia 3, ADR-0068 §5.2). A tela só manda referência a segredo:
// `${secret:NOME#campo}` ou, para campo arquivo, `${secretfile:NOME#campo}`. Valor em claro não passa por aqui.

// SetPersonaEnvRef põe a variável `name` do adapter_config.env da persona como a referência `ref`. `ref` vazio tira a
// variável. Só o admin; a referência é conferida contra os segredos da empresa; grava `persona.updated` (campo env, sem
// valor) na mesma transação e invalida a prontidão, como a troca de ambiente.
func (s *Service) SetPersonaEnvRef(ctx context.Context, actor domain.Actor, companyID, personaID domain.ID, name, ref string) (domain.Persona, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.Persona{}, err
	}
	if err := checkPersonaEnvInput(name, ref); err != nil {
		return domain.Persona{}, err
	}
	var out domain.Persona
	err := s.withTx(ctx, func(t *txc) error {
		row, err := t.q.GetPersonaByIDForUpdate(t.ctx, store.GetPersonaByIDForUpdateParams{CompanyID: companyID, ID: personaID})
		if err != nil {
			return mapErr(err, "get persona")
		}
		cfg, err := withEnvRef(row.AdapterConfig, name, ref)
		if err != nil {
			return err
		}
		if ref != "" {
			if err := s.checkAdapterSecretRefs(t.ctx, t.q, companyID, "adapter_config.env."+name, ref); err != nil {
				return err
			}
		}
		if err := t.q.SetPersonaAdapterConfig(t.ctx, store.SetPersonaAdapterConfigParams{CompanyID: companyID, ID: personaID, AdapterConfig: cfg, Now: t.now}); err != nil {
			return mapErr(err, "set persona adapter config")
		}
		if err := t.invalidateReadiness(companyID, personaID); err != nil {
			return err
		}
		out = personaFromRow(row)
		out.AdapterConfig = cfg
		return t.activity(actor, companyID, "persona.updated", "persona", personaID,
			map[string]any{"fields": []string{"env"}, "variable": name, "removed": ref == ""}, "")
	})
	return out, err
}

// checkPersonaEnvInput recusa nome de variável que o ambiente não aceita e referência que não é um único
// `${secret:}` ou `${secretfile:}`. `ref` vazio é a remoção e passa.
func checkPersonaEnvInput(name, ref string) error {
	if !domain.EnvNameRe.MatchString(name) {
		return domain.Invalid("name", "nome de variável inválido: letras, números e _, sem começar por número")
	}
	if ref != "" && !secretref.IsRef(ref) {
		return domain.Invalid("reference", "use ${secret:NOME#campo} ou ${secretfile:NOME#campo}")
	}
	return nil
}

// withEnvRef devolve a configuração do adapter com `adapter_config.env[name]` igual a `ref` (ou sem a variável, se
// `ref` for vazio). As demais chaves e variáveis ficam como estão, inclusive valores em claro de antes. Sem variável
// nenhuma, a chave `env` some.
func withEnvRef(cfg []byte, name, ref string) ([]byte, error) {
	doc := map[string]any{}
	if len(cfg) > 0 {
		if err := json.Unmarshal(cfg, &doc); err != nil {
			return nil, domain.Invalid("adapter_config", "JSON inválido")
		}
	}
	env, _ := doc["env"].(map[string]any)
	if env == nil {
		env = map[string]any{}
	}
	if ref == "" {
		delete(env, name)
	} else {
		env[name] = ref
	}
	if len(env) == 0 {
		delete(doc, "env")
	} else {
		doc["env"] = env
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("marshal adapter_config: %w", err)
	}
	return out, nil
}
