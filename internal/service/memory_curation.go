package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Consolidação de memória (ADR-0030).
//
// A curadoria é uma run de sistema pela máquina normal: a cada rodada o Hanko cria, para cada workspace da
// empresa, uma task do sistema atribuída à persona curadora e a acorda com o gatilho memory. Orçamento,
// assinatura, ledger de custos e alertas valem como para qualquer run. Quando a run termina, a consolidação
// (o registro da rodada) fecha junto com a task. O que a curadoria remove, funde ou reescreve não é aplicado
// por ela: o proxy /memory-mcp o retém como proposta (memory_proposals) e abre um gate de tipo memory.

const (
	// consolidationStaleAfter é quanto uma rodada aberta espera a run começar antes de expirar.
	consolidationStaleAfter = 6 * time.Hour
	maxConsolidationSummary = 4000
	// memoryTaskDescription é a descrição da task de sistema; o protocolo completo vai no prompt da run.
	memoryTaskDescription = "Curadoria periódica da memória deste workspace: deduplicar, fundir páginas redundantes, " +
		"marcar o que ficou obsoleto e resumir o que ficou longo, sem apagar fatos com decisão humana. " +
		"O que remove, funde ou reescreve conteúdo vira proposta e só é aplicado depois da decisão de um humano."
)

// MemoryStatus é o que a tela e a CLI mostram da memória da empresa.
type MemoryStatus struct {
	Provider         string `json:"provider"`
	Available        bool   `json:"available"` // há provedor de memória (não é none)
	Settings         domain.MemorySettings
	CuratorSlug      string `json:"curator_slug,omitempty"`
	CuratorName      string `json:"curator_name,omitempty"`
	Workspaces       int    `json:"workspaces"`
	PendingProposals int    `json:"pending_proposals"`
}

// MemoryConsolidationView é uma rodada com a run e o custo.
type MemoryConsolidationView struct {
	domain.MemoryConsolidation
	TaskKey   string   `json:"task_key,omitempty"`
	RunName   string   `json:"run_name,omitempty"`
	RunStatus string   `json:"run_status,omitempty"`
	CostUSD   *float64 `json:"cost_usd,omitempty"`
	Proposals int      `json:"proposals"`
}

// UpdateMemorySettingsInput muda a configuração; campos nil ficam como estão.
type UpdateMemorySettingsInput struct {
	Enabled          *bool
	IntervalHours    *int
	CuratorPersonaID *domain.ID
	ClearCurator     bool
}

// MemoryAvailable diz se há provedor de memória (a consolidação não existe com o provedor none).
func (s *Service) MemoryAvailable() bool {
	p := strings.TrimSpace(s.MemoryProvider)
	return p != "" && p != "none"
}

// errMemoryOff é a mensagem clara de quando a memória está desligada.
func errMemoryOff(field string) error {
	return domain.Invalid(field, "a memória está desligada (HANKO_MEMORY_PROVIDER=none); ligue o ai-memory para consolidar")
}

func settingsFromRow(r store.MemorySetting) domain.MemorySettings {
	return domain.MemorySettings{CompanyID: r.CompanyID, Enabled: r.Enabled, IntervalHours: int(r.IntervalHours),
		CuratorPersonaID: r.CuratorPersonaID, LastFiredAt: r.LastFiredAt, NextFireAt: r.NextFireAt, UpdatedAt: r.UpdatedAt}
}

func defaultMemorySettings(companyID domain.ID) domain.MemorySettings {
	return domain.MemorySettings{CompanyID: companyID, IntervalHours: domain.MemoryDefaultIntervalHours}
}

func (s *Service) loadMemorySettings(ctx context.Context, q *store.Queries, companyID domain.ID) (domain.MemorySettings, error) {
	r, err := q.GetMemorySettings(ctx, companyID)
	if isNoRows(err) {
		return defaultMemorySettings(companyID), nil
	}
	if err != nil {
		return domain.MemorySettings{}, fmt.Errorf("get memory settings: %w", err)
	}
	return settingsFromRow(r), nil
}

// GetMemoryStatus devolve a configuração, o curador e contagens da memória da empresa.
func (s *Service) GetMemoryStatus(ctx context.Context, companyID domain.ID) (MemoryStatus, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return MemoryStatus{}, err
	}
	q := s.read()
	return s.memoryStatus(ctx, q, companyID)
}

func (s *Service) memoryStatus(ctx context.Context, q *store.Queries, companyID domain.ID) (MemoryStatus, error) {
	set, err := s.loadMemorySettings(ctx, q, companyID)
	if err != nil {
		return MemoryStatus{}, err
	}
	st := MemoryStatus{Provider: "none", Available: s.MemoryAvailable(), Settings: set}
	if st.Available {
		st.Provider = strings.TrimSpace(s.MemoryProvider)
	}
	if set.CuratorPersonaID != nil {
		if p, err := q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: *set.CuratorPersonaID}); err == nil {
			st.CuratorSlug, st.CuratorName = p.Slug, p.Name
		} else if !isNoRows(err) {
			return MemoryStatus{}, fmt.Errorf("get curator: %w", err)
		}
	}
	wss, err := q.ListWorkspaces(ctx, companyID)
	if err != nil {
		return MemoryStatus{}, fmt.Errorf("list workspaces: %w", err)
	}
	st.Workspaces = len(wss)
	pend, err := q.CountPendingGatesOfKind(ctx, store.CountPendingGatesOfKindParams{CompanyID: companyID, Kind: string(domain.GateMemory)})
	if err != nil {
		return MemoryStatus{}, fmt.Errorf("count pending memory gates: %w", err)
	}
	st.PendingProposals = int(pend)
	return st, nil
}

// UpdateMemorySettings liga, desliga e configura a consolidação (admin). Ligar exige provedor de memória e
// uma persona curadora da empresa; ligar ou mudar o intervalo reagenda a próxima rodada a partir de agora.
func (s *Service) UpdateMemorySettings(ctx context.Context, actor domain.Actor, companyID domain.ID, in UpdateMemorySettingsInput) (MemoryStatus, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return MemoryStatus{}, err
	}
	if in.IntervalHours != nil && (*in.IntervalHours < domain.MemoryMinIntervalHours || *in.IntervalHours > domain.MemoryMaxIntervalHours) {
		return MemoryStatus{}, domain.Invalid("interval_hours", fmt.Sprintf("must be between %d and %d", domain.MemoryMinIntervalHours, domain.MemoryMaxIntervalHours))
	}
	if in.ClearCurator && in.CuratorPersonaID != nil {
		return MemoryStatus{}, domain.Invalid("curator", "cannot set and clear at once")
	}
	var out MemoryStatus
	err := s.withTx(ctx, func(t *txc) error {
		if _, err := t.q.GetCompanyByID(ctx, companyID); err != nil {
			return mapErr(err, "company")
		}
		cur := defaultMemorySettings(companyID)
		if r, err := t.q.GetMemorySettingsForUpdate(ctx, companyID); err == nil {
			cur = settingsFromRow(r)
		} else if !isNoRows(err) {
			return fmt.Errorf("get memory settings: %w", err)
		}
		next := cur
		var changed []string
		if in.IntervalHours != nil && *in.IntervalHours != next.IntervalHours {
			next.IntervalHours = *in.IntervalHours
			changed = append(changed, "interval_hours")
		}
		if in.ClearCurator && next.CuratorPersonaID != nil {
			next.CuratorPersonaID = nil
			changed = append(changed, "curator")
		} else if in.CuratorPersonaID != nil && (next.CuratorPersonaID == nil || *next.CuratorPersonaID != *in.CuratorPersonaID) {
			if _, err := t.q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: *in.CuratorPersonaID}); err != nil {
				return domain.Invalid("curator", "persona not found in company")
			}
			id := *in.CuratorPersonaID
			next.CuratorPersonaID = &id
			changed = append(changed, "curator")
		}
		wasEnabled := next.Enabled
		if in.Enabled != nil && *in.Enabled != next.Enabled {
			next.Enabled = *in.Enabled
			if next.Enabled {
				changed = append(changed, "enabled")
			} else {
				changed = append(changed, "disabled")
			}
		}
		if next.Enabled {
			if !s.MemoryAvailable() {
				return errMemoryOff("enabled")
			}
			if next.CuratorPersonaID == nil {
				return domain.Invalid("curator", "escolha a persona curadora antes de ligar a consolidação")
			}
		}
		switch {
		case !next.Enabled:
			next.NextFireAt = nil
		case !wasEnabled || next.NextFireAt == nil || cur.IntervalHours != next.IntervalHours:
			n := t.now.Add(time.Duration(next.IntervalHours) * time.Hour)
			next.NextFireAt = &n
		}
		if len(changed) == 0 && next.NextFireAt == cur.NextFireAt {
			var err error
			out, err = s.memoryStatus(ctx, t.q, companyID)
			return err
		}
		if _, err := t.q.UpsertMemorySettings(ctx, store.UpsertMemorySettingsParams{CompanyID: companyID, Enabled: next.Enabled,
			IntervalHours: i32(next.IntervalHours), CuratorPersonaID: next.CuratorPersonaID, NextFireAt: next.NextFireAt, Now: t.now}); err != nil {
			return mapErr(err, "update memory settings")
		}
		if err := t.activity(actor, companyID, "memory.settings_updated", "company", companyID, map[string]any{
			"fields": changed, "enabled": next.Enabled, "interval_hours": next.IntervalHours, "curator_persona_id": next.CuratorPersonaID}, ""); err != nil {
			return err
		}
		var err error
		out, err = s.memoryStatus(ctx, t.q, companyID)
		return err
	})
	return out, err
}

// DueMemorySettings lista as empresas com a consolidação ligada cuja próxima rodada chegou. Vazio sem
// provedor de memória.
func (s *Service) DueMemorySettings(ctx context.Context, now time.Time) ([]domain.MemorySettings, error) {
	if !s.MemoryAvailable() {
		return nil, nil
	}
	rows, err := s.read().DueMemorySettings(ctx, store.DueMemorySettingsParams{Now: ptr(now.UTC()), Lim: 100})
	if err != nil {
		return nil, fmt.Errorf("due memory settings: %w", err)
	}
	out := make([]domain.MemorySettings, len(rows))
	for i, r := range rows {
		out[i] = settingsFromRow(r)
	}
	return out, nil
}

// FireMemoryConsolidation dispara a rodada agendada de uma empresa: uma task de sistema por workspace,
// atribuída ao curador. Idempotente por (workspace, horário agendado) e sem empilhar: workspace com
// consolidação aberta é pulado. A agenda sempre avança, mesmo quando nada foi criado (curador parado,
// empresa sem workspace), para não repetir a tentativa a cada tick.
func (s *Service) FireMemoryConsolidation(ctx context.Context, companyID domain.ID, scheduledFor time.Time) ([]domain.MemoryConsolidation, error) {
	if !s.MemoryAvailable() {
		return nil, nil
	}
	scheduledFor = scheduledFor.UTC().Truncate(time.Microsecond)
	var out []domain.MemoryConsolidation
	err := s.withTx(ctx, func(t *txc) error {
		r, err := t.q.GetMemorySettingsForUpdate(ctx, companyID)
		if isNoRows(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("get memory settings: %w", err)
		}
		set := settingsFromRow(r)
		if !set.Enabled || set.NextFireAt == nil || set.NextFireAt.After(t.now) {
			return nil // desligada ou reagendada depois de ser listada
		}
		after := t.now
		if scheduledFor.After(after) {
			after = scheduledFor
		}
		next := after.Add(time.Duration(set.IntervalHours) * time.Hour)
		advance := func() error {
			if err := t.q.MarkMemoryFired(ctx, store.MarkMemoryFiredParams{CompanyID: companyID, LastFiredAt: &t.now, NextFireAt: &next, Now: t.now}); err != nil {
				return fmt.Errorf("mark memory fired: %w", err)
			}
			return nil
		}
		co, err := t.q.GetCompanyByID(ctx, companyID)
		if err != nil {
			return mapErr(err, "company")
		}
		var curator *store.Persona
		if set.CuratorPersonaID != nil {
			if p, err := t.q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: *set.CuratorPersonaID}); err == nil {
				curator = &p
			} else if !isNoRows(err) {
				return fmt.Errorf("get curator: %w", err)
			}
		}
		if curator == nil || !domain.PersonaStatus(curator.Status).CanWork() {
			reason := "curator_missing"
			if curator != nil {
				reason = "curator_" + curator.Status
			}
			if err := t.activity(domain.SystemActor, companyID, "memory.skipped", "company", companyID,
				map[string]any{"reason": reason, "scheduled_for": scheduledFor}, ""); err != nil {
				return err
			}
			return advance()
		}
		wss, err := t.q.ListWorkspaces(ctx, companyID)
		if err != nil {
			return fmt.Errorf("list workspaces: %w", err)
		}
		out, err = t.fireConsolidations(domain.SystemActor, co, *curator, wss, scheduledFor, domain.MemoryScheduled)
		if err != nil {
			return err
		}
		return advance()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ConsolidateMemoryNow é o "Executar agora": uma rodada manual de um workspace (ref = id ou slug) ou de todos
// (ref vazio). Exige o provedor de memória e o curador configurado; não depende de a agenda estar ligada nem a
// move. Sem nada a criar (já existe uma aberta), devolve ErrConflict.
func (s *Service) ConsolidateMemoryNow(ctx context.Context, actor domain.Actor, companyID domain.ID, workspaceRef string) ([]domain.MemoryConsolidation, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return nil, err
	}
	if !s.MemoryAvailable() {
		return nil, errMemoryOff("memory")
	}
	var out []domain.MemoryConsolidation
	err := s.withTx(ctx, func(t *txc) error {
		co, err := t.q.GetCompanyByID(ctx, companyID)
		if err != nil {
			return mapErr(err, "company")
		}
		set, err := s.loadMemorySettings(ctx, t.q, companyID)
		if err != nil {
			return err
		}
		if set.CuratorPersonaID == nil {
			return domain.Invalid("curator", "escolha a persona curadora antes de consolidar")
		}
		curator, err := t.q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: *set.CuratorPersonaID})
		if err != nil {
			return mapErr(err, "curator")
		}
		if !domain.PersonaStatus(curator.Status).CanWork() {
			return domain.Invalid("curator", fmt.Sprintf("a persona curadora não pode trabalhar agora (status %s)", curator.Status))
		}
		wss, err := t.q.ListWorkspaces(ctx, companyID)
		if err != nil {
			return fmt.Errorf("list workspaces: %w", err)
		}
		if ref := strings.TrimSpace(workspaceRef); ref != "" {
			picked := wss[:0:0]
			for _, w := range wss {
				if w.Slug == ref || w.ID.String() == ref {
					picked = append(picked, w)
				}
			}
			if len(picked) == 0 {
				return domain.Invalid("workspace", "workspace not found in company")
			}
			wss = picked
		}
		if len(wss) == 0 {
			return domain.Invalid("workspace", "a empresa não tem workspace para consolidar")
		}
		out, err = t.fireConsolidations(actor, co, curator, wss, t.now.Truncate(time.Minute), domain.MemoryManual)
		if err != nil {
			return err
		}
		if len(out) == 0 {
			return fmt.Errorf("já existe uma consolidação aberta para este workspace: %w", domain.ErrConflict)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// fireConsolidations cria, por workspace, o registro da rodada e a task de sistema do curador.
func (t *txc) fireConsolidations(actor domain.Actor, co store.Company, curator store.Persona, wss []store.Workspace,
	scheduledFor time.Time, origin domain.MemoryOrigin) ([]domain.MemoryConsolidation, error) {
	loc := loadLocation(co.Timezone)
	var out []domain.MemoryConsolidation
	for _, ws := range wss {
		row, err := t.q.InsertMemoryConsolidation(t.ctx, store.InsertMemoryConsolidationParams{ID: domain.NewID(), CompanyID: co.ID,
			WorkspaceID: &ws.ID, WorkspaceSlug: ws.Slug, Origin: string(origin), ScheduledFor: scheduledFor, Now: t.now})
		if isNoRows(err) {
			continue // a rodada já existe ou o workspace tem uma aberta
		}
		if err != nil {
			return nil, fmt.Errorf("insert memory consolidation: %w", err)
		}
		persona := curator.ID
		wsID := ws.ID
		task, err := t.createTask(actor, co.ID, CreateTaskInput{
			Title:       truncateRunes(fmt.Sprintf("Consolidação de memória — %s — %s", ws.Name, scheduledFor.In(loc).Format("2006-01-02")), 500),
			Description: memoryTaskDescription, Priority: domain.PriorityLow, PersonaID: &persona, WorkspaceID: &wsID,
			trigger: domain.TriggerMemory, noMemoryFact: true})
		if err != nil {
			return nil, fmt.Errorf("create consolidation task: %w", err)
		}
		if err := t.q.SetMemoryConsolidationTask(t.ctx, store.SetMemoryConsolidationTaskParams{ID: row.ID, TaskID: &task.ID}); err != nil {
			return nil, fmt.Errorf("set consolidation task: %w", err)
		}
		row.TaskID = &task.ID
		if err := t.activity(actor, co.ID, "memory.consolidation_started", "company", co.ID, map[string]any{
			"consolidation_id": row.ID, "workspace": ws.Slug, "task_id": task.ID, "task_key": task.Key, "origin": origin,
			"curator_persona_id": curator.ID}, ""); err != nil {
			return nil, err
		}
		out = append(out, consolidationFromRow(row))
	}
	return out, nil
}

func consolidationFromRow(r store.MemoryConsolidation) domain.MemoryConsolidation {
	return domain.MemoryConsolidation{ID: r.ID, CompanyID: r.CompanyID, WorkspaceID: r.WorkspaceID, WorkspaceSlug: r.WorkspaceSlug,
		TaskID: r.TaskID, RunID: r.RunID, Origin: domain.MemoryOrigin(r.Origin), ScheduledFor: r.ScheduledFor,
		Status: domain.MemoryConsolidationStatus(r.Status), Summary: r.Summary, CreatedAt: r.CreatedAt, FinishedAt: r.FinishedAt}
}

// ListMemoryConsolidations devolve o histórico de rodadas (mais recentes primeiro) com a run e o custo.
func (s *Service) ListMemoryConsolidations(ctx context.Context, companyID domain.ID, limit int) ([]MemoryConsolidationView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	rows, err := s.read().ListMemoryConsolidations(ctx, store.ListMemoryConsolidationsParams{CompanyID: companyID, Lim: i32(clamp(limit, 30, 200))})
	if err != nil {
		return nil, fmt.Errorf("list memory consolidations: %w", err)
	}
	out := make([]MemoryConsolidationView, len(rows))
	for i, r := range rows {
		out[i] = MemoryConsolidationView{MemoryConsolidation: consolidationFromRow(r.MemoryConsolidation), TaskKey: r.TaskKey,
			RunName: r.RunName, RunStatus: r.RunStatus, CostUSD: r.RunCostUsd, Proposals: int(r.Proposals)}
		if out[i].RunID == nil && r.LastRunID != (domain.ID{}) {
			id := r.LastRunID
			out[i].RunID = &id
		}
	}
	return out, nil
}

// consolidationOutcome mapeia o fim da run no estado da rodada e da task.
func consolidationOutcome(status domain.RunStatus) (domain.MemoryConsolidationStatus, domain.TaskStatus) {
	switch status {
	case domain.RunDone:
		return domain.ConsolidationDone, domain.TaskDone
	case domain.RunFailed, domain.RunExpired:
		return domain.ConsolidationFailed, domain.TaskCancelled
	}
	return domain.ConsolidationCancelled, domain.TaskCancelled
}

// memoryRunEnded fecha a rodada e a task de sistema quando a run de curadoria termina (qualquer desfecho).
// As propostas abertas seguem vivas: o gate é do humano e independe da run.
func (t *txc) memoryRunEnded(run store.Run, status domain.RunStatus, summary string) error {
	taskID := run.TaskID
	c, err := t.q.GetMemoryConsolidationByTaskForUpdate(t.ctx, &taskID)
	if isNoRows(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get consolidation: %w", err)
	}
	if c.Status != string(domain.ConsolidationOpen) {
		return nil
	}
	cs, ts := consolidationOutcome(status)
	return t.closeConsolidation(c, cs, ts, &run.ID, clipText(strings.TrimSpace(summary), maxConsolidationSummary))
}

func (t *txc) closeConsolidation(c store.MemoryConsolidation, cs domain.MemoryConsolidationStatus, ts domain.TaskStatus, runID *domain.ID, summary string) error {
	row, err := t.q.FinishMemoryConsolidation(t.ctx, store.FinishMemoryConsolidationParams{ID: c.ID, Status: string(cs), RunID: runID,
		Summary: summary, Now: &t.now})
	if isNoRows(err) {
		return nil
	}
	if err != nil {
		return mapErr(err, "finish consolidation")
	}
	if c.TaskID != nil {
		if err := t.closeSystemTask(c.CompanyID, *c.TaskID, ts); err != nil {
			return err
		}
	}
	return t.activity(domain.SystemActor, c.CompanyID, "memory.consolidation_finished", "company", c.CompanyID, map[string]any{
		"consolidation_id": row.ID, "workspace": row.WorkspaceSlug, "status": cs, "run_id": runID, "task_id": c.TaskID}, "")
}

// closeSystemTask encerra a task de sistema da consolidação (sem virar memória: a curadoria não se documenta
// na própria memória) para que a próxima rodada não seja barrada por uma task aberta.
func (t *txc) closeSystemTask(companyID, taskID domain.ID, to domain.TaskStatus) error {
	cur, err := t.q.GetTaskByIDForUpdate(t.ctx, store.GetTaskByIDForUpdateParams{CompanyID: companyID, ID: taskID})
	if isNoRows(err) {
		return nil
	}
	if err != nil {
		return mapErr(err, "get task")
	}
	from := domain.TaskStatus(cur.Status)
	if !from.Open() {
		return nil
	}
	now := t.now
	if _, err := t.q.UpdateTask(t.ctx, store.UpdateTaskParams{CompanyID: companyID, ID: cur.ID, Title: cur.Title, Description: cur.Description,
		Status: string(to), Priority: cur.Priority, PersonaID: cur.PersonaID, ProjectID: cur.ProjectID, ClosedAt: &now, Now: t.now}); err != nil {
		return mapErr(err, "close task")
	}
	if err := t.activity(domain.SystemActor, companyID, "task.status_changed", "task", cur.ID, map[string]any{"from": from, "to": to}, ""); err != nil {
		return err
	}
	if err := t.activity(domain.SystemActor, companyID, "task.closed", "task", cur.ID, map[string]any{"status": to}, ""); err != nil {
		return err
	}
	if tv, err := loadTaskView(t.ctx, t.q, companyID, cur.ID); err == nil {
		t.publish(companyTopic(companyID), "task.updated", tv)
	}
	return nil
}

// ResolveStaleConsolidations expira as rodadas abertas há tempo demais sem run ativa (a run nunca começou:
// orçamento estourado, curador pausado) e cancela a task, para a agenda seguir. Rede de segurança do tick.
func (s *Service) ResolveStaleConsolidations(ctx context.Context) (int, error) {
	rows, err := s.read().ListStaleMemoryConsolidations(ctx, store.ListStaleMemoryConsolidationsParams{
		Before: s.Now().UTC().Add(-consolidationStaleAfter), Lim: 50})
	if err != nil {
		return 0, fmt.Errorf("list stale consolidations: %w", err)
	}
	n := 0
	var errs []error
	for _, c := range rows {
		err := s.withTx(ctx, func(t *txc) error {
			if c.TaskID != nil { // ordem canônica: task antes de run/rodada
				if _, err := t.q.GetTaskByIDForUpdate(ctx, store.GetTaskByIDForUpdateParams{CompanyID: c.CompanyID, ID: *c.TaskID}); err != nil && !isNoRows(err) {
					return mapErr(err, "get task")
				}
			}
			cur, err := t.q.GetMemoryConsolidationByTaskForUpdate(ctx, c.TaskID)
			if isNoRows(err) || (err == nil && cur.Status != string(domain.ConsolidationOpen)) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("get consolidation: %w", err)
			}
			return t.closeConsolidation(cur, domain.ConsolidationExpired, domain.TaskCancelled, nil,
				"A curadoria não chegou a rodar (orçamento, curador parado ou fila).")
		})
		if err != nil {
			errs = append(errs, err)
			continue
		}
		n++
	}
	return n, errors.Join(errs...)
}
