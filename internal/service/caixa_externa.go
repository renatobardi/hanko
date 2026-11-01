package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Caixa: agentes e hosts externos novos (ADR-0075 item 2; ADR-0077). Informativos: ninguém precisa agir neles, então
// ficam fora de Count e do selo. Lido e arquivado seguem o de todo item da Caixa. "Novo" é o criado nos últimos
// externalNewWindow; a janela é uma escolha do autor, a registrar.

const (
	externalNewWindow = 30 * 24 * time.Hour
	maxExternalNew    = 50
)

// ExternalNew é um agente ou host externo novo que aparece na Caixa.
type ExternalNew struct {
	ID   domain.ID
	Name string
	At   time.Time
}

// addExternalNew preenche os agentes e hosts novos da empresa no Inbox.
func (s *Service) addExternalNew(ctx context.Context, companyID domain.ID, in *Inbox) error {
	since := s.Now().UTC().Add(-externalNewWindow)
	q := s.read()
	agents, err := q.ListNewExternalAgents(ctx, store.ListNewExternalAgentsParams{CompanyID: companyID, Since: since, Lim: maxExternalNew})
	if err != nil {
		return fmt.Errorf("list new external agents: %w", err)
	}
	for _, a := range agents {
		in.NewAgents = append(in.NewAgents, ExternalNew{ID: a.ID, Name: a.Name, At: a.CreatedAt})
	}
	hosts, err := q.ListNewExternalHosts(ctx, store.ListNewExternalHostsParams{CompanyID: companyID, Since: since, Lim: maxExternalNew})
	if err != nil {
		return fmt.Errorf("list new external hosts: %w", err)
	}
	for _, h := range hosts {
		in.NewHosts = append(in.NewHosts, ExternalNew{ID: h.ID, Name: h.Name, At: h.CreatedAt})
	}
	return nil
}

// InboxApproval é um pedido do canal pendente na Caixa (ADR-0081 item 6). O comando para copiar é `oute approve <canal_id>`.
type InboxApproval struct {
	ID      domain.ID
	CanalID string
	Title   string
	At      time.Time
}

// InboxRoundQuestion é uma pergunta da rodada ao Bardi sem resposta (ADR-0080 item 6).
type InboxRoundQuestion struct {
	ID   domain.ID
	Name string
	At   time.Time
}

// addExternalWork preenche as aprovações externas pendentes (só owner e admin) e as perguntas da rodada sem resposta.
func (s *Service) addExternalWork(ctx context.Context, companyID domain.ID, in *Inbox) error {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err == nil {
		rows, err := s.Pool.Query(ctx, `SELECT a.id, a.canal_id, a.title, a.requested_at FROM external_approval a LEFT JOIN external_outbox_snapshot o ON o.host_id = a.host_id
			WHERE a.company_id = $1 AND a.state = 'pending'
			  AND NOT (o.host_id IS NOT NULL AND a.requested_at IS NOT NULL AND a.requested_at < o.snapshot_at - 60000000000 AND NOT (a.canal_id = ANY(o.canal_ids)))
			ORDER BY COALESCE(a.requested_at, 0) DESC, a.id LIMIT $2`, companyID, maxExternalNew)
		if err != nil {
			return fmt.Errorf("inbox approvals: %w", err)
		}
		for rows.Next() {
			var a InboxApproval
			var at *int64
			if err := rows.Scan(&a.ID, &a.CanalID, &a.Title, &at); err != nil {
				rows.Close()
				return fmt.Errorf("inbox approvals: %w", err)
			}
			if t := nsTime(at); t != nil {
				a.At = *t
			}
			in.ExternalApprovals = append(in.ExternalApprovals, a)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	} else if !errors.Is(err, domain.ErrForbidden) {
		return err
	}
	return s.addRoundQuestions(ctx, companyID, in)
}

// addRoundQuestions: a rodada sem fechamento cuja última pergunta é igual ou mais nova que a última resposta, até o dobro de
// `limit.lookback_hours` (24 h por padrão) depois da pergunta (ADR-0080 item 6; `decisions.py@845764f`).
func (s *Service) addRoundQuestions(ctx context.Context, companyID domain.ID, in *Inbox) error {
	rows, err := s.Pool.Query(ctx, `SELECT r.id, r.name, r.last_asked_at FROM external_round r
		WHERE r.company_id = $1 AND r.closed_at IS NULL AND r.last_asked_at IS NOT NULL
		  AND (r.last_answered_at IS NULL OR r.last_asked_at >= r.last_answered_at)
		  AND r.last_asked_at >= $2 - 2 * (COALESCE((SELECT value FROM company_alert_settings WHERE company_id = $1 AND key = 'limit.lookback_hours'), 24)::float8 * 3600000000000)::bigint
		ORDER BY r.last_asked_at DESC, r.id LIMIT $3`, companyID, s.Now().UnixNano(), maxExternalNew)
	if err != nil {
		return fmt.Errorf("inbox round questions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var q InboxRoundQuestion
		var at int64
		if err := rows.Scan(&q.ID, &q.Name, &at); err != nil {
			return fmt.Errorf("inbox round questions: %w", err)
		}
		q.At = time.Unix(0, at).UTC()
		in.RoundQuestions = append(in.RoundQuestions, q)
	}
	return rows.Err()
}
