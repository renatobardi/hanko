package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Máquina de estados dos cards de status (ADR-0032): montagem, avaliação da política, geração do resumo,
// gravação pela resumidora, fim da run e o tick periódico.

const (
	// maxCardContextBytes é o teto do pacote de contexto que vai na descrição da task de geração.
	maxCardContextBytes = 60 << 10
	// maxCardCommentExcerpt é quanto do último comentário humano de cada task entra no pacote.
	maxCardCommentExcerpt = 300
	// maxCardChangesStored é quantas mudanças uma rodada guarda.
	maxCardChangesStored = 200
	// cardPollReactive/Interval/Manual são os intervalos de verificação de mudanças por política.
	cardPollReactive = time.Minute
	cardPollInterval = 5 * time.Minute
	cardPollManual   = 15 * time.Minute
	// cardTickBatch é quantos cards um tick avalia.
	cardTickBatch = 200

	cardCompileTitle = "Montar card de status: "
	cardUpdateTitle  = "Atualizar card de status: "
)

// evalReason é o motivo de uma avaliação: o tick (auto) segue a política; os demais são pedidos explícitos.
type evalReason int

const (
	evalAuto evalReason = iota
	evalManual
	evalManualFull
	evalRestore
	evalQuery
)

func (r evalReason) explicit() bool { return r != evalAuto }

func (r evalReason) trigger(mode string) domain.CardUpdateTrigger {
	switch r {
	case evalManual, evalManualFull:
		return domain.CardTriggerManual
	case evalRestore:
		return domain.CardTriggerRestore
	case evalQuery:
		return domain.CardTriggerQuery
	}
	switch mode {
	case domain.CardModeInterval:
		return domain.CardTriggerInterval
	case domain.CardModeCron:
		return domain.CardTriggerCron
	}
	return domain.CardTriggerReactive
}

// ---------- tasks observadas ----------

type cardMatch struct {
	tasks     []CardMatchedTask
	perQuery  []int
	truncated bool
}

// matchCard roda os filtros do card e junta as tasks (sem repetir), mais recentes primeiro, até
// domain.MaxCardObservedTasks.
func matchCard(ctx context.Context, q *store.Queries, companyID domain.ID, queries []domain.StatusCardQuery) (cardMatch, error) {
	m := cardMatch{perQuery: make([]int, len(queries))}
	byID := map[domain.ID]int{}
	for i, cq := range queries {
		p := store.StatusCardMatchTasksParams{CompanyID: companyID, PersonaID: cq.PersonaID, ProjectID: cq.ProjectID, GoalID: cq.GoalID,
			OpenOnly: cq.OpenOnly, Lim: domain.MaxCardObservedTasks + 1}
		for _, s := range cq.Statuses {
			p.Statuses = append(p.Statuses, string(s))
		}
		for _, pr := range cq.Priorities {
			p.Priorities = append(p.Priorities, string(pr))
		}
		if t := strings.TrimSpace(cq.Text); t != "" {
			e := escapeLike(t)
			p.Q = &e
		}
		rows, err := q.StatusCardMatchTasks(ctx, p)
		if err != nil {
			return cardMatch{}, fmt.Errorf("status card match: %w", err)
		}
		if len(rows) > domain.MaxCardObservedTasks {
			rows, m.truncated = rows[:domain.MaxCardObservedTasks], true
		}
		m.perQuery[i] = len(rows)
		for _, r := range rows {
			if j, ok := byID[r.ID]; ok {
				m.tasks[j].Queries = append(m.tasks[j].Queries, i)
				continue
			}
			mt := CardMatchedTask{CardTaskRef: CardTaskRef{ID: r.ID, Key: domain.TaskKey(r.TaskPrefix, r.Number), Title: r.Title,
				Status: domain.TaskStatus(r.Status), Priority: domain.Priority(r.Priority), PersonaName: r.PersonaName},
				PersonaID: r.PersonaID, UpdatedAt: r.UpdatedAt, Queries: []int{i}}
			if r.HumanCommentID != "" {
				at := r.HumanCommentAt
				mt.HumanCommentID, mt.HumanCommentAt, mt.HumanComment = r.HumanCommentID, &at, r.HumanCommentBody
			}
			byID[r.ID] = len(m.tasks)
			m.tasks = append(m.tasks, mt)
		}
	}
	sortMatched(m.tasks)
	if len(m.tasks) > domain.MaxCardObservedTasks {
		m.tasks, m.truncated = m.tasks[:domain.MaxCardObservedTasks], true
	}
	if m.tasks == nil {
		m.tasks = []CardMatchedTask{}
	}
	return m, nil
}

func sortMatched(ts []CardMatchedTask) {
	// insertion sort estável: listas pequenas (até alguns milhares) e já quase ordenadas
	for i := 1; i < len(ts); i++ {
		for j := i; j > 0 && ts[j].UpdatedAt.After(ts[j-1].UpdatedAt); j-- {
			ts[j], ts[j-1] = ts[j-1], ts[j]
		}
	}
}

func fingerprintOf(ts []CardMatchedTask) domain.CardFingerprint {
	fp := domain.CardFingerprint{}
	for _, t := range ts {
		p := domain.CardTaskPrint{Key: t.Key, Title: t.Title, Status: t.Status, Priority: t.Priority, PersonaName: t.PersonaName,
			UpdatedAt: t.UpdatedAt.UTC(), HumanCommentID: t.HumanCommentID, HumanCommentAt: t.HumanCommentAt}
		if t.PersonaID != nil {
			p.PersonaID = t.PersonaID.String()
		}
		fp[t.ID.String()] = p
	}
	return fp
}

// ---------- montagem ----------

// startCardCompile cria a task de sistema "Montar card de status" para a resumidora e grava o card em
// compiling. Uma geração em andamento é encerrada (a montagem nova vale mais). Sem resumidora, o card fica em
// erro com o motivo.
func (t *txc) startCardCompile(row store.StatusCard, trig domain.CardUpdateTrigger) (store.StatusCard, error) {
	var err error
	if row.GeneratingTaskID != nil {
		if row, err = t.abortCardGeneration(row, "substituída por uma montagem nova"); err != nil {
			return store.StatusCard{}, err
		}
	}
	if row.SummarizerPersonaID == nil {
		row.State, row.FailureReason, row.NextEvalAt = string(domain.CardError), "Escolha a persona resumidora para montar o card.", nil
		return t.saveCard(row)
	}
	c := cardFromRow(row)
	names, err := loadCardNames(t.ctx, t.q, row.CompanyID)
	if err != nil {
		return store.StatusCard{}, err
	}
	task, err := t.createTask(domain.SystemActor, row.CompanyID, CreateTaskInput{
		Title:       truncateRunes(cardCompileTitle+cardTitleOrDefault(row.Title), 500),
		Description: compileContext(c, names), Priority: domain.PriorityLow, PersonaID: row.SummarizerPersonaID,
		trigger: domain.TriggerStatusCard, noMemoryFact: true})
	if err != nil {
		return store.StatusCard{}, fmt.Errorf("create compile task: %w", err)
	}
	if _, err := t.q.InsertStatusCardUpdate(t.ctx, store.InsertStatusCardUpdateParams{ID: domain.NewID(), CompanyID: row.CompanyID, CardID: row.ID,
		Kind: string(domain.CardUpdateCompile), Trigger: string(trig), TaskID: &task.ID, Changes: []byte("[]"), Fingerprint: []byte("{}"),
		QueryVersion: row.QueryVersion, Now: t.now}); err != nil {
		return store.StatusCard{}, mapErr(err, "insert status card update")
	}
	until := t.now.Add(domain.CardGenerationTimeout)
	row.InterestHash = interestHash(row.Interest)
	row.State, row.FailureReason = string(domain.CardCompiling), ""
	row.GeneratingTaskID, row.GeneratingSince, row.NextEvalAt = &task.ID, &t.now, &until
	if row, err = t.saveCard(row); err != nil {
		return store.StatusCard{}, err
	}
	if err := t.activity(domain.SystemActor, row.CompanyID, "status_card.compile_started", "status_card", row.ID, map[string]any{
		"task_id": task.ID, "task_key": task.Key, "summarizer_persona_id": row.SummarizerPersonaID}, ""); err != nil {
		return store.StatusCard{}, err
	}
	return row, nil
}

// abortCardGeneration encerra a geração em andamento (rodada falha, run cancelada, task de sistema fechada) e
// devolve o card sem a geração, ainda sem gravar.
func (t *txc) abortCardGeneration(row store.StatusCard, reason string) (store.StatusCard, error) {
	taskID := *row.GeneratingTaskID
	u, err := t.q.GetStatusCardUpdateByTaskForUpdate(t.ctx, taskID)
	if err != nil && !isNoRows(err) {
		return store.StatusCard{}, fmt.Errorf("get status card update: %w", err)
	}
	if err == nil && u.Status == string(domain.CardUpdateRunning) {
		if _, err := t.q.FinishStatusCardUpdate(t.ctx, store.FinishStatusCardUpdateParams{ID: u.ID, Status: string(domain.CardUpdateFailed),
			Error: reason, ChangeSummary: u.ChangeSummary, Now: &t.now}); err != nil {
			return store.StatusCard{}, mapErr(err, "finish status card update")
		}
	}
	if err := t.cancelTaskRun(domain.SystemActor, row.CompanyID, taskID, "status_card_aborted"); err != nil {
		return store.StatusCard{}, err
	}
	if err := t.closeSystemTask(row.CompanyID, taskID, domain.TaskCancelled); err != nil {
		return store.StatusCard{}, err
	}
	row.GeneratingTaskID, row.GeneratingSince = nil, nil
	return row, nil
}

// failCardGeneration encerra a geração e deixa o card em erro com o motivo (o último resumo bom fica).
func (t *txc) failCardGeneration(row store.StatusCard, reason string) (store.StatusCard, error) {
	var err error
	if row.GeneratingTaskID != nil {
		if row, err = t.abortCardGeneration(row, reason); err != nil {
			return store.StatusCard{}, err
		}
	}
	row.State, row.FailureReason, row.NextEvalAt = string(domain.CardError), reason, nil
	if row, err = t.saveCard(row); err != nil {
		return store.StatusCard{}, err
	}
	return row, t.activity(domain.SystemActor, row.CompanyID, "status_card.failed", "status_card", row.ID, map[string]any{"reason": reason}, "")
}

// ---------- avaliação ----------

func nextCardPoll(pol domain.CardPolicy, row store.StatusCard, now time.Time) time.Time {
	switch pol.Mode {
	case domain.CardModeManual:
		return now.Add(cardPollManual)
	case domain.CardModeInterval:
		next := now.Add(cardPollInterval)
		if row.LastGeneratedAt != nil {
			if due := row.LastGeneratedAt.Add(time.Duration(pol.IntervalMinutes) * time.Minute); due.Before(next) && due.After(now) {
				next = due
			}
		}
		return next
	case domain.CardModeCron:
		return nextCronPoll(pol, row.LastGeneratedAt, now)
	}
	return now.Add(cardPollReactive)
}

// evaluateCard compara as tasks observadas com o retrato da última versão, aplica a política e, se for a hora,
// dispara a geração. Grava o card e o devolve.
func (t *txc) evaluateCard(row store.StatusCard, reason evalReason) (store.StatusCard, error) {
	now := t.now
	pol := policyFromRaw(row.RefreshPolicy)
	save := func(next *time.Time) (store.StatusCard, error) {
		row.NextEvalAt = next
		return t.saveCard(row)
	}
	decision := domain.CardEngine(row.Engine) == domain.CardEngineDecision
	switch {
	case row.ArchivedAt != nil:
		return save(nil)
	case decision && row.GeneratingSince != nil:
		if now.Sub(*row.GeneratingSince) >= cardDecisionTimeout {
			return t.failDecisionGeneration(row, "A geração pelo decision model não terminou (o Hanko reiniciou ou o modelo não respondeu). Rode de novo para tentar outra vez.")
		}
		if reason.explicit() {
			return store.StatusCard{}, fmt.Errorf("o card já está sendo atualizado: %w", domain.ErrConflict)
		}
		until := row.GeneratingSince.Add(cardDecisionTimeout)
		return save(&until)
	case row.GeneratingTaskID != nil:
		if row.GeneratingSince == nil || now.Sub(*row.GeneratingSince) >= domain.CardGenerationTimeout {
			return t.failCardGeneration(row, "A geração não terminou em 2 h (resumidora pausada, sem orçamento ou fila parada). Rode de novo para tentar outra vez.")
		}
		if reason.explicit() {
			return store.StatusCard{}, fmt.Errorf("o card já está sendo atualizado: %w", domain.ErrConflict)
		}
		until := row.GeneratingSince.Add(domain.CardGenerationTimeout)
		return save(&until)
	case !decision && row.SummarizerPersonaID == nil:
		if reason.explicit() {
			return store.StatusCard{}, domain.Invalid("summarizer", "escolha a persona resumidora antes de atualizar o card")
		}
		row.State, row.FailureReason = string(domain.CardError), "Escolha a persona resumidora para atualizar o card."
		return save(nil)
	case row.QueryVersion == 0:
		if reason.explicit() {
			return store.StatusCard{}, domain.Invalid("queries", "o card ainda não tem filtros: rode a montagem de novo")
		}
		return save(nil)
	case row.State == string(domain.CardError) && !reason.explicit():
		return save(nil) // erro só sai com um pedido explícito (atualizar ou montar de novo)
	}

	c := cardFromRow(row)
	m, err := matchCard(t.ctx, t.q, row.CompanyID, c.Queries)
	if err != nil {
		return store.StatusCard{}, err
	}
	fp := fingerprintOf(m.tasks)
	changes := domain.DiffFingerprints(fingerprintFromRaw(row.Fingerprint), fp, pol.Triggers)
	if h := domain.HashChanges(changes); h != row.PendingChangeHash {
		row.PendingChangeHash = h
		if h != "" {
			row.LastChangeAt = &now
		}
	}
	row.PendingChangeCount = i32(len(changes))
	hasSummary := row.SummaryRevision > 0

	spent, err := t.q.CardTokensSince(t.ctx, store.CardTokensSinceParams{CardID: row.ID, Since: pol.DayStart(now)})
	if err != nil {
		return store.StatusCard{}, fmt.Errorf("card tokens: %w", err)
	}
	if spent >= pol.DailyTokenCap {
		if row.State != string(domain.CardPausedBudget) {
			if err := t.activity(domain.SystemActor, row.CompanyID, "status_card.paused", "status_card", row.ID,
				map[string]any{"reason": "daily_token_cap", "tokens": spent, "cap": pol.DailyTokenCap}, ""); err != nil {
				return store.StatusCard{}, err
			}
		}
		row.State = string(domain.CardPausedBudget)
		next := pol.NextDay(now)
		return save(&next)
	}
	if !reason.explicit() {
		if pol.ActiveHours != nil && !pol.ActiveHours.Contains(now) {
			row.State = string(domain.CardPausedHours)
			next := pol.ActiveHours.NextStart(now)
			return save(&next)
		}
	}
	if row.State == string(domain.CardPausedBudget) || row.State == string(domain.CardPausedHours) || row.State == string(domain.CardError) {
		row.State = string(domain.CardActive)
	}
	if !reason.explicit() {
		due := false
		pending := len(changes) > 0 || !hasSummary || (decision && row.SummaryQueryVersion != row.QueryVersion)
		switch pol.Mode {
		case domain.CardModeInterval:
			due = pending && (row.LastGeneratedAt == nil || !now.Before(row.LastGeneratedAt.Add(time.Duration(pol.IntervalMinutes)*time.Minute)))
		case domain.CardModeCron:
			due = cronDue(pol, row.LastGeneratedAt, now) && (pending || !pol.CronSkipUnchanged)
		case domain.CardModeReactive:
			if pending {
				if hasSummary && row.LastChangeAt != nil {
					if wait := row.LastChangeAt.Add(time.Duration(pol.DebounceSeconds) * time.Second); now.Before(wait) {
						return save(&wait)
					}
				}
				due = true
			}
		}
		if !due {
			next := nextCardPoll(pol, row, now)
			return save(&next)
		}
		gens, err := t.q.CardGenerationsSince(t.ctx, store.CardGenerationsSinceParams{CardID: row.ID, Since: now.Add(-time.Hour)})
		if err != nil {
			return store.StatusCard{}, fmt.Errorf("card generations: %w", err)
		}
		if len(gens) >= pol.MaxUpdatesPerHour {
			next := gens[0].Add(time.Hour)
			return save(&next)
		}
	}
	if decision { // o decision model refaz o resumo inteiro numa chamada só (ADR-0048)
		return t.startDecisionGeneration(row, reason.trigger(pol.Mode), changes, fp, m)
	}
	kind := domain.ChooseCardUpdateKind(domain.CardKindInput{ExplicitFull: reason == evalManualFull, HasSummary: hasSummary,
		Changes: len(changes), QueryChanged: int(row.SummaryQueryVersion) != int(row.QueryVersion),
		IncrementalsSinceFull: int(row.IncrementalsSinceFull), Restore: reason == evalRestore})
	return t.startCardGeneration(row, kind, reason.trigger(pol.Mode), changes, fp, m)
}

// startCardGeneration cria a task "Atualizar card de status" com o pacote de contexto e registra a rodada.
func (t *txc) startCardGeneration(row store.StatusCard, kind domain.CardUpdateKind, trig domain.CardUpdateTrigger,
	changes []domain.CardChange, fp domain.CardFingerprint, m cardMatch) (store.StatusCard, error) {
	c := cardFromRow(row)
	names, err := loadCardNames(t.ctx, t.q, row.CompanyID)
	if err != nil {
		return store.StatusCard{}, err
	}
	task, err := t.createTask(domain.SystemActor, row.CompanyID, CreateTaskInput{
		Title:       truncateRunes(cardUpdateTitle+cardTitleOrDefault(row.Title), 500),
		Description: updateContext(c, names, kind, changes, m), Priority: domain.PriorityLow, PersonaID: row.SummarizerPersonaID,
		trigger: domain.TriggerStatusCard, noMemoryFact: true})
	if err != nil {
		return store.StatusCard{}, fmt.Errorf("create status card task: %w", err)
	}
	stored := changes
	if len(stored) > maxCardChangesStored {
		stored = stored[:maxCardChangesStored]
	}
	if stored == nil {
		stored = []domain.CardChange{}
	}
	if _, err := t.q.InsertStatusCardUpdate(t.ctx, store.InsertStatusCardUpdateParams{ID: domain.NewID(), CompanyID: row.CompanyID, CardID: row.ID,
		Kind: string(kind), Trigger: string(trig), TaskID: &task.ID, Changes: mustJSON(stored), Fingerprint: mustJSON(fp),
		QueryVersion: row.QueryVersion, Now: t.now}); err != nil {
		return store.StatusCard{}, mapErr(err, "insert status card update")
	}
	until := t.now.Add(domain.CardGenerationTimeout)
	row.State, row.FailureReason = string(domain.CardActive), ""
	row.GeneratingTaskID, row.GeneratingSince, row.NextEvalAt = &task.ID, &t.now, &until
	if row, err = t.saveCard(row); err != nil {
		return store.StatusCard{}, err
	}
	if err := t.activity(domain.SystemActor, row.CompanyID, "status_card.update_started", "status_card", row.ID, map[string]any{
		"kind": kind, "trigger": trig, "changes": len(changes), "task_id": task.ID, "task_key": task.Key}, ""); err != nil {
		return store.StatusCard{}, err
	}
	return row, nil
}

// ---------- escrita da resumidora (MCP) ----------

// cardGenerationFor confere que a chamada vem da resumidora, na task de geração ativa do card, e devolve a
// rodada travada.
func (t *txc) cardGenerationFor(card store.StatusCard, ref CardRunRef, kinds ...domain.CardUpdateKind) (store.StatusCardUpdate, error) {
	if card.ArchivedAt != nil {
		return store.StatusCardUpdate{}, domain.Invalid("archived", "o card está arquivado")
	}
	if card.SummarizerPersonaID == nil || *card.SummarizerPersonaID != ref.PersonaID {
		return store.StatusCardUpdate{}, fmt.Errorf("status card: só a persona resumidora do card grava nele: %w", domain.ErrNotAllowed)
	}
	if card.GeneratingTaskID == nil || *card.GeneratingTaskID != ref.TaskID {
		return store.StatusCardUpdate{}, fmt.Errorf("status card: esta execução não é a geração ativa do card (ela foi substituída ou já terminou): %w", domain.ErrNotAllowed)
	}
	u, err := t.q.GetStatusCardUpdateByTaskForUpdate(t.ctx, ref.TaskID)
	if err != nil {
		return store.StatusCardUpdate{}, mapErr(err, "status card update")
	}
	ok := false
	for _, k := range kinds {
		if domain.CardUpdateKind(u.Kind) == k {
			ok = true
		}
	}
	if !ok {
		if u.Kind == string(domain.CardUpdateCompile) {
			return store.StatusCardUpdate{}, domain.Invalid("task", "esta é a montagem do card: grave os filtros com hanko_status_card_set_query")
		}
		return store.StatusCardUpdate{}, domain.Invalid("task", "esta é uma atualização do card: grave o resumo com hanko_status_card_set_summary")
	}
	if u.Status != string(domain.CardUpdateRunning) {
		return store.StatusCardUpdate{}, fmt.Errorf("status card: a rodada já terminou: %w", domain.ErrConflict)
	}
	return u, nil
}

func cardClip(s string, n int) string { return truncateRunes(strings.TrimSpace(s), n) }

// SetStatusCardQuery grava os filtros da montagem (só a resumidora, na task de montagem ativa) e dispara a
// primeira atualização do resumo, do zero.
func (s *Service) SetStatusCardQuery(ctx context.Context, actor domain.Actor, companyID domain.ID, ref CardRunRef, cardID domain.ID, in SetCardQueryInput) (StatusCardView, error) {
	var out StatusCardView
	err := s.withTx(ctx, func(t *txc) error {
		card, err := t.q.GetStatusCardForUpdate(ctx, store.GetStatusCardForUpdateParams{CompanyID: companyID, ID: cardID})
		if err != nil {
			return mapErr(err, "status card")
		}
		u, err := t.cardGenerationFor(card, ref, domain.CardUpdateCompile)
		if err != nil {
			return err
		}
		qs, err := t.resolveCardQueries(companyID, in.Queries)
		if err != nil {
			return err
		}
		if !card.TitlePinned && strings.TrimSpace(in.Title) != "" {
			title, err := checkCardTitle(in.Title)
			if err != nil {
				return err
			}
			card.Title = title
		}
		summary := cardClip(in.ChangeSummary, domain.MaxCardChangeSummaryRunes)
		if _, err := t.q.FinishStatusCardUpdate(ctx, store.FinishStatusCardUpdateParams{ID: u.ID, Status: string(domain.CardUpdateOK),
			ChangeSummary: summary, Now: &t.now}); err != nil {
			return mapErr(err, "finish status card update")
		}
		card.Queries = mustJSON(qs)
		card.QueryVersion++
		card.QueryCompiledAt = &t.now
		card.State, card.FailureReason = string(domain.CardActive), ""
		card.GeneratingTaskID, card.GeneratingSince = nil, nil
		if card, err = t.saveCard(card); err != nil {
			return err
		}
		if err := t.activity(actor, companyID, "status_card.query_set", "status_card", card.ID, map[string]any{
			"query_version": card.QueryVersion, "queries": len(qs), "change_summary": summary}, ""); err != nil {
			return err
		}
		if card, err = t.evaluateCard(card, evalQuery); err != nil {
			return err
		}
		out, err = s.cardView(ctx, t.q, card, false)
		return err
	})
	return out, err
}

// SetStatusCardSummary grava o resumo (só a resumidora, na task de atualização ativa): nova revisão, retrato
// das tasks da geração, pendências zeradas e a próxima verificação agendada.
func (s *Service) SetStatusCardSummary(ctx context.Context, actor domain.Actor, companyID domain.ID, ref CardRunRef, cardID domain.ID, in SetCardSummaryInput) (StatusCardView, []string, error) {
	md := strings.TrimSpace(in.Markdown)
	if md == "" || len(md) > domain.MaxCardSummaryBytes {
		return StatusCardView{}, nil, domain.Invalid("markdown", fmt.Sprintf("o resumo precisa ter de 1 byte a %d KiB", domain.MaxCardSummaryBytes>>10))
	}
	if !utf8.ValidString(md) {
		return StatusCardView{}, nil, domain.Invalid("markdown", "o resumo precisa ser texto UTF-8")
	}
	if len(in.Mentioned) > domain.MaxCardMentions {
		return StatusCardView{}, nil, domain.Invalid("mentioned", fmt.Sprintf("cite no máximo %d tasks", domain.MaxCardMentions))
	}
	var out StatusCardView
	var unknown []string
	err := s.withTx(ctx, func(t *txc) error {
		card, err := t.q.GetStatusCardForUpdate(ctx, store.GetStatusCardForUpdateParams{CompanyID: companyID, ID: cardID})
		if err != nil {
			return mapErr(err, "status card")
		}
		u, err := t.cardGenerationFor(card, ref, domain.CardUpdateFull, domain.CardUpdateIncremental)
		if err != nil {
			return err
		}
		if !card.TitlePinned && strings.TrimSpace(in.Title) != "" {
			title, err := checkCardTitle(in.Title)
			if err != nil {
				return err
			}
			card.Title = title
		}
		mentioned, miss, err := t.resolveTaskKeys(companyID, in.Mentioned)
		if err != nil {
			return err
		}
		unknown = miss
		model := ""
		if ref.RunID != nil {
			if r, err := t.q.GetRunByID(ctx, store.GetRunByIDParams{CompanyID: companyID, ID: *ref.RunID}); err == nil {
				model = r.Model
			}
		}
		summary := cardClip(in.ChangeSummary, domain.MaxCardChangeSummaryRunes)
		rev := card.SummaryRevision + 1
		if _, err := t.q.InsertStatusCardRevision(ctx, store.InsertStatusCardRevisionParams{ID: domain.NewID(), CompanyID: companyID, CardID: card.ID,
			Revision: rev, Markdown: md, ChangeSummary: summary, Kind: u.Kind, RunID: ref.RunID, Model: model, MentionedTaskIds: mentioned, Now: t.now}); err != nil {
			return mapErr(err, "insert status card revision")
		}
		if err := t.q.PruneStatusCardRevisions(ctx, store.PruneStatusCardRevisionsParams{CardID: card.ID, Below: rev - domain.MaxCardRevisions}); err != nil {
			return fmt.Errorf("prune status card revisions: %w", err)
		}
		if _, err := t.q.FinishStatusCardUpdate(ctx, store.FinishStatusCardUpdateParams{ID: u.ID, Status: string(domain.CardUpdateOK),
			ChangeSummary: summary, Now: &t.now}); err != nil {
			return mapErr(err, "finish status card update")
		}
		kind := u.Kind
		card.Summary, card.SummaryRevision, card.SummaryQueryVersion = md, rev, u.QueryVersion
		if kind == string(domain.CardUpdateFull) {
			card.IncrementalsSinceFull = 0
		} else {
			card.IncrementalsSinceFull++
		}
		card.LastUpdateKind, card.LastGeneratedAt, card.LastModel = &kind, &t.now, model
		card.Fingerprint, card.FingerprintAt, card.MentionedTaskIds = u.Fingerprint, &t.now, mentioned
		card.PendingChangeCount, card.PendingChangeHash = 0, ""
		card.State, card.FailureReason = string(domain.CardActive), ""
		card.GeneratingTaskID, card.GeneratingSince = nil, nil
		next := nextCardPoll(policyFromRaw(card.RefreshPolicy), card, t.now)
		card.NextEvalAt = &next
		if card, err = t.saveCard(card); err != nil {
			return err
		}
		if err := t.activity(actor, companyID, "status_card.summary_updated", "status_card", card.ID, map[string]any{
			"revision": rev, "kind": kind, "mentioned": len(mentioned), "change_summary": summary}, ""); err != nil {
			return err
		}
		out, err = s.cardView(ctx, t.q, card, false)
		return err
	})
	if err == nil { // o resultado também vira uma versão do artefato do card (ADR-0048)
		s.publishCardArtifact(ctx, companyID, cardID)
	}
	return out, unknown, err
}

// resolveTaskKeys converte chaves de task (HNK-12) em ids da empresa; as desconhecidas voltam à parte.
func (t *txc) resolveTaskKeys(companyID domain.ID, keys []string) ([]domain.ID, []string, error) {
	var nums []int64
	want := map[int64]string{}
	var unknown []string
	for _, k := range keys {
		_, n, ok := domain.ParseTaskKey(k)
		if !ok {
			if s := strings.TrimSpace(k); s != "" {
				unknown = append(unknown, s)
			}
			continue
		}
		if _, dup := want[n]; !dup {
			nums = append(nums, n)
		}
		want[n] = strings.ToUpper(strings.TrimSpace(k))
	}
	ids := []domain.ID{}
	if len(nums) == 0 {
		return ids, unknown, nil
	}
	rows, err := t.q.StatusCardTasksByNumber(t.ctx, store.StatusCardTasksByNumberParams{CompanyID: companyID, Numbers: nums})
	if err != nil {
		return nil, nil, fmt.Errorf("tasks by number: %w", err)
	}
	found := map[int64]bool{}
	for _, r := range rows {
		if domain.TaskKey(r.TaskPrefix, r.Number) == want[r.Number] {
			ids = append(ids, r.ID)
			found[r.Number] = true
		}
	}
	for _, n := range nums {
		if !found[n] {
			unknown = append(unknown, want[n])
		}
	}
	return ids, unknown, nil
}

// ---------- fim da run ----------

// isStatusCardTask diz se a task é uma task de sistema de card de status (montagem ou atualização).
func (t *txc) isStatusCardTask(taskID domain.ID) (bool, error) {
	ok, err := t.q.IsStatusCardTask(t.ctx, taskID)
	if err != nil {
		return false, fmt.Errorf("is status card task: %w", err)
	}
	return ok, nil
}

// errStatusCardTaskWake é a recusa de acordar à mão uma task de sistema de card de status.
const errStatusCardTaskWake = "esta é uma task de sistema de um card de status: o Hanko a acorda sozinho; use \"Atualizar agora\" no card"

// statusCardRunEnded fecha a rodada quando a run de montagem ou atualização termina (qualquer desfecho): grava
// tokens e custo da run na rodada, fecha a task de sistema e, se a resumidora não gravou nada, deixa o card em
// erro com o motivo.
func (t *txc) statusCardRunEnded(run store.Run, status domain.RunStatus, errMsg string) error {
	if run.TaskID == nil {
		return nil
	}
	peek, err := t.q.GetStatusCardUpdateByTask(t.ctx, *run.TaskID)
	if isNoRows(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get status card update: %w", err)
	}
	// ordem das travas: card antes da rodada (a mesma das tools da resumidora)
	card, err := t.q.GetStatusCardForUpdate(t.ctx, store.GetStatusCardForUpdateParams{CompanyID: peek.CompanyID, ID: peek.CardID})
	if err != nil {
		return mapErr(err, "status card")
	}
	u, err := t.q.GetStatusCardUpdateByTaskForUpdate(t.ctx, *run.TaskID)
	if err != nil {
		return mapErr(err, "status card update")
	}
	if err := t.q.SetStatusCardUpdateRun(t.ctx, store.SetStatusCardUpdateRunParams{ID: u.ID, RunID: &run.ID, InputTokens: run.InputTokens,
		OutputTokens: run.OutputTokens, CostUsd: run.CostUsd, Model: run.Model}); err != nil {
		return fmt.Errorf("set status card update run: %w", err)
	}
	taskTo := domain.TaskDone
	if u.Status == string(domain.CardUpdateRunning) {
		taskTo = domain.TaskCancelled
		reason := cardRunFailure(domain.CardUpdateKind(u.Kind), status, errMsg)
		if _, err := t.q.FinishStatusCardUpdate(t.ctx, store.FinishStatusCardUpdateParams{ID: u.ID, Status: string(domain.CardUpdateFailed),
			Error: reason, ChangeSummary: u.ChangeSummary, Now: &t.now}); err != nil {
			return mapErr(err, "finish status card update")
		}
		if card.GeneratingTaskID != nil && *card.GeneratingTaskID == *run.TaskID {
			card.State, card.FailureReason, card.NextEvalAt = string(domain.CardError), reason, nil
			card.GeneratingTaskID, card.GeneratingSince = nil, nil
			if _, err := t.saveCard(card); err != nil {
				return err
			}
			if err := t.activity(domain.SystemActor, card.CompanyID, "status_card.failed", "status_card", card.ID, map[string]any{
				"reason": reason, "run_id": run.ID, "kind": u.Kind}, ""); err != nil {
				return err
			}
		}
	} else if u.Status == string(domain.CardUpdateFailed) {
		taskTo = domain.TaskCancelled
	}
	if err := t.closeSystemTask(card.CompanyID, *run.TaskID, taskTo); err != nil {
		return err
	}
	if err := t.q.PruneStatusCardUpdates(t.ctx, store.PruneStatusCardUpdatesParams{CardID: card.ID, Keep: domain.MaxCardUpdates}); err != nil {
		return fmt.Errorf("prune status card updates: %w", err)
	}
	return nil
}

func cardRunFailure(kind domain.CardUpdateKind, status domain.RunStatus, errMsg string) string {
	what := "A atualização não terminou"
	tool := "hanko_status_card_set_summary"
	if kind == domain.CardUpdateCompile {
		what, tool = "A montagem não terminou", "hanko_status_card_set_query"
	}
	msg := fmt.Sprintf("%s: a execução acabou (%s) sem chamar %s.", what, status, tool)
	if e := strings.TrimSpace(errMsg); e != "" {
		msg += " Erro: " + cardClip(e, 300)
	}
	return msg + " Rode de novo para tentar outra vez."
}

// ---------- tick ----------

// EvaluateDueStatusCards é o tick periódico (status_card_tick): avalia os cards cuja próxima verificação
// chegou. Um tick por vez na instalação (trava consultiva) e um card por transação, travado sem esperar (um
// card ocupado fica para o próximo tick). Devolve quantos cards foram avaliados.
func (s *Service) EvaluateDueStatusCards(ctx context.Context) (int, error) {
	guard, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin status card tick: %w", err)
	}
	defer rollbackTx(guard)
	ok, err := store.New(guard).TryStatusCardTickLock(ctx)
	if err != nil {
		return 0, fmt.Errorf("status card tick lock: %w", err)
	}
	if !ok {
		return 0, nil
	}
	now := s.Now().UTC()
	due, err := s.read().ListDueStatusCards(ctx, store.ListDueStatusCardsParams{Now: &now, Lim: cardTickBatch})
	if err != nil {
		return 0, fmt.Errorf("due status cards: %w", err)
	}
	n := 0
	var errs []error
	for _, d := range due {
		var job *cardDecisionJob
		err := s.withTx(ctx, func(t *txc) error {
			row, err := t.q.GetStatusCardForUpdateSkipLocked(ctx, store.GetStatusCardForUpdateSkipLockedParams{CompanyID: d.CompanyID, ID: d.ID})
			if isNoRows(err) {
				return nil
			}
			if err != nil {
				return mapErr(err, "status card")
			}
			if row.NextEvalAt == nil || row.NextEvalAt.After(t.now) || row.ArchivedAt != nil {
				return nil
			}
			_, err = t.evaluateCard(row, evalAuto)
			job = t.cardJob
			return err
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("card %s: %w", d.ID, err))
			continue
		}
		n++
		if job != nil { // geração pelo decision model: a chamada HTTP roda fora da transação do card
			s.logCardJob(ctx, job)
		}
	}
	return n, errors.Join(errs...)
}

// ---------- pacote de contexto ----------

// cardContextHeader é a marca que o prompt da run usa para achar o card e a etapa na descrição da task.
func cardContextHeader(c domain.StatusCard, stage string) string {
	return fmt.Sprintf("<!-- hanko:status_card id=%s etapa=%s -->\n", c.ID, stage)
}

func compileContext(c domain.StatusCard, names cardNames) string {
	var b strings.Builder
	b.WriteString(cardContextHeader(c, "montagem"))
	fmt.Fprintf(&b, "# Montagem do card de status: %s\n\n", cardTitleOrDefault(c.Title))
	fmt.Fprintf(&b, "- Card: `%s`\n- Etapa: montagem dos filtros\n", c.ID)
	if c.TitlePinned {
		b.WriteString("- Título fixado pelo humano: não proponha outro.\n")
	} else {
		b.WriteString("- Título: proponha um título curto (até 200 caracteres) no campo title.\n")
	}
	b.WriteString("\n## Interesse (escrito por quem acompanha)\n\n")
	b.WriteString(c.Interest)
	b.WriteString("\n\n")
	if len(c.Queries) > 0 {
		fmt.Fprintf(&b, "## Filtros atuais (versão %d)\n\n", c.QueryVersion)
		for i, q := range c.Queries {
			fmt.Fprintf(&b, "%d. %s\n", i+1, names.describe(q))
		}
		b.WriteString("\n")
	}
	b.WriteString("## O que fazer\n\n")
	b.WriteString("Transforme o interesse em 1 a 10 filtros de task e grave-os com `hanko_status_card_set_query` (card acima). " +
		"Cada filtro combina (E) os campos status, priority, persona (slug), project (slug), goal (slug), text e open; " +
		"uma task entra se casar com qualquer filtro (OU). Use `hanko_persona_list`, `hanko_project_list` e `hanko_goal_list` para achar os slugs.\n")
	return b.String()
}

func updateContext(c domain.StatusCard, names cardNames, kind domain.CardUpdateKind, changes []domain.CardChange, m cardMatch) string {
	var b strings.Builder
	b.WriteString(cardContextHeader(c, "atualizacao"))
	fmt.Fprintf(&b, "# Atualização do card de status: %s\n\n", cardTitleOrDefault(c.Title))
	tipo := "do zero (full): escreva o resumo inteiro a partir das tasks abaixo"
	if kind == domain.CardUpdateIncremental {
		tipo = "incremental: parta do resumo anterior e incorpore as mudanças"
	}
	fmt.Fprintf(&b, "- Card: `%s`\n- Etapa: atualização %s\n- Tipo: %s\n- Filtros: versão %d\n", c.ID, kind, tipo, c.QueryVersion)
	if c.TitlePinned {
		b.WriteString("- Título fixado pelo humano: não proponha outro.\n")
	}
	b.WriteString("\n## Interesse\n\n")
	b.WriteString(clipText(c.Interest, 8<<10))
	b.WriteString("\n\n## Filtros\n\n")
	for i, q := range c.Queries {
		fmt.Fprintf(&b, "%d. %s\n", i+1, names.describe(q))
	}
	fmt.Fprintf(&b, "\n## Mudanças desde a última versão (%d)\n\n", len(changes))
	if len(changes) == 0 {
		b.WriteString("(nenhuma)\n")
	}
	for i, ch := range changes {
		if i >= 100 {
			fmt.Fprintf(&b, "- … e mais %d mudanças\n", len(changes)-i)
			break
		}
		b.WriteString("- " + describeChange(ch) + "\n")
	}
	var prev string
	if kind == domain.CardUpdateIncremental && c.Summary != "" {
		prev = fmt.Sprintf("\n## Resumo anterior (revisão %d)\n\n%s\n", c.SummaryRevision, c.Summary)
	}
	total := len(m.tasks)
	head := fmt.Sprintf("\n## Tasks observadas (%d", total)
	if m.truncated {
		head += fmt.Sprintf(", limitado a %d", domain.MaxCardObservedTasks)
	}
	b.WriteString(head + ")\n\n")
	if total == 0 {
		b.WriteString("(nenhuma task casa com os filtros hoje)\n")
	}
	budget := maxCardContextBytes - b.Len() - len(prev) - 512
	for i, tk := range m.tasks {
		var line strings.Builder
		who := tk.PersonaName
		if who == "" {
			who = "sem responsável"
		}
		fmt.Fprintf(&line, "- %s · %s · %s · %s · %s\n", tk.Key, tk.Status, tk.Priority, who, tk.Title)
		if tk.HumanCommentAt != nil {
			fmt.Fprintf(&line, "  - último comentário humano (%s): %s\n", tk.HumanCommentAt.UTC().Format("2006-01-02 15:04 UTC"),
				strings.Join(strings.Fields(truncateRunes(tk.HumanComment, maxCardCommentExcerpt)), " "))
		}
		if line.Len() > budget {
			fmt.Fprintf(&b, "- … e mais %d tasks (use hanko_task_get para detalhes)\n", total-i)
			break
		}
		budget -= line.Len()
		b.WriteString(line.String())
	}
	b.WriteString(prev)
	b.WriteString("\n## O que fazer\n\nGrave o resumo com `hanko_status_card_set_summary` (card acima), citando em mentioned as chaves das tasks mencionadas.\n")
	return b.String()
}

func describeChange(ch domain.CardChange) string {
	switch ch.ChangeKind {
	case domain.ChangeEntered:
		return fmt.Sprintf("%s entrou no conjunto (status %s)", ch.Key, ch.To)
	case domain.ChangeLeft:
		return fmt.Sprintf("%s saiu do conjunto (estava %s)", ch.Key, ch.From)
	case domain.ChangeStatus:
		return fmt.Sprintf("%s: status %s → %s", ch.Key, ch.From, ch.To)
	case domain.ChangeAssignee:
		from, to := ch.From, ch.To
		if from == "" {
			from = "ninguém"
		}
		if to == "" {
			to = "ninguém"
		}
		return fmt.Sprintf("%s: responsável %s → %s", ch.Key, from, to)
	case domain.ChangeHumanComment:
		return fmt.Sprintf("%s: novo comentário humano", ch.Key)
	}
	return fmt.Sprintf("%s: alterada (título, prioridade ou detalhes)", ch.Key)
}
