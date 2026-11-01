package service

import (
	"fmt"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/export"
)

// Configuração externa na importação (ADR-0088 itens 1, 3 e 5): validada por inteiro antes de gravar e gravada na mesma
// transação da empresa nova. Os ids são novos. O host importado vem sem chave. Numa empresa existente (merge) a configuração
// externa não entra: o merge só leva o que a empresa de destino pode receber sem conflito (aviso `external_not_merged`).

const (
	maxExternalName  = 100
	maxExternalAlias = 200
	maxPairingRepo   = 500
)

// validateExternal confere a configuração externa do arquivo sem tocar no banco.
func validateExternal(doc export.Document) error {
	if err := validateExternalNames("external_agents", len(doc.ExternalAgents), func(i int) (string, []string) {
		return doc.ExternalAgents[i].Name, doc.ExternalAgents[i].Aliases
	}); err != nil {
		return err
	}
	if err := validateExternalNames("external_hosts", len(doc.ExternalHosts), func(i int) (string, []string) {
		return doc.ExternalHosts[i].Name, doc.ExternalHosts[i].Aliases
	}); err != nil {
		return err
	}
	if err := validatePairings(doc); err != nil {
		return err
	}
	seen := map[string]bool{}
	for i, a := range doc.AlertSettings {
		if err := domain.ValidateAlertSetting(a.Key, a.Value); err != nil {
			return domain.Invalid(iv("alert_settings", i, "key"), err.Error())
		}
		if seen[a.Key] {
			return domain.Invalid(iv("alert_settings", i, "key"), fmt.Sprintf("duplicate %q", a.Key))
		}
		seen[a.Key] = true
	}
	return validateSubscriptionsExternal(doc)
}

// validateExternalNames confere nome (único sem distinguir caixa) e apelidos (únicos no tipo) de agentes ou hosts.
func validateExternalNames(path string, n int, at func(int) (string, []string)) error {
	names, aliases := map[string]bool{}, map[string]bool{}
	for i := 0; i < n; i++ {
		name, al := at(i)
		trimmed := strings.TrimSpace(name)
		if trimmed == "" || len([]rune(trimmed)) > maxExternalName {
			return domain.Invalid(iv(path, i, "name"), "required, up to 100 characters")
		}
		k := strings.ToLower(trimmed)
		if names[k] {
			return domain.Invalid(iv(path, i, "name"), fmt.Sprintf("duplicate %q", trimmed))
		}
		names[k] = true
		for _, a := range al {
			if a == "" || len([]rune(a)) > maxExternalAlias {
				return domain.Invalid(iv(path, i, "aliases"), "alias is required, up to 200 characters")
			}
			if aliases[a] {
				return domain.Invalid(iv(path, i, "aliases"), fmt.Sprintf("duplicate alias %q", a))
			}
			aliases[a] = true
		}
	}
	return nil
}

func validatePairings(doc export.Document) error {
	ws := map[string]bool{}
	for _, w := range doc.Workspaces {
		ws[w.Slug] = true
	}
	seen := map[string]bool{}
	for i, p := range doc.RepoPairings {
		if p.Repo == "" || len([]rune(p.Repo)) > maxPairingRepo {
			return domain.Invalid(iv("repo_pairings", i, "repo"), "required, up to 500 characters")
		}
		if seen[p.Repo] {
			return domain.Invalid(iv("repo_pairings", i, "repo"), fmt.Sprintf("duplicate %q", p.Repo))
		}
		seen[p.Repo] = true
		if p.Kind != "" && p.Kind != "url" && p.Kind != "name" {
			return domain.Invalid(iv("repo_pairings", i, "kind"), "want url or name")
		}
		if !ws[p.Workspace] {
			return domain.Invalid(iv("repo_pairings", i, "workspace"), fmt.Sprintf("unknown workspace %q", p.Workspace))
		}
	}
	return nil
}

func validateSubscriptionsExternal(doc export.Document) error {
	keys := map[string]bool{}
	for i, s := range doc.Subscriptions {
		if s.ExternalKey != "" {
			if err := domain.ValidateSubscriptionExternalKey(s.ExternalKey); err != nil {
				return domain.Invalid(iv("subscriptions", i, "external_key"), err.Error())
			}
			if keys[s.ExternalKey] {
				return domain.Invalid(iv("subscriptions", i, "external_key"), fmt.Sprintf("duplicate %q", s.ExternalKey))
			}
			keys[s.ExternalKey] = true
		}
		for j, h := range s.MonthlyHistory {
			if h.MonthlyUSD < 0 {
				return domain.Invalid(fmt.Sprintf("subscriptions[%d].monthly_history[%d].monthly_usd", i, j), "must be >= 0")
			}
			if _, err := time.Parse("2006-01-02", h.StartsOn); err != nil {
				return domain.Invalid(fmt.Sprintf("subscriptions[%d].monthly_history[%d].starts_on", i, j), "want YYYY-MM-DD")
			}
			if h.Origin != "" && h.Origin != "created" && h.Origin != "manual" && h.Origin != "migrated" {
				return domain.Invalid(fmt.Sprintf("subscriptions[%d].monthly_history[%d].origin", i, j), "want created, manual or migrated")
			}
		}
	}
	return nil
}

// importSubscriptionExternal grava a chave externa e o histórico de mensalidade da assinatura. Sem histórico no arquivo (arquivo
// antigo), nasce a linha de nascimento com o valor do arquivo, como toda assinatura tem (ADR-0082 item 6).
func (t *txc) importSubscriptionExternal(cid, subID domain.ID, x export.Subscription) error {
	if x.ExternalKey != "" {
		if _, err := t.tx.Exec(t.ctx, `UPDATE subscriptions SET external_key = $2 WHERE id = $1`, subID, x.ExternalKey); err != nil {
			return mapErr(err, "import subscription external key")
		}
	}
	history := x.MonthlyHistory
	if len(history) == 0 {
		history = []export.MonthlyValue{{MonthlyUSD: x.MonthlyUSD, StartsOn: dayOf(x.CreatedAt).Format("2006-01-02"), RecordedAt: x.CreatedAt, Origin: "created"}}
	}
	for _, h := range history {
		origin := h.Origin
		if origin == "" {
			origin = "manual"
		}
		if _, err := t.tx.Exec(t.ctx, `INSERT INTO subscription_monthly_history (company_id, subscription_id, monthly_usd, starts_on, recorded_at, origin, actor_type)
			VALUES ($1, $2, $3, $4::date, $5, $6, 'system')`, cid, subID, h.MonthlyUSD, h.StartsOn, h.RecordedAt, origin); err != nil {
			return fmt.Errorf("import monthly history: %w", err)
		}
	}
	return nil
}

// importExternal grava agentes, hosts, emparelhamentos e limites de alerta da empresa nova.
func (t *txc) importExternal(cid domain.ID, doc export.Document, wsID map[string]domain.ID) error {
	for _, a := range doc.ExternalAgents {
		if err := t.importExternalEntity(cid, "external_agent", "external_agent_alias", "agent_id", a.Name, a.Aliases, nil, a.Archived); err != nil {
			return err
		}
	}
	for _, h := range doc.ExternalHosts {
		always := h.AlwaysOn
		if err := t.importExternalEntity(cid, "external_host", "external_host_alias", "host_id", h.Name, h.Aliases, &always, h.Archived); err != nil {
			return err
		}
	}
	for _, p := range doc.RepoPairings {
		kind := p.Kind
		if kind == "" {
			kind = "name"
		}
		if _, err := t.tx.Exec(t.ctx, `INSERT INTO repo_pairing (id, company_id, repo_key, key_kind, workspace_id, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, $6, $6)`,
			domain.NewID(), cid, p.Repo, kind, wsID[p.Workspace], t.now); err != nil {
			return mapErr(err, "import repo pairing")
		}
	}
	for _, a := range doc.AlertSettings {
		if _, err := t.tx.Exec(t.ctx, `INSERT INTO company_alert_settings (company_id, key, value, updated_by_type, updated_at) VALUES ($1, $2, $3, 'system', $4)`, cid, a.Key, a.Value, t.now); err != nil {
			return mapErr(err, "import alert setting")
		}
	}
	return nil
}

func (t *txc) importExternalEntity(cid domain.ID, table, aliasTable, aliasCol, name string, aliases []string, alwaysOn *bool, archived *time.Time) error {
	id := domain.NewID()
	name = strings.TrimSpace(name)
	var err error
	if alwaysOn != nil {
		_, err = t.tx.Exec(t.ctx, `INSERT INTO `+table+` (id, company_id, name, always_on, archived_at, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, $6, $6)`,
			id, cid, name, *alwaysOn, archived, t.now)
	} else {
		_, err = t.tx.Exec(t.ctx, `INSERT INTO `+table+` (id, company_id, name, archived_at, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, $5)`, id, cid, name, archived, t.now)
	}
	if err != nil {
		return mapErr(err, "import "+table)
	}
	for _, a := range aliases {
		if _, err := t.tx.Exec(t.ctx, `INSERT INTO `+aliasTable+` (company_id, alias, `+aliasCol+`, created_at) VALUES ($1, $2, $3, $4)`, cid, a, id, t.now); err != nil {
			return mapErr(err, "import "+aliasTable)
		}
	}
	return nil
}
