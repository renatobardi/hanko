package service

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/export"
)

// Configuração externa no export (ADR-0088 item 1): agentes e hosts externos (com apelidos, sem chave), emparelhamentos, chave
// externa e histórico de mensalidade das assinaturas e os limites de alerta da empresa. Tudo ordenado, para o round trip
// export → import → export dar o mesmo arquivo. Conversas, sessões, rodadas, aprovações, chamadas e alertas não entram.

// exportExternal preenche o documento com a configuração externa. `wsSlug` e `subName` ligam por referência, sem id.
func exportExternal(ctx context.Context, tx pgx.Tx, companyID domain.ID, doc *export.Document, wsSlug, subName map[domain.ID]string) error {
	var err error
	if doc.ExternalAgents, err = exportExternalAgents(ctx, tx, companyID); err != nil {
		return err
	}
	if doc.ExternalHosts, err = exportExternalHosts(ctx, tx, companyID); err != nil {
		return err
	}
	if doc.RepoPairings, err = exportRepoPairings(ctx, tx, companyID, wsSlug); err != nil {
		return err
	}
	if doc.AlertSettings, err = exportAlertSettings(ctx, tx, companyID); err != nil {
		return err
	}
	return exportSubscriptionExternal(ctx, tx, companyID, doc, subName)
}

func aliasMap(ctx context.Context, tx pgx.Tx, companyID domain.ID, table, col string) (map[domain.ID][]string, error) {
	rows, err := tx.Query(ctx, `SELECT `+col+`, alias FROM `+table+` WHERE company_id = $1 ORDER BY alias`, companyID)
	if err != nil {
		return nil, fmt.Errorf("export %s: %w", table, err)
	}
	defer rows.Close()
	out := map[domain.ID][]string{}
	for rows.Next() {
		var id domain.ID
		var alias string
		if err := rows.Scan(&id, &alias); err != nil {
			return nil, fmt.Errorf("export %s: %w", table, err)
		}
		out[id] = append(out[id], alias)
	}
	return out, rows.Err()
}

func exportExternalAgents(ctx context.Context, tx pgx.Tx, companyID domain.ID) ([]export.ExternalAgent, error) {
	aliases, err := aliasMap(ctx, tx, companyID, "external_agent_alias", "agent_id")
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT id, name, archived_at FROM external_agent WHERE company_id = $1 ORDER BY lower(name), name`, companyID)
	if err != nil {
		return nil, fmt.Errorf("export external agents: %w", err)
	}
	defer rows.Close()
	var out []export.ExternalAgent
	for rows.Next() {
		var id domain.ID
		var a export.ExternalAgent
		if err := rows.Scan(&id, &a.Name, &a.Archived); err != nil {
			return nil, fmt.Errorf("export external agents: %w", err)
		}
		a.Aliases = aliases[id]
		out = append(out, a)
	}
	return out, rows.Err()
}

func exportExternalHosts(ctx context.Context, tx pgx.Tx, companyID domain.ID) ([]export.ExternalHost, error) {
	aliases, err := aliasMap(ctx, tx, companyID, "external_host_alias", "host_id")
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT id, name, always_on, archived_at FROM external_host WHERE company_id = $1 ORDER BY lower(name), name`, companyID)
	if err != nil {
		return nil, fmt.Errorf("export external hosts: %w", err)
	}
	defer rows.Close()
	var out []export.ExternalHost
	for rows.Next() {
		var id domain.ID
		var h export.ExternalHost
		if err := rows.Scan(&id, &h.Name, &h.AlwaysOn, &h.Archived); err != nil {
			return nil, fmt.Errorf("export external hosts: %w", err)
		}
		h.Aliases = aliases[id]
		out = append(out, h)
	}
	return out, rows.Err()
}

func exportRepoPairings(ctx context.Context, tx pgx.Tx, companyID domain.ID, wsSlug map[domain.ID]string) ([]export.RepoPairing, error) {
	rows, err := tx.Query(ctx, `SELECT repo_key, key_kind, workspace_id FROM repo_pairing WHERE company_id = $1 ORDER BY repo_key`, companyID)
	if err != nil {
		return nil, fmt.Errorf("export repo pairings: %w", err)
	}
	defer rows.Close()
	var out []export.RepoPairing
	for rows.Next() {
		var repo, kind string
		var ws domain.ID
		if err := rows.Scan(&repo, &kind, &ws); err != nil {
			return nil, fmt.Errorf("export repo pairings: %w", err)
		}
		if kind == "name" {
			kind = "" // o padrão não viaja
		}
		out = append(out, export.RepoPairing{Repo: repo, Kind: kind, Workspace: wsSlug[ws]})
	}
	return out, rows.Err()
}

func exportAlertSettings(ctx context.Context, tx pgx.Tx, companyID domain.ID) ([]export.AlertSetting, error) {
	rows, err := tx.Query(ctx, `SELECT key, value::float8 FROM company_alert_settings WHERE company_id = $1 ORDER BY key`, companyID)
	if err != nil {
		return nil, fmt.Errorf("export alert settings: %w", err)
	}
	defer rows.Close()
	var out []export.AlertSetting
	for rows.Next() {
		var a export.AlertSetting
		if err := rows.Scan(&a.Key, &a.Value); err != nil {
			return nil, fmt.Errorf("export alert settings: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// exportSubscriptionExternal põe a chave externa e o histórico de mensalidade em cada assinatura do documento, pelo nome.
func exportSubscriptionExternal(ctx context.Context, tx pgx.Tx, companyID domain.ID, doc *export.Document, subName map[domain.ID]string) error {
	idx := map[string]int{}
	for i, s := range doc.Subscriptions {
		idx[s.Name] = i
	}
	rows, err := tx.Query(ctx, `SELECT s.id, COALESCE(s.external_key, '') FROM subscriptions s WHERE s.company_id = $1`, companyID)
	if err != nil {
		return fmt.Errorf("export subscription keys: %w", err)
	}
	for rows.Next() {
		var id domain.ID
		var key string
		if err := rows.Scan(&id, &key); err != nil {
			rows.Close()
			return fmt.Errorf("export subscription keys: %w", err)
		}
		if i, ok := idx[subName[id]]; ok {
			doc.Subscriptions[i].ExternalKey = key
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	h, err := tx.Query(ctx, `SELECT subscription_id, monthly_usd::float8, to_char(starts_on, 'YYYY-MM-DD'), recorded_at, origin FROM subscription_monthly_history
		WHERE company_id = $1 ORDER BY starts_on, recorded_at, id`, companyID)
	if err != nil {
		return fmt.Errorf("export monthly history: %w", err)
	}
	defer h.Close()
	for h.Next() {
		var id domain.ID
		var v export.MonthlyValue
		if err := h.Scan(&id, &v.MonthlyUSD, &v.StartsOn, &v.RecordedAt, &v.Origin); err != nil {
			return fmt.Errorf("export monthly history: %w", err)
		}
		if i, ok := idx[subName[id]]; ok {
			doc.Subscriptions[i].MonthlyHistory = append(doc.Subscriptions[i].MonthlyHistory, v)
		}
	}
	return h.Err()
}
