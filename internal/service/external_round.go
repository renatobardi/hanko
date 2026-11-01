package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
)

// Leitura da rodada externa e do ciclo, texto da etapa e marca da ação (ADR-0080 itens 3, 4 e 7). Leitura é de qualquer
// membro; o texto da etapa e a marca são de owner ou admin, a marca com reautenticação recente (ADR-0070). O texto é dado
// não confiável e sai do fato da ingestão, nunca de uma cópia no estado.

// RoundView é uma rodada externa.
type RoundView struct {
	ID                                                           domain.ID
	RoundKey, Name, Repo                                         string
	CycleID, WorkspaceID                                         *domain.ID
	MaxSessions                                                  *int
	OpenedAt, ClosedAt, LastEventAt, LastAskedAt, LastAnsweredAt *time.Time
	CreatedAt, UpdatedAt                                         time.Time
}

// ParticipantView é um worker da rodada.
type ParticipantView struct {
	ID                  domain.ID
	Slug, IssueRef      string
	ExternalSessionID   *domain.ID
	SpawnedAt, ClosedAt *time.Time
}

// ActionMarkView é a marca mais nova de uma ação.
type ActionMarkView struct {
	State        string
	MarkedByType string
	MarkedByID   *domain.ID
	MarkedAt     time.Time
	Migrated     bool
}

// StepActionView é uma ação da etapa vigente, com a marca mais nova.
type StepActionView struct {
	ActionID, ApprovalKey string
	Mark                  *ActionMarkView
}

// StepView é a revisão vigente de uma etapa. Ação só aparece na etapa aprovada.
type StepView struct {
	ID                                            domain.ID
	Kind, StepKey, Review, ReferenceCheck, SHA256 string
	Revision                                      int64
	PublishedAt                                   time.Time
	Actions                                       []StepActionView
}

// RoundDetailView é a rodada com participantes e etapas.
type RoundDetailView struct {
	RoundView
	Participants []ParticipantView
	Steps        []StepView
}

// CycleView é o ciclo de uma issue.
type CycleView struct {
	ID        domain.ID
	IssueRef  string
	Summary   *StepView
	Rounds    []RoundView
	CreatedAt time.Time
}

const roundCols = `id, round_key, name, repo, cycle_id, workspace_id, max_sessions, opened_at, closed_at, last_event_at, last_asked_at,
	last_answered_at, created_at, updated_at`

type rowScanner interface{ Scan(dest ...any) error }

func scanRound(r rowScanner) (RoundView, error) {
	var v RoundView
	var opened, closed, event, asked, answered *int64
	if err := r.Scan(&v.ID, &v.RoundKey, &v.Name, &v.Repo, &v.CycleID, &v.WorkspaceID, &v.MaxSessions, &opened, &closed, &event, &asked,
		&answered, &v.CreatedAt, &v.UpdatedAt); err != nil {
		return v, err
	}
	v.OpenedAt, v.ClosedAt, v.LastEventAt, v.LastAskedAt, v.LastAnsweredAt = nsTime(opened), nsTime(closed), nsTime(event), nsTime(asked), nsTime(answered)
	return v, nil
}

// ListExternalRounds lista as rodadas externas, a aberta mais recente primeiro, com filtro por ciclo (qualquer membro).
func (s *Service) ListExternalRounds(ctx context.Context, companyID domain.ID, cycle *domain.ID, limit, offset int) ([]RoundView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+roundCols+` FROM external_round WHERE company_id = $1 AND ($2::uuid IS NULL OR cycle_id = $2)
		ORDER BY COALESCE(opened_at, 0) DESC, created_at DESC, id LIMIT $3 OFFSET $4`, companyID, cycle, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("external rounds: %w", err)
	}
	defer rows.Close()
	out := []RoundView{}
	for rows.Next() {
		v, err := scanRound(rows)
		if err != nil {
			return nil, fmt.Errorf("external rounds: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// GetExternalRound mostra a rodada com participantes, etapas vigentes e as ações com a marca mais nova (qualquer membro).
func (s *Service) GetExternalRound(ctx context.Context, companyID, id domain.ID) (RoundDetailView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return RoundDetailView{}, err
	}
	var d RoundDetailView
	r, err := scanRound(s.Pool.QueryRow(ctx, `SELECT `+roundCols+` FROM external_round WHERE company_id = $1 AND id = $2`, companyID, id))
	if err != nil {
		return d, mapErr(err, "external round")
	}
	d.RoundView = r
	if d.Participants, err = s.roundParticipants(ctx, id); err != nil {
		return d, err
	}
	d.Steps, err = s.roundSteps(ctx, `s.round_id = $1`, id)
	return d, err
}

func (s *Service) roundParticipants(ctx context.Context, roundID domain.ID) ([]ParticipantView, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, slug, issue_ref, external_session_id, spawned_at, closed_at FROM round_participant WHERE round_id = $1
		ORDER BY COALESCE(spawned_at, 0), slug`, roundID)
	if err != nil {
		return nil, fmt.Errorf("round participants: %w", err)
	}
	defer rows.Close()
	out := []ParticipantView{}
	for rows.Next() {
		var p ParticipantView
		var sp, cl *int64
		if err := rows.Scan(&p.ID, &p.Slug, &p.IssueRef, &p.ExternalSessionID, &sp, &cl); err != nil {
			return nil, fmt.Errorf("round participants: %w", err)
		}
		p.SpawnedAt, p.ClosedAt = nsTime(sp), nsTime(cl)
		out = append(out, p)
	}
	return out, rows.Err()
}

// roundSteps lê as etapas de um escopo (rodada ou ciclo) na ordem da rodada, com as ações e a marca mais nova de cada uma.
func (s *Service) roundSteps(ctx context.Context, where string, scope domain.ID) ([]StepView, error) {
	rows, err := s.Pool.Query(ctx, `SELECT s.id, s.kind, s.step_key, s.revision, s.sha256, s.review, s.reference_check, s.published_at
		FROM round_step s WHERE `+where+` ORDER BY array_position(ARRAY['triage','merge','kaizen','closing','cycle_summary'], s.kind),
		NULLIF(s.step_key, '')::bigint NULLS FIRST, s.id`, scope)
	if err != nil {
		return nil, fmt.Errorf("round steps: %w", err)
	}
	var out []StepView
	for rows.Next() {
		var v StepView
		var pub int64
		if err := rows.Scan(&v.ID, &v.Kind, &v.StepKey, &v.Revision, &v.SHA256, &v.Review, &v.ReferenceCheck, &pub); err != nil {
			rows.Close()
			return nil, fmt.Errorf("round steps: %w", err)
		}
		v.PublishedAt = time.Unix(0, pub).UTC()
		v.Actions = []StepActionView{}
		out = append(out, v)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Review != "approved" {
			continue // a etapa reprovada ou sem revisor sai com o texto e as ações fechados
		}
		if out[i].Actions, err = s.stepActions(ctx, out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Service) stepActions(ctx context.Context, stepID domain.ID) ([]StepActionView, error) {
	rows, err := s.Pool.Query(ctx, `SELECT a.action_id, a.approval_key, m.state, m.marked_by_type, m.marked_by_id, m.marked_unix_nano, m.migrated
		FROM step_action a LEFT JOIN LATERAL (SELECT * FROM action_mark x WHERE x.step_id = a.step_id AND x.action_id = a.action_id
		  ORDER BY x.marked_unix_nano DESC, x.created_at DESC, x.id DESC LIMIT 1) m ON true
		WHERE a.step_id = $1 ORDER BY a.created_at, a.action_id`, stepID)
	if err != nil {
		return nil, fmt.Errorf("step actions: %w", err)
	}
	defer rows.Close()
	out := []StepActionView{}
	for rows.Next() {
		var a StepActionView
		var state, byType *string
		var byID *domain.ID
		var ns *int64
		var migrated *bool
		if err := rows.Scan(&a.ActionID, &a.ApprovalKey, &state, &byType, &byID, &ns, &migrated); err != nil {
			return nil, fmt.Errorf("step actions: %w", err)
		}
		if state != nil && byType != nil && ns != nil {
			a.Mark = &ActionMarkView{State: *state, MarkedByType: *byType, MarkedByID: byID, MarkedAt: time.Unix(0, *ns).UTC(), Migrated: migrated != nil && *migrated}
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetExternalCycle mostra o ciclo de uma issue: o resumo no topo e as rodadas (qualquer membro).
func (s *Service) GetExternalCycle(ctx context.Context, companyID, id domain.ID) (CycleView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return CycleView{}, err
	}
	var v CycleView
	if err := s.Pool.QueryRow(ctx, `SELECT id, issue_ref, created_at FROM round_cycle WHERE company_id = $1 AND id = $2`, companyID, id).
		Scan(&v.ID, &v.IssueRef, &v.CreatedAt); err != nil {
		return v, mapErr(err, "external cycle")
	}
	steps, err := s.roundSteps(ctx, `s.cycle_id = $1 AND s.kind = 'cycle_summary'`, id)
	if err != nil {
		return v, err
	}
	if len(steps) > 0 {
		v.Summary = &steps[0]
	}
	if v.Rounds, err = s.ListExternalRounds(ctx, companyID, &id, 1000, 0); err != nil {
		return v, err
	}
	return v, nil
}

// RoundStepContent devolve o texto da revisão vigente de uma etapa, lido do fato da ingestão (owner ou admin). Etapa reprovada
// ou sem revisor não entrega o texto. Cada leitura gera `activity_event` (ADR-0080 item 3).
func (s *Service) RoundStepContent(ctx context.Context, actor domain.Actor, companyID, roundID, stepID domain.ID) (string, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return "", err
	}
	var text string
	err := s.withTx(ctx, func(t *txc) error {
		var review, dedupe string
		var at int64
		err := t.tx.QueryRow(t.ctx, `SELECT review, fact_dedupe_key, fact_time_unix_nano FROM round_step WHERE company_id = $1 AND round_id = $2 AND id = $3`,
			companyID, roundID, stepID).Scan(&review, &dedupe, &at)
		if err != nil {
			return mapErr(err, "round step")
		}
		if review != "approved" {
			return domain.ErrNotAllowed // o texto da etapa reprovada ou sem revisor fica fechado (ADR-0080 item 3)
		}
		var body *string
		err = t.tx.QueryRow(t.ctx, `SELECT body FROM telemetry_logs WHERE company_id = $1 AND dedupe_key = $2 AND time_unix_nano = $3`, companyID, dedupe, at).Scan(&body)
		if err != nil {
			return mapErr(err, "step fact")
		}
		text = textOf(body)
		return t.activity(actor, companyID, "round_step.content_read", "round_step", stepID, map[string]any{"round_id": roundID.String()}, "")
	})
	return text, err
}

// MarkRoundAction acrescenta uma marca (feita ou pendente) a uma ação da etapa vigente (owner ou admin, com reautenticação
// recente). Só se acrescenta: a mais nova vence. Ação que cita um pedido do canal não tem caixa (ADR-0080 item 4).
func (s *Service) MarkRoundAction(ctx context.Context, actor domain.Actor, companyID, roundID, stepID domain.ID, actionID, state string) (ActionMarkView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return ActionMarkView{}, err
	}
	if state != "done" && state != "pending" {
		return ActionMarkView{}, domain.Invalid("state", "use done or pending")
	}
	identity, _ := auth.IdentityFrom(ctx)
	if _, err := s.RequireReauth(identity); err != nil {
		return ActionMarkView{}, err
	}
	var out ActionMarkView
	err := s.withTx(ctx, func(t *txc) error {
		var review, approval string
		err := t.tx.QueryRow(t.ctx, `SELECT st.review, a.approval_key FROM round_step st JOIN step_action a ON a.step_id = st.id
			WHERE st.company_id = $1 AND st.round_id = $2 AND st.id = $3 AND a.action_id = $4`, companyID, roundID, stepID, actionID).Scan(&review, &approval)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound // só a ação que existe na revisão vigente aceita marca
		}
		if err != nil {
			return fmt.Errorf("mark action: %w", err)
		}
		if review != "approved" {
			return domain.ErrNotAllowed
		}
		if approval != "" {
			return domain.Invalid("action", "the state of an action that cites an approval request is the request's")
		}
		byType, byID := "system", (*domain.ID)(nil)
		if actor.Type == domain.ActorHuman {
			byType, byID = "human", actor.ID
		}
		ns := t.now.UnixNano()
		if _, err := t.tx.Exec(t.ctx, `INSERT INTO action_mark (id, company_id, step_id, action_id, state, marked_by_type, marked_by_id, marked_unix_nano, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, domain.NewID(), companyID, stepID, actionID, state, byType, byID, ns, t.now); err != nil {
			return fmt.Errorf("mark action: %w", err)
		}
		out = ActionMarkView{State: state, MarkedByType: byType, MarkedByID: byID, MarkedAt: time.Unix(0, ns).UTC()}
		return t.activity(actor, companyID, "round_action.marked", "round_step", stepID,
			map[string]any{"round_id": roundID.String(), "action_id": actionID, "state": state}, "")
	})
	return out, err
}
