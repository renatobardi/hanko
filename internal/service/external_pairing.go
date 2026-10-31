package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/renatobardi/hanko/internal/domain"
)

// Emparelhamento do repositório com o workspace (ADR-0075 itens 3 a 5; fatia 1 da v0.8, #125). Corrigir o emparelhamento
// leva o passado junto: as conversas, sessões, rodadas e chamadas do repositório passam a contar no workspace novo, e o
// `calls_daily` troca de linha pelas mesmas chamadas (decisão Q42 do Bardi: mexe só nas derivadas, nunca no bruto).

// RepoPairingView é um emparelhamento com o slug do workspace.
type RepoPairingView struct {
	ID            domain.ID
	RepoKey       string
	KeyKind       string
	WorkspaceID   domain.ID
	WorkspaceSlug string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

const pairingSelect = `SELECT p.id, p.repo_key, p.key_kind, p.workspace_id, w.slug, p.created_at, p.updated_at
FROM repo_pairing p JOIN workspaces w ON w.id = p.workspace_id`

func scanPairing(row pgx.Row) (RepoPairingView, error) {
	var v RepoPairingView
	err := row.Scan(&v.ID, &v.RepoKey, &v.KeyKind, &v.WorkspaceID, &v.WorkspaceSlug, &v.CreatedAt, &v.UpdatedAt)
	return v, err
}

// ListRepoPairings lista os emparelhamentos da empresa, do mais novo para o mais antigo.
func (s *Service) ListRepoPairings(ctx context.Context, companyID domain.ID, limit, offset int) ([]RepoPairingView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, pairingSelect+` WHERE p.company_id = $1 ORDER BY p.created_at DESC, p.id LIMIT $2 OFFSET $3`, companyID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list repo pairings: %w", err)
	}
	defer rows.Close()
	out := []RepoPairingView{}
	for rows.Next() {
		v, err := scanPairing(rows)
		if err != nil {
			return nil, fmt.Errorf("list repo pairings: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// UpdateRepoPairing troca o workspace de um emparelhamento (owner ou admin). O workspace de sistema `no-repo` não serve:
// ele é de quem não tem repositório. Trocar para o mesmo workspace não faz nada.
func (s *Service) UpdateRepoPairing(ctx context.Context, actor domain.Actor, companyID, id, workspaceID domain.ID) (RepoPairingView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return RepoPairingView{}, err
	}
	err := s.withTx(ctx, func(t *txc) error {
		var key string
		var old domain.ID
		if err := t.tx.QueryRow(ctx, `SELECT repo_key, workspace_id FROM repo_pairing WHERE company_id = $1 AND id = $2 FOR UPDATE`,
			companyID, id).Scan(&key, &old); err != nil {
			return mapErr(err, "repo pairing")
		}
		if old == workspaceID {
			return nil
		}
		var system bool
		if err := t.tx.QueryRow(ctx, `SELECT is_system FROM workspaces WHERE company_id = $1 AND id = $2`, companyID, workspaceID).Scan(&system); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.Invalid("workspace_id", "workspace não encontrado")
			}
			return fmt.Errorf("check workspace: %w", err)
		}
		if system {
			return domain.Invalid("workspace_id", "o workspace de sistema no-repo não recebe emparelhamento")
		}
		if err := movePairingPast(t, companyID, key, old, workspaceID); err != nil {
			return err
		}
		if _, err := t.tx.Exec(ctx, `UPDATE repo_pairing SET workspace_id = $3, updated_at = $4 WHERE company_id = $1 AND id = $2`,
			companyID, id, workspaceID, t.now); err != nil {
			return mapErr(err, "repo pairing")
		}
		return t.activity(actor, companyID, "repo_pairing.updated", "repo_pairing", id,
			map[string]any{"repo_key": key, "from": old, "to": workspaceID}, "")
	})
	if err != nil {
		return RepoPairingView{}, err
	}
	v, err := scanPairing(s.Pool.QueryRow(ctx, pairingSelect+` WHERE p.company_id = $1 AND p.id = $2`, companyID, id))
	return v, mapErr(err, "repo pairing")
}

// Passado de um repositório: as chamadas são as das conversas dele (a chamada sem conversa não tem repositório guardado e
// fica onde está). O `calls_daily` é refeito pelas mesmas chamadas: sai do workspace velho e entra no novo, e a linha que
// zerou some.
const movedCallsCTE = `WITH moved AS (
  SELECT (to_timestamp(m.time_unix_nano / 1000000000.0) AT TIME ZONE 'UTC')::date AS day, m.host_id, m.agent_id, m.model, m.subscription_key,
    count(*)::bigint AS calls, count(*) FILTER (WHERE m.is_error)::bigint AS errors, sum(m.input_tokens)::bigint AS input_tokens,
    sum(m.output_tokens)::bigint AS output_tokens, sum(m.cache_read_tokens)::bigint AS cache_read_tokens,
    sum(m.cache_creation_tokens)::bigint AS cache_creation_tokens, COALESCE(sum(m.cost_real_usd), 0) AS cost_real_usd,
    COALESCE(sum(m.cost_list_usd), 0) AS cost_list_usd, count(*) FILTER (WHERE m.cost_class = 'unpriced')::bigint AS unpriced_calls
  FROM model_calls m JOIN external_conversation c ON c.id = m.conversation_id
  WHERE m.company_id = $1 AND c.oute_repo = $2 AND m.workspace_id = $3
  GROUP BY 1, 2, 3, 4, 5)`

func movePairingPast(t *txc, companyID domain.ID, repoKey string, from, to domain.ID) error {
	steps := []struct {
		what, sql string
		args      []any
	}{
		{"calls_daily (sai)", movedCallsCTE + ` UPDATE calls_daily d SET calls = d.calls - mv.calls, errors = d.errors - mv.errors,
			input_tokens = d.input_tokens - mv.input_tokens, output_tokens = d.output_tokens - mv.output_tokens,
			cache_read_tokens = d.cache_read_tokens - mv.cache_read_tokens, cache_creation_tokens = d.cache_creation_tokens - mv.cache_creation_tokens,
			cost_real_usd = d.cost_real_usd - mv.cost_real_usd, cost_list_usd = d.cost_list_usd - mv.cost_list_usd,
			unpriced_calls = d.unpriced_calls - mv.unpriced_calls
			FROM moved mv WHERE d.company_id = $1 AND d.workspace_id = $3 AND d.day = mv.day AND d.host_id = mv.host_id
			AND d.agent_id = mv.agent_id AND d.model = mv.model AND d.subscription_key = mv.subscription_key`, []any{companyID, repoKey, from}},
		{"calls_daily (entra)", movedCallsCTE + ` INSERT INTO calls_daily (company_id, day, host_id, agent_id, workspace_id, model, subscription_key,
			calls, errors, input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, cost_real_usd, cost_list_usd, unpriced_calls)
			SELECT $1, day, host_id, agent_id, $4::uuid, model, subscription_key, calls, errors, input_tokens, output_tokens, cache_read_tokens,
			cache_creation_tokens, cost_real_usd, cost_list_usd, unpriced_calls FROM moved
			ON CONFLICT (company_id, day, host_id, agent_id, workspace_id, model, subscription_key) DO UPDATE SET
			calls = calls_daily.calls + EXCLUDED.calls, errors = calls_daily.errors + EXCLUDED.errors,
			input_tokens = calls_daily.input_tokens + EXCLUDED.input_tokens, output_tokens = calls_daily.output_tokens + EXCLUDED.output_tokens,
			cache_read_tokens = calls_daily.cache_read_tokens + EXCLUDED.cache_read_tokens,
			cache_creation_tokens = calls_daily.cache_creation_tokens + EXCLUDED.cache_creation_tokens,
			cost_real_usd = calls_daily.cost_real_usd + EXCLUDED.cost_real_usd, cost_list_usd = calls_daily.cost_list_usd + EXCLUDED.cost_list_usd,
			unpriced_calls = calls_daily.unpriced_calls + EXCLUDED.unpriced_calls`, []any{companyID, repoKey, from, to}},
		{"calls_daily (zeradas)", `DELETE FROM calls_daily WHERE company_id = $1 AND workspace_id = $2 AND calls = 0`, []any{companyID, from}},
		{"model_calls", `UPDATE model_calls m SET workspace_id = $4 FROM external_conversation c WHERE c.id = m.conversation_id
			AND m.company_id = $1 AND c.oute_repo = $2 AND m.workspace_id = $3`, []any{companyID, repoKey, from, to}},
		{"conversas", `UPDATE external_conversation SET workspace_id = $3, updated_at = $4 WHERE company_id = $1 AND oute_repo = $2`, []any{companyID, repoKey, to, t.now}},
		{"sessões", `UPDATE external_session SET workspace_id = $3, updated_at = $4 WHERE company_id = $1 AND oute_repo = $2`, []any{companyID, repoKey, to, t.now}},
		{"rodadas", `UPDATE external_round SET workspace_id = $3, updated_at = $4 WHERE company_id = $1 AND repo = $2`, []any{companyID, repoKey, to, t.now}},
	}
	for _, st := range steps {
		if _, err := t.tx.Exec(t.ctx, st.sql, st.args...); err != nil {
			return fmt.Errorf("repo pairing: mover %s: %w", st.what, err)
		}
	}
	return nil
}
