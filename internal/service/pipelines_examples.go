package service

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Exemplos de decisão (treino de decisão, ADR-0034): uma decisão humana — gate decidido ou revisão de case — guardada
// com as notas de quem a salvou e o contexto até o momento da decisão (o corte). Salvar é de member humano; editar
// e apagar, só do autor; exportar (JSONL), só de admin.

const (
	maxExampleNotesBytes   = 16 << 10
	maxExampleNotesHistory = 20
	maxSnapshotComments    = 50
	maxSnapshotRuns        = 20
	maxSnapshotEvents      = 50
	maxSnapshotTasks       = 3
	maxExampleExport       = 10000
	exportPage             = 200
)

// SaveDecisionExampleInput salva uma decisão como exemplo. SourceID é o id do gate ou do evento review_decided.
type SaveDecisionExampleInput struct {
	SourceKind domain.DecisionSource
	SourceID   domain.ID
	Notes      string
}

// DecisionExampleFilter filtra a lista de exemplos.
type DecisionExampleFilter struct {
	SourceKind    domain.DecisionSource
	SourceID      *domain.ID
	Limit, Offset int
}

func decisionExampleFromRow(r store.DecisionExample) domain.DecisionExample {
	e := domain.DecisionExample{ID: r.ID, CompanyID: r.CompanyID, SourceKind: domain.DecisionSource(r.SourceKind), SourceID: r.SourceID,
		TaskID: r.TaskID, CaseID: r.CaseID, CutoffAt: r.CutoffAt, DecisionOutcome: r.DecisionOutcome, Notes: r.Notes,
		CreatedByUserID: r.CreatedByUserID, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, Snapshot: map[string]any{}}
	_ = json.Unmarshal(r.NotesHistory, &e.NotesHistory)
	_ = json.Unmarshal(r.Snapshot, &e.Snapshot)
	return e
}

func checkNotes(notes string) (string, error) {
	notes = strings.TrimSpace(notes)
	if len(notes) > maxExampleNotesBytes {
		return "", domain.Invalid("notes", fmt.Sprintf("too long (max %d bytes)", maxExampleNotesBytes))
	}
	return notes, nil
}

// SaveDecisionExample salva uma decisão como exemplo, com o snapshot montado no corte (o momento da decisão).
func (s *Service) SaveDecisionExample(ctx context.Context, actor domain.Actor, companyID domain.ID, in SaveDecisionExampleInput) (domain.DecisionExample, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return domain.DecisionExample{}, err
	}
	if actor.Type != domain.ActorHuman {
		return domain.DecisionExample{}, fmt.Errorf("decision examples are saved by humans: %w", domain.ErrNotAllowed)
	}
	if !in.SourceKind.Valid() {
		return domain.DecisionExample{}, domain.Invalid("source_kind", "use gate or case_review")
	}
	notes, err := checkNotes(in.Notes)
	if err != nil {
		return domain.DecisionExample{}, err
	}
	var out domain.DecisionExample
	err = s.withTx(ctx, func(t *txc) error {
		prefix, err := t.companyPrefix(companyID)
		if err != nil {
			return err
		}
		p := store.CreateDecisionExampleParams{ID: domain.NewID(), CompanyID: companyID, SourceKind: string(in.SourceKind), SourceID: in.SourceID,
			Notes: notes, CreatedByUserID: actor.ID, Now: t.now}
		var snap map[string]any
		switch in.SourceKind {
		case domain.DecisionFromGate:
			snap, err = t.gateSnapshot(companyID, prefix, in.SourceID, &p)
		default:
			snap, err = t.caseReviewSnapshot(companyID, prefix, in.SourceID, &p)
		}
		if err != nil {
			return err
		}
		raw, err := json.Marshal(snap)
		if err != nil {
			return fmt.Errorf("marshal snapshot: %w", err)
		}
		p.Snapshot = raw
		row, err := t.q.CreateDecisionExample(ctx, p)
		if err != nil {
			if isUniqueOn(err, "decision_examples_author_uq") {
				return fmt.Errorf("you already saved this decision as an example: %w", domain.ErrConflict)
			}
			return mapErr(err, "create decision example")
		}
		out = decisionExampleFromRow(row)
		return t.activity(actor, companyID, "decision_example.saved", "decision_example", row.ID,
			map[string]any{"source_kind": in.SourceKind, "source_id": in.SourceID, "outcome": row.DecisionOutcome}, "")
	})
	return out, err
}

// taskSnapshot é a task com os comentários e as runs até o corte.
func (t *txc) taskSnapshot(companyID domain.ID, prefix string, taskID domain.ID, cutoff time.Time) (map[string]any, error) {
	task, err := t.q.GetTaskByID(t.ctx, store.GetTaskByIDParams{CompanyID: companyID, ID: taskID})
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("snapshot task: %w", err)
	}
	cs, err := t.q.ListCommentsUntil(t.ctx, store.ListCommentsUntilParams{CompanyID: companyID, TaskID: taskID, Cutoff: cutoff, Lim: maxSnapshotComments})
	if err != nil {
		return nil, fmt.Errorf("snapshot comments: %w", err)
	}
	comments := make([]map[string]any, 0, len(cs))
	for _, c := range cs {
		comments = append(comments, map[string]any{"author": c.AuthorType, "body": clipBytes(c.Body, 4<<10), "at": c.CreatedAt})
	}
	rs, err := t.q.ListRunsUntil(t.ctx, store.ListRunsUntilParams{CompanyID: companyID, TaskID: &taskID, Cutoff: cutoff, Lim: maxSnapshotRuns})
	if err != nil {
		return nil, fmt.Errorf("snapshot runs: %w", err)
	}
	runs := make([]map[string]any, 0, len(rs))
	for _, r := range rs {
		m := map[string]any{"id": r.ID, "status": r.Status, "trigger": r.Trigger, "created_at": r.CreatedAt}
		if r.FinishedAt != nil && !r.FinishedAt.After(cutoff) {
			m["summary"], m["finished_at"] = clipBytes(r.Summary, 4<<10), r.FinishedAt
		} else {
			m["status"] = "running_at_cutoff"
		}
		runs = append(runs, m)
	}
	return map[string]any{"key": domain.TaskKey(prefix, task.Number), "title": task.Title, "description": clipBytes(task.Description, 8<<10),
		"comments": comments, "runs": runs}, nil
}

func (t *txc) gateSnapshot(companyID domain.ID, prefix string, gateID domain.ID, p *store.CreateDecisionExampleParams) (map[string]any, error) {
	g, err := t.q.GetGateByID(t.ctx, store.GetGateByIDParams{CompanyID: companyID, ID: gateID})
	if err != nil {
		return nil, mapErr(err, "get gate")
	}
	gate := gateFromRow(g)
	if !gate.Status.Decision() || gate.DecidedAt == nil {
		return nil, domain.Invalid("source_id", "the gate has not been decided")
	}
	p.CutoffAt, p.DecisionOutcome, p.TaskID = *gate.DecidedAt, string(gate.Status), gate.TaskID
	snap := map[string]any{"source": map[string]any{"kind": "gate", "id": gate.ID}, "cutoff_at": *gate.DecidedAt}
	if gate.TaskID != nil {
		ts, err := t.taskSnapshot(companyID, prefix, *gate.TaskID, *gate.DecidedAt)
		if err != nil {
			return nil, err
		}
		if ts != nil {
			snap["task"] = ts
		}
	}
	decision := map[string]any{"type": gate.Kind, "payload": map[string]any{"title": gate.Title, "body": clipBytes(gate.Body, 8<<10),
		"requested_status": gate.RequestedStatus}, "outcome": gate.Status, "note": gate.DecisionNote}
	if gate.DecidedBy != nil {
		decision["actor"] = gate.DecidedBy
	}
	snap["decision"] = decision
	return snap, nil
}

func (t *txc) caseReviewSnapshot(companyID domain.ID, prefix string, eventID domain.ID, p *store.CreateDecisionExampleParams) (map[string]any, error) {
	ev, err := t.q.GetCaseEvent(t.ctx, store.GetCaseEventParams{CompanyID: companyID, ID: eventID})
	if err != nil {
		return nil, mapErr(err, "get case event")
	}
	if ev.Kind != string(domain.CaseEvReviewDecided) {
		return nil, domain.Invalid("source_id", "not a review decision")
	}
	e := caseEventFromRow(ev)
	decision, _ := e.Payload["decision"].(string)
	p.CutoffAt, p.DecisionOutcome, p.CaseID = ev.CreatedAt, decision, &ev.CaseID
	c, err := t.q.GetCaseByID(t.ctx, store.GetCaseByIDParams{CompanyID: companyID, ID: ev.CaseID})
	if err != nil {
		return nil, mapErr(err, "get case")
	}
	caseSnap := map[string]any{"identifier": domain.CaseIdentifier(prefix, c.Number), "case_type": c.CaseType}
	if sn, ok := e.Payload["snapshot"].(map[string]any); ok {
		for k, v := range sn {
			caseSnap[k] = v
		}
	}
	if c.PipelineID != nil {
		if pl, err := t.q.GetPipelineByID(t.ctx, store.GetPipelineByIDParams{CompanyID: companyID, ID: *c.PipelineID}); err == nil {
			caseSnap["pipeline"] = map[string]any{"key": pl.Key, "name": pl.Name}
		}
	}
	evs, err := t.q.ListCaseEventsUntil(t.ctx, store.ListCaseEventsUntilParams{CompanyID: companyID, CaseID: c.ID, Cutoff: ev.CreatedAt, Lim: maxSnapshotEvents})
	if err != nil {
		return nil, fmt.Errorf("snapshot case events: %w", err)
	}
	history := make([]map[string]any, 0, len(evs))
	for _, r := range evs {
		ce := caseEventFromRow(r)
		h := map[string]any{"kind": ce.Kind, "at": ce.CreatedAt, "actor": ce.Actor}
		for _, k := range []string{"from", "to", "reason", "rationale", "decision"} {
			if v, ok := ce.Payload[k]; ok {
				h[k] = v
			}
		}
		history = append(history, h)
	}
	caseSnap["history"] = history
	snap := map[string]any{"source": map[string]any{"kind": "case_review", "id": ev.ID}, "cutoff_at": ev.CreatedAt, "case": caseSnap}
	links, err := t.q.ListCaseLinks(t.ctx, store.ListCaseLinksParams{CompanyID: companyID, CaseID: c.ID})
	if err != nil {
		return nil, fmt.Errorf("snapshot links: %w", err)
	}
	var tasks []map[string]any
	for i := len(links) - 1; i >= 0 && len(tasks) < maxSnapshotTasks; i-- {
		if links[i].CreatedAt.After(ev.CreatedAt) {
			continue
		}
		ts, err := t.taskSnapshot(companyID, prefix, links[i].TaskID, ev.CreatedAt)
		if err != nil {
			return nil, err
		}
		if ts != nil {
			ts["role"] = links[i].Role
			tasks = append(tasks, ts)
		}
	}
	if len(tasks) > 0 {
		snap["tasks"] = tasks
	}
	snap["decision"] = map[string]any{"type": "case_review", "payload": map[string]any{"from": e.Payload["from"], "to": e.Payload["to"],
		"approved_version": e.Payload["approved_version"]}, "actor": e.Actor, "outcome": decision, "note": e.Payload["reason"]}
	return snap, nil
}

// authorOf confere que quem edita é o autor do exemplo.
func authorOf(actor domain.Actor, ex store.DecisionExample) error {
	if actor.Type != domain.ActorHuman || !sameID(actor.ID, ex.CreatedByUserID) {
		return fmt.Errorf("only the author edits or deletes a decision example: %w", domain.ErrNotAllowed)
	}
	return nil
}

// UpdateDecisionExampleNotes troca as notas (só o autor); a versão anterior vai para o histórico.
func (s *Service) UpdateDecisionExampleNotes(ctx context.Context, actor domain.Actor, companyID, id domain.ID, notes string) (domain.DecisionExample, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return domain.DecisionExample{}, err
	}
	notes, err := checkNotes(notes)
	if err != nil {
		return domain.DecisionExample{}, err
	}
	var out domain.DecisionExample
	err = s.withTx(ctx, func(t *txc) error {
		ex, err := t.q.GetDecisionExampleForUpdate(ctx, store.GetDecisionExampleForUpdateParams{CompanyID: companyID, ID: id})
		if err != nil {
			return mapErr(err, "get decision example")
		}
		if err := authorOf(actor, ex); err != nil {
			return err
		}
		if ex.Notes == notes {
			out = decisionExampleFromRow(ex)
			return nil
		}
		var hist []domain.DecisionNote
		_ = json.Unmarshal(ex.NotesHistory, &hist)
		hist = append(hist, domain.DecisionNote{Notes: ex.Notes, At: ex.UpdatedAt})
		if len(hist) > maxExampleNotesHistory {
			hist = hist[len(hist)-maxExampleNotesHistory:]
		}
		raw, err := json.Marshal(hist)
		if err != nil {
			return fmt.Errorf("marshal notes history: %w", err)
		}
		row, err := t.q.UpdateDecisionExampleNotes(ctx, store.UpdateDecisionExampleNotesParams{CompanyID: companyID, ID: id, Notes: notes, NotesHistory: raw, Now: t.now})
		if err != nil {
			return mapErr(err, "update decision example")
		}
		out = decisionExampleFromRow(row)
		return t.activity(actor, companyID, "decision_example.updated", "decision_example", id, nil, "")
	})
	return out, err
}

// DeleteDecisionExample apaga um exemplo (só o autor).
func (s *Service) DeleteDecisionExample(ctx context.Context, actor domain.Actor, companyID, id domain.ID) error {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return err
	}
	return s.withTx(ctx, func(t *txc) error {
		ex, err := t.q.GetDecisionExampleForUpdate(ctx, store.GetDecisionExampleForUpdateParams{CompanyID: companyID, ID: id})
		if err != nil {
			return mapErr(err, "get decision example")
		}
		if err := authorOf(actor, ex); err != nil {
			return err
		}
		if _, err := t.q.DeleteDecisionExample(ctx, store.DeleteDecisionExampleParams{CompanyID: companyID, ID: id}); err != nil {
			return fmt.Errorf("delete decision example: %w", err)
		}
		return t.activity(actor, companyID, "decision_example.deleted", "decision_example", id, map[string]any{"source_kind": ex.SourceKind}, "")
	})
}

// GetDecisionExample devolve um exemplo.
func (s *Service) GetDecisionExample(ctx context.Context, companyID, id domain.ID) (domain.DecisionExample, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return domain.DecisionExample{}, err
	}
	r, err := s.read().GetDecisionExample(ctx, store.GetDecisionExampleParams{CompanyID: companyID, ID: id})
	if err != nil {
		return domain.DecisionExample{}, mapErr(err, "get decision example")
	}
	return decisionExampleFromRow(r), nil
}

// ListDecisionExamples lista os exemplos (mais recentes primeiro).
func (s *Service) ListDecisionExamples(ctx context.Context, companyID domain.ID, f DecisionExampleFilter) ([]domain.DecisionExample, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	p := store.ListDecisionExamplesParams{CompanyID: companyID, SourceID: f.SourceID, Lim: i32(clamp(f.Limit, 100, 500)), Off: i32(max(f.Offset, 0))}
	if f.SourceKind != "" {
		if !f.SourceKind.Valid() {
			return nil, domain.Invalid("source_kind", "use gate or case_review")
		}
		k := string(f.SourceKind)
		p.SourceKind = &k
	}
	rows, err := s.read().ListDecisionExamples(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("list decision examples: %w", err)
	}
	out := make([]domain.DecisionExample, len(rows))
	for i, r := range rows {
		out[i] = decisionExampleFromRow(r)
	}
	return out, nil
}

// decisionExampleLine é uma linha do JSONL exportado.
type decisionExampleLine struct {
	ID              domain.ID      `json:"id"`
	SourceKind      string         `json:"source_kind"`
	SourceID        domain.ID      `json:"source_id"`
	DecisionOutcome string         `json:"decision_outcome"`
	CutoffAt        time.Time      `json:"cutoff_at"`
	Notes           string         `json:"notes"`
	Snapshot        map[string]any `json:"snapshot"`
	CreatedAt       time.Time      `json:"created_at"`
}

// ExportDecisionExamples escreve os exemplos em JSONL (um objeto por linha, mais recentes primeiro, até 10 000) (admin).
func (s *Service) ExportDecisionExamples(ctx context.Context, companyID domain.ID, w io.Writer) (int, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return 0, err
	}
	q := s.read()
	bw := bufio.NewWriter(w)
	enc := json.NewEncoder(bw)
	enc.SetEscapeHTML(false)
	n := 0
	for off := 0; off < maxExampleExport; off += exportPage {
		rows, err := q.ListDecisionExamples(ctx, store.ListDecisionExamplesParams{CompanyID: companyID, Lim: exportPage, Off: i32(off)})
		if err != nil {
			return n, fmt.Errorf("list decision examples: %w", err)
		}
		for _, r := range rows {
			e := decisionExampleFromRow(r)
			if err := enc.Encode(decisionExampleLine{ID: e.ID, SourceKind: string(e.SourceKind), SourceID: e.SourceID, DecisionOutcome: e.DecisionOutcome,
				CutoffAt: e.CutoffAt, Notes: e.Notes, Snapshot: e.Snapshot, CreatedAt: e.CreatedAt}); err != nil {
				return n, fmt.Errorf("write jsonl: %w", err)
			}
			n++
		}
		if len(rows) < exportPage {
			break
		}
	}
	return n, bw.Flush()
}
