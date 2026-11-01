package service

import (
	"context"
	"fmt"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
)

// Leitura da aprovação externa (ADR-0081 itens 3 a 5). Qualquer membro vê título, estado, decisão, host, agente, sha256 e versões;
// só owner e admin leem o corpo do script, e cada abertura gera `activity_event`. A saída do host nunca entra no Hanko.

// ApprovalView é um pedido do canal.
type ApprovalView struct {
	ID                                           domain.ID
	CanalID, Title, RequestedAs, HostName, State string
	HostID, AgentID                              *domain.ID
	Decision, Approver, DecidedSHA256            *string
	ApprovedBy, ScriptSHA256                     string
	RC, DurationS, OutputBytes                   *int
	Versions                                     int
	RequestedAt, DecidedAt, ListedAt             *time.Time
}

// ApprovalVersionView é uma versão do script que chegou.
type ApprovalVersionView struct {
	SHA256 string
	SeenAt time.Time
}

// ApprovalScriptView é o corpo do script da versão que a página mostra.
type ApprovalScriptView struct {
	ApprovalID domain.ID
	SHA256     string
	Script     string
	Versions   []ApprovalVersionView
}

const approvalCols = `a.id, a.canal_id, a.title, a.requested_as, a.host_name, a.state, a.host_id, a.agent_id, a.decision, a.approver, a.decided_sha256,
	a.approved_by, a.script_sha256, a.rc, a.duration_s, a.output_bytes, (SELECT count(*) FROM external_approval_version v WHERE v.approval_id = a.id)::int,
	a.requested_at, a.decided_at, a.listed_at`

func scanApproval(r rowScanner) (ApprovalView, error) {
	var v ApprovalView
	var req, dec, listed *int64
	if err := r.Scan(&v.ID, &v.CanalID, &v.Title, &v.RequestedAs, &v.HostName, &v.State, &v.HostID, &v.AgentID, &v.Decision, &v.Approver, &v.DecidedSHA256,
		&v.ApprovedBy, &v.ScriptSHA256, &v.RC, &v.DurationS, &v.OutputBytes, &v.Versions, &req, &dec, &listed); err != nil {
		return v, err
	}
	v.RequestedAt, v.DecidedAt, v.ListedAt = nsTime(req), nsTime(dec), nsTime(listed)
	return v, nil
}

// ListExternalApprovals lista os pedidos, o mais recente primeiro, com filtro por estado. O pendente que a lista mais nova da outbox
// do host (tirada mais de 60 s depois dele) não tem sai da lista, sem ser apagado (ADR-0081 item 5; #815).
func (s *Service) ListExternalApprovals(ctx context.Context, companyID domain.ID, state string, limit, offset int) ([]ApprovalView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+approvalCols+` FROM external_approval a LEFT JOIN external_outbox_snapshot o ON o.host_id = a.host_id
		WHERE a.company_id = $1 AND ($2 = '' OR a.state = $2)
		  AND NOT (a.state = 'pending' AND o.host_id IS NOT NULL AND a.requested_at IS NOT NULL AND a.requested_at < o.snapshot_at - 60000000000 AND NOT (a.canal_id = ANY(o.canal_ids)))
		ORDER BY COALESCE(a.requested_at, a.decided_at, 0) DESC, a.id LIMIT $3 OFFSET $4`, companyID, state, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("external approvals: %w", err)
	}
	defer rows.Close()
	out := []ApprovalView{}
	for rows.Next() {
		v, err := scanApproval(rows)
		if err != nil {
			return nil, fmt.Errorf("external approvals: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// GetExternalApproval mostra um pedido pelo id, mesmo o que saiu das listas (qualquer membro).
func (s *Service) GetExternalApproval(ctx context.Context, companyID, id domain.ID) (ApprovalView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return ApprovalView{}, err
	}
	v, err := scanApproval(s.Pool.QueryRow(ctx, `SELECT `+approvalCols+` FROM external_approval a WHERE a.company_id = $1 AND a.id = $2`, companyID, id))
	if err != nil {
		return v, mapErr(err, "external approval")
	}
	return v, nil
}

// ApprovalScript devolve o corpo do script da versão vigente (a do `proposed` mais antigo), lido do fato da ingestão (owner ou admin).
// Cada abertura gera `activity_event` (ADR-0081 item 3).
func (s *Service) ApprovalScript(ctx context.Context, actor domain.Actor, companyID, id domain.ID) (ApprovalScriptView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return ApprovalScriptView{}, err
	}
	var out ApprovalScriptView
	err := s.withTx(ctx, func(t *txc) error {
		var dedupe string
		var at *int64
		if err := t.tx.QueryRow(t.ctx, `SELECT script_sha256, fact_dedupe_key, fact_time_unix_nano FROM external_approval WHERE company_id = $1 AND id = $2`,
			companyID, id).Scan(&out.SHA256, &dedupe, &at); err != nil {
			return mapErr(err, "external approval")
		}
		if at == nil || dedupe == "" {
			return domain.ErrNotFound // o pedido só tem a decisão: o script nunca chegou
		}
		var body *string
		if err := t.tx.QueryRow(t.ctx, `SELECT body FROM telemetry_logs WHERE company_id = $1 AND dedupe_key = $2 AND time_unix_nano = $3`, companyID, dedupe, *at).Scan(&body); err != nil {
			return mapErr(err, "approval fact")
		}
		out.ApprovalID, out.Script = id, textOf(body)
		rows, err := t.tx.Query(t.ctx, `SELECT sha256, seen_at FROM external_approval_version WHERE approval_id = $1 ORDER BY fact_time_unix_nano, sha256`, id)
		if err != nil {
			return fmt.Errorf("approval versions: %w", err)
		}
		defer rows.Close()
		out.Versions = []ApprovalVersionView{}
		for rows.Next() {
			var v ApprovalVersionView
			var seen int64
			if err := rows.Scan(&v.SHA256, &seen); err != nil {
				return fmt.Errorf("approval versions: %w", err)
			}
			v.SeenAt = time.Unix(0, seen).UTC()
			out.Versions = append(out.Versions, v)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		return t.activity(actor, companyID, "external_approval.script_read", "external_approval", id, map[string]any{"sha256": out.SHA256}, "")
	})
	return out, err
}
