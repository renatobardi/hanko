package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"go.opentelemetry.io/otel/attribute"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Coordenação de tasks (ADR-0040): dependências bloqueantes, árvore pai/filho, pausa herdada por subárvore e
// política de revisão por camada. Este arquivo concentra as regras; tasks.go, runs.go e gates.go chamam os ganchos
// (dispatchBlocker, onTaskClosed, resolveReviewPolicy).

// TaskCoord são os campos derivados da coordenação que a view de uma task carrega.
type TaskCoord struct {
	// OpenDependencies conta as dependências ainda abertas; Blocked é o derivado "bloqueada" (OpenDependencies > 0).
	OpenDependencies  int  `json:"open_dependencies"`
	DependenciesTotal int  `json:"dependencies_total"`
	Blocked           bool `json:"blocked"`
	ChildrenTotal     int  `json:"children_total"`
	ChildrenOpen      int  `json:"children_open"`
	// Hold é a pausa efetiva (a própria ou herdada de um ancestral); nil = a task não está pausada.
	Hold *domain.TaskHold `json:"hold,omitempty"`
}

// DependencyView é uma ponta de dependência com o estado dela.
type DependencyView struct {
	domain.TaskRef
	PersonaName string `json:"persona_name,omitempty"`
	// Closed: a task já está done ou cancelled (não bloqueia mais).
	Closed bool `json:"closed"`
}

// TaskRelations é tudo o que liga uma task às outras: árvore, dependências e política de revisão.
type TaskRelations struct {
	// Ancestors vai da raiz até o pai imediato.
	Ancestors  []domain.TaskRef `json:"ancestors"`
	Children   []DependencyView `json:"children"`
	DependsOn  []DependencyView `json:"depends_on"`
	Dependents []DependencyView `json:"dependents"`
}

// ReviewPolicyView é a política de revisão de uma task por camada e o resultado da precedência.
type ReviewPolicyView struct {
	Task      domain.ReviewOverride `json:"task"`
	Project   domain.ReviewOverride `json:"project"`
	Persona   domain.PersonaReview  `json:"persona"`
	Effective domain.ReviewPolicy   `json:"effective"`
}

// ---------- leitura: contadores e pausa ----------

// enrichTasks preenche os campos derivados (dependências, filhos, pausa) das views, em duas consultas.
func enrichTasks(ctx context.Context, q *store.Queries, companyID domain.ID, views []*TaskView) error {
	if len(views) == 0 {
		return nil
	}
	ids := make([]domain.ID, len(views))
	for i, v := range views {
		ids[i] = v.ID
	}
	rows, err := q.ListTaskCoordination(ctx, store.ListTaskCoordinationParams{CompanyID: companyID, Ids: ids})
	if err != nil {
		return fmt.Errorf("task coordination: %w", err)
	}
	byID := make(map[domain.ID]store.ListTaskCoordinationRow, len(rows))
	for _, r := range rows {
		byID[r.TaskID] = r
	}
	holds, err := q.ListEffectiveHolds(ctx, store.ListEffectiveHoldsParams{CompanyID: companyID, Ids: ids})
	if err != nil {
		return fmt.Errorf("effective holds: %w", err)
	}
	holdBy := make(map[domain.ID]store.ListEffectiveHoldsRow, len(holds))
	for _, h := range holds {
		holdBy[h.TaskID] = h
	}
	for _, v := range views {
		if r, ok := byID[v.ID]; ok {
			v.OpenDependencies, v.DependenciesTotal = int(r.OpenDependencies), int(r.DependenciesTotal)
			v.Blocked = domain.Blocked(v.OpenDependencies)
			v.ChildrenTotal, v.ChildrenOpen = int(r.ChildrenTotal), int(r.ChildrenOpen)
		}
		if h, ok := holdBy[v.ID]; ok {
			v.Hold = holdFromRow(h, v.ID)
		}
	}
	return nil
}

func holdFromRow(h store.ListEffectiveHoldsRow, taskID domain.ID) *domain.TaskHold {
	return &domain.TaskHold{Reason: h.Reason, By: actorOf(h.HeldByType, h.HeldByID), At: h.HeldAt,
		OriginID: h.HoldTaskID, OriginKey: domain.TaskKey(h.TaskPrefix, h.HoldNumber), Inherited: h.HoldTaskID != taskID}
}

// dispatchBlocker diz por que uma run não pode abrir agora para a task por coordenação: pausada (ErrTaskHeld) ou
// com dependência aberta (ErrTaskBlocked). nil = livre. A pausa vem antes: é a decisão humana mais forte.
func (t *txc) dispatchBlocker(companyID, taskID domain.ID) error {
	holds, err := t.q.ListEffectiveHolds(t.ctx, store.ListEffectiveHoldsParams{CompanyID: companyID, Ids: []domain.ID{taskID}})
	if err != nil {
		return fmt.Errorf("effective hold: %w", err)
	}
	if len(holds) > 0 {
		h := holdFromRow(holds[0], taskID)
		return fmt.Errorf("%w: %s (%s)", domain.ErrTaskHeld, h.Reason, h.OriginKey)
	}
	open, err := t.q.TaskHasOpenDependencies(t.ctx, taskID)
	if err != nil {
		return fmt.Errorf("open dependencies: %w", err)
	}
	if open {
		return domain.ErrTaskBlocked
	}
	return nil
}

// recordHeldWake guarda um wake recusado por pausa, para o "retomar" reenviá-lo.
func (t *txc) recordHeldWake(companyID, taskID, personaID domain.ID, trigger domain.RunTrigger) error {
	if err := t.q.RecordHeldWake(t.ctx, store.RecordHeldWakeParams{TaskID: taskID, CompanyID: companyID, PersonaID: personaID,
		Trigger: string(trigger), Now: t.now}); err != nil {
		return fmt.Errorf("record held wake: %w", err)
	}
	return nil
}

// ---------- relações da task ----------

func refOf(id domain.ID, number int64, prefix, title, status string, persona *domain.ID) domain.TaskRef {
	return domain.TaskRef{ID: id, Key: domain.TaskKey(prefix, number), Title: title, Status: domain.TaskStatus(status), PersonaID: persona}
}

func depView(ref domain.TaskRef, personaName string) DependencyView {
	return DependencyView{TaskRef: ref, PersonaName: personaName, Closed: !ref.Status.Open()}
}

// loadRelations monta árvore e dependências de uma task.
func loadRelations(ctx context.Context, q *store.Queries, companyID, taskID domain.ID) (TaskRelations, error) {
	rel := TaskRelations{Ancestors: []domain.TaskRef{}, Children: []DependencyView{}, DependsOn: []DependencyView{}, Dependents: []DependencyView{}}
	anc, err := q.ListTaskAncestors(ctx, store.ListTaskAncestorsParams{CompanyID: companyID, TaskID: taskID})
	if err != nil {
		return rel, fmt.Errorf("task ancestors: %w", err)
	}
	for _, a := range anc {
		rel.Ancestors = append(rel.Ancestors, refOf(a.ID, a.Number, a.TaskPrefix, a.Title, a.Status, a.PersonaID))
	}
	kids, err := q.ListTaskChildren(ctx, store.ListTaskChildrenParams{CompanyID: companyID, ParentID: &taskID})
	if err != nil {
		return rel, fmt.Errorf("task children: %w", err)
	}
	for _, k := range kids {
		rel.Children = append(rel.Children, depView(refOf(k.ID, k.Number, k.TaskPrefix, k.Title, k.Status, k.PersonaID), k.PersonaName))
	}
	deps, err := q.ListTaskDependencies(ctx, taskID)
	if err != nil {
		return rel, fmt.Errorf("task dependencies: %w", err)
	}
	for _, d := range deps {
		rel.DependsOn = append(rel.DependsOn, depView(refOf(d.ID, d.Number, d.TaskPrefix, d.Title, d.Status, d.PersonaID), d.PersonaName))
	}
	dts, err := q.ListTaskDependents(ctx, taskID)
	if err != nil {
		return rel, fmt.Errorf("task dependents: %w", err)
	}
	for _, d := range dts {
		rel.Dependents = append(rel.Dependents, depView(refOf(d.ID, d.Number, d.TaskPrefix, d.Title, d.Status, d.PersonaID), d.PersonaName))
	}
	return rel, nil
}

// TaskRelations devolve a árvore (ancestrais e filhos) e as dependências de uma task.
func (s *Service) TaskRelations(ctx context.Context, companyID, taskID domain.ID) (TaskRelations, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return TaskRelations{}, err
	}
	q := s.read()
	if _, err := q.GetTaskByID(ctx, store.GetTaskByIDParams{CompanyID: companyID, ID: taskID}); err != nil {
		return TaskRelations{}, mapErr(err, "get task")
	}
	return loadRelations(ctx, q, companyID, taskID)
}

// ---------- dependências ----------

// reachableEdges carrega as arestas alcançáveis a partir de start.
func (t *txc) reachableEdges(start domain.ID) (domain.DepEdges, error) {
	rows, err := t.q.ListReachableDependencyEdges(t.ctx, start)
	if err != nil {
		return nil, fmt.Errorf("reachable dependency edges: %w", err)
	}
	e := domain.DepEdges{}
	for _, r := range rows {
		e.Add(r.TaskID, r.DependsOnID)
	}
	return e, nil
}

// cyclePath devolve a cadeia depends → … → task dentro de edges (BFS), para a mensagem do erro.
func cyclePath(edges domain.DepEdges, from, to domain.ID) []domain.ID {
	prev := map[domain.ID]domain.ID{}
	seen := map[domain.ID]bool{from: true}
	queue := []domain.ID{from}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == to {
			var rev []domain.ID
			for n := to; ; n = prev[n] {
				rev = append(rev, n)
				if n == from {
					break
				}
			}
			out := make([]domain.ID, len(rev))
			for i, n := range rev {
				out[len(rev)-1-i] = n
			}
			return out
		}
		for _, nx := range edges[cur] {
			if !seen[nx] {
				seen[nx] = true
				prev[nx] = cur
				queue = append(queue, nx)
			}
		}
	}
	return nil
}

func (t *txc) keyOf(companyID, id domain.ID) string {
	r, err := t.q.GetTaskView(t.ctx, store.GetTaskViewParams{CompanyID: companyID, ID: id})
	if err != nil {
		return id.String()
	}
	return domain.TaskKey(r.TaskPrefix, r.Task.Number)
}

// addDependency grava taskID → dependsOnID dentro da transação do chamador. Devolve false quando a dependência já
// existia (idempotente). O ator só é checado quanto à autoria do fato; a permissão é do chamador.
func (t *txc) addDependency(actor domain.Actor, companyID, taskID, dependsOnID domain.ID) (bool, error) {
	if taskID == dependsOnID {
		return false, domain.Invalid("depends_on", "uma task não depende de si mesma")
	}
	if err := t.q.LockTaskDependencies(t.ctx, companyID); err != nil {
		return false, fmt.Errorf("lock task dependencies: %w", err)
	}
	if _, err := t.q.GetTaskByID(t.ctx, store.GetTaskByIDParams{CompanyID: companyID, ID: taskID}); err != nil {
		return false, mapErr(err, "get task")
	}
	if _, err := t.q.GetTaskByID(t.ctx, store.GetTaskByIDParams{CompanyID: companyID, ID: dependsOnID}); err != nil {
		if isNoRows(err) {
			return false, domain.Invalid("depends_on", "task not found in company")
		}
		return false, mapErr(err, "get task")
	}
	n, err := t.q.CountTaskDependencies(t.ctx, taskID)
	if err != nil {
		return false, fmt.Errorf("count dependencies: %w", err)
	}
	if int(n) >= domain.MaxTaskDependencies {
		return false, domain.Invalid("depends_on", fmt.Sprintf("no máximo %d dependências por task", domain.MaxTaskDependencies))
	}
	edges, err := t.reachableEdges(dependsOnID)
	if err != nil {
		return false, err
	}
	if domain.WouldCycle(edges, taskID, dependsOnID) {
		keys := []string{t.keyOf(companyID, taskID)}
		for _, id := range cyclePath(edges, dependsOnID, taskID) {
			keys = append(keys, t.keyOf(companyID, id))
		}
		return false, fmt.Errorf("%s: %w", domain.CycleMessage(keys), domain.ErrDependencyCycle)
	}
	inserted, err := t.q.AddTaskDependency(t.ctx, store.AddTaskDependencyParams{CompanyID: companyID, TaskID: taskID, DependsOnID: dependsOnID,
		CreatedByType: string(actor.Type), CreatedByID: actor.ID, Now: t.now})
	if err != nil {
		if constraintOf(err) == "task_dependencies_no_cycle" { // corrida: outro commit fechou o ciclo antes do lock
			return false, fmt.Errorf("%w", domain.ErrDependencyCycle)
		}
		return false, mapErr(err, "add dependency")
	}
	if inserted == 0 {
		return false, nil
	}
	countCoord(t.ctx, "dependency", "added")
	return true, t.activity(actor, companyID, "task.dependency_added", "task", taskID, map[string]any{
		"depends_on": dependsOnID, "depends_on_key": t.keyOf(companyID, dependsOnID)}, "")
}

// constraintOf devolve o nome da constraint de um erro do Postgres (vazio se não for).
func constraintOf(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.ConstraintName
	}
	return ""
}

// checkTaskEdit aplica a regra de autoria das mudanças em uma task: a persona só mexe nas dela e nas de quem está
// abaixo no organograma.
func (t *txc) checkTaskEdit(actor domain.Actor, companyID domain.ID, task store.Task) error {
	return t.checkTaskOwnership(actor, companyID, task.PersonaID)
}

// AddTaskDependency faz taskID depender de dependsOnID. Sem ciclo (409 com a cadeia), mesma empresa, no máximo 50.
// Repetir a mesma dependência não faz nada.
func (s *Service) AddTaskDependency(ctx context.Context, actor domain.Actor, companyID, taskID, dependsOnID domain.ID) (TaskView, error) {
	ctx, endSpan := coordSpan(ctx, "service.add_task_dependency", attribute.String("company_id", companyID.String()), attribute.String("task_id", taskID.String()))
	defer endSpan()
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return TaskView{}, err
	}
	var out TaskView
	err := s.withTx(ctx, func(t *txc) error {
		// O lock do grafo vem antes do lock da linha da task: duas escritas opostas (A→B e B→A) travariam uma na outra
		// se cada uma segurasse a própria task e esperasse o lock do grafo.
		if err := t.q.LockTaskDependencies(ctx, companyID); err != nil {
			return fmt.Errorf("lock task dependencies: %w", err)
		}
		task, err := t.q.GetTaskByIDForUpdate(ctx, store.GetTaskByIDForUpdateParams{CompanyID: companyID, ID: taskID})
		if err != nil {
			return mapErr(err, "get task")
		}
		if err := t.checkTaskEdit(actor, companyID, task); err != nil {
			return err
		}
		if _, err := t.addDependency(actor, companyID, taskID, dependsOnID); err != nil {
			return err
		}
		if out, err = loadTaskView(ctx, t.q, companyID, taskID); err != nil {
			return err
		}
		t.publish(companyTopic(companyID), "task.updated", out)
		return nil
	})
	return out, err
}

// RemoveTaskDependency desfaz a dependência. Se era a última aberta, a task é liberada e o responsável acordado.
func (s *Service) RemoveTaskDependency(ctx context.Context, actor domain.Actor, companyID, taskID, dependsOnID domain.ID) (TaskView, error) {
	ctx, endSpan := coordSpan(ctx, "service.remove_task_dependency", attribute.String("company_id", companyID.String()), attribute.String("task_id", taskID.String()))
	defer endSpan()
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return TaskView{}, err
	}
	var out TaskView
	err := s.withTx(ctx, func(t *txc) error {
		if err := t.q.LockTaskDependencies(ctx, companyID); err != nil { // antes do lock da task (ver AddTaskDependency)
			return fmt.Errorf("lock task dependencies: %w", err)
		}
		task, err := t.q.GetTaskByIDForUpdate(ctx, store.GetTaskByIDForUpdateParams{CompanyID: companyID, ID: taskID})
		if err != nil {
			return mapErr(err, "get task")
		}
		if err := t.checkTaskEdit(actor, companyID, task); err != nil {
			return err
		}
		dep, err := t.q.GetTaskByID(ctx, store.GetTaskByIDParams{CompanyID: companyID, ID: dependsOnID})
		if err != nil {
			return mapErr(err, "get dependency")
		}
		n, err := t.q.RemoveTaskDependency(ctx, store.RemoveTaskDependencyParams{CompanyID: companyID, TaskID: taskID, DependsOnID: dependsOnID})
		if err != nil {
			return fmt.Errorf("remove dependency: %w", err)
		}
		if n == 0 {
			return notFound("dependency")
		}
		countCoord(ctx, "dependency", "removed")
		if err := t.activity(actor, companyID, "task.dependency_removed", "task", taskID, map[string]any{
			"depends_on": dependsOnID, "depends_on_key": t.keyOf(companyID, dependsOnID)}, ""); err != nil {
			return err
		}
		if domain.TaskStatus(dep.Status).Open() && domain.TaskStatus(task.Status).Open() {
			open, err := t.q.TaskHasOpenDependencies(ctx, taskID)
			if err != nil {
				return fmt.Errorf("open dependencies: %w", err)
			}
			if !open {
				if err := t.unblock(companyID, task.ID, task.PersonaID, domain.TaskStatus(task.Status), "removed", 0); err != nil {
					return err
				}
			}
		}
		if out, err = loadTaskView(ctx, t.q, companyID, taskID); err != nil {
			return err
		}
		t.publish(companyTopic(companyID), "task.updated", out)
		return nil
	})
	return out, err
}

// unblock registra que uma task ficou livre e acorda o responsável. by diz o que liberou: a chave da task que
// fechou, ou "removed" quando a última dependência aberta foi desfeita.
func (t *txc) unblock(companyID, taskID domain.ID, personaID *domain.ID, status domain.TaskStatus, by string, cancelled int) error {
	data := map[string]any{"by": by}
	if cancelled > 0 {
		data["cancelled_dependencies"] = cancelled
	}
	if err := t.activity(domain.SystemActor, companyID, "task.unblocked", "task", taskID, data, ""); err != nil {
		return err
	}
	countCoord(t.ctx, "dependency", "unblocked")
	if err := t.wake(companyID, taskID, personaID, status, domain.TriggerAssign, true); err != nil {
		return err
	}
	if tv, err := loadTaskView(t.ctx, t.q, companyID, taskID); err == nil {
		t.publish(companyTopic(companyID), "task.updated", tv)
	}
	return nil
}

// ListTaskDependencies lista as dependências de uma task e quem depende dela.
func (s *Service) ListTaskDependencies(ctx context.Context, companyID, taskID domain.ID) (depends, dependents []DependencyView, err error) {
	rel, err := s.TaskRelations(ctx, companyID, taskID)
	if err != nil {
		return nil, nil, err
	}
	return rel.DependsOn, rel.Dependents, nil
}

// ---------- fechamento: o que a coordenação dispara ----------

// onTaskClosed roda quando uma task acaba de fechar (done ou cancelled), na transação do fechamento: libera quem
// dependia só dela e acorda o pai quando o último filho fecha. A cascata (A → B → C) vem do fato de cada
// fechamento repetir esta regra.
func (t *txc) onTaskClosed(companyID domain.ID, closed store.Task) error {
	key := t.keyOf(companyID, closed.ID)
	rel, err := t.q.ListDependentsToRelease(t.ctx, store.ListDependentsToReleaseParams{CompanyID: companyID, DependsOnID: closed.ID})
	if err != nil {
		return fmt.Errorf("dependents to release: %w", err)
	}
	for _, d := range rel {
		if err := t.unblock(companyID, d.ID, d.PersonaID, domain.TaskStatus(d.Status), key, int(d.CancelledDeps)); err != nil {
			return err
		}
	}
	if closed.ParentID == nil {
		return nil
	}
	open, err := t.q.CountOpenChildren(t.ctx, closed.ParentID)
	if err != nil {
		return fmt.Errorf("count open children: %w", err)
	}
	if open > 0 {
		return nil
	}
	parent, err := t.q.GetTaskByID(t.ctx, store.GetTaskByIDParams{CompanyID: companyID, ID: *closed.ParentID})
	if err != nil {
		return mapErr(err, "get parent")
	}
	if !domain.TaskStatus(parent.Status).Open() {
		return nil
	}
	if err := t.activity(domain.SystemActor, companyID, "task.children_done", "task", parent.ID, map[string]any{"last": key}, ""); err != nil {
		return err
	}
	countCoord(t.ctx, "dependency", "children_done")
	return t.wake(companyID, parent.ID, parent.PersonaID, domain.TaskStatus(parent.Status), domain.TriggerAssign, true)
}

// checkCloseWithChildren aplica "pai não conclui com filho aberto": devolve ErrChildrenOpen com a lista, a menos
// que um humano force com motivo. overridden diz que o override foi usado (o chamador grava o fato).
func (t *txc) checkCloseWithChildren(actor domain.Actor, companyID, taskID domain.ID, force bool, reason string) (overridden bool, err error) {
	open, err := t.q.ListOpenChildren(t.ctx, store.ListOpenChildrenParams{CompanyID: companyID, ParentID: &taskID})
	if err != nil {
		return false, fmt.Errorf("open children: %w", err)
	}
	if len(open) == 0 {
		return false, nil
	}
	keys := make([]string, len(open))
	for i, c := range open {
		keys[i] = domain.TaskKey(c.TaskPrefix, c.Number)
	}
	if !force {
		return false, fmt.Errorf("%s: %w", strings.Join(keys, ", "), domain.ErrChildrenOpen)
	}
	if actor.Type != domain.ActorHuman {
		return false, fmt.Errorf("only a human can close a task with open subtasks: %w", domain.ErrNotAllowed)
	}
	reason = strings.TrimSpace(reason)
	if n := utf8.RuneCountInString(reason); n < 1 || n > domain.MaxOverrideReason {
		return false, domain.Invalid("override_reason", fmt.Sprintf("o motivo do override tem de 1 a %d caracteres", domain.MaxOverrideReason))
	}
	countCoord(t.ctx, "dependency", "close_override")
	return true, nil
}

// ---------- pausa ----------

// HoldTask pausa a task e, por herança, toda a subárvore: nenhuma run abre enquanto durar (o despacho recusa), as
// runs ainda na fila são canceladas e uma run em andamento termina sozinha. Só humano pausa.
func (s *Service) HoldTask(ctx context.Context, actor domain.Actor, companyID, taskID domain.ID, reason string) (TaskView, error) {
	ctx, endSpan := coordSpan(ctx, "service.hold_task", attribute.String("company_id", companyID.String()), attribute.String("task_id", taskID.String()))
	defer endSpan()
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return TaskView{}, err
	}
	if actor.Type == domain.ActorPersona {
		return TaskView{}, fmt.Errorf("a persona não pausa tasks: %w", domain.ErrNotAllowed)
	}
	reason = strings.TrimSpace(reason)
	if n := utf8.RuneCountInString(reason); n < 1 || n > domain.MaxHoldReason {
		return TaskView{}, domain.Invalid("reason", fmt.Sprintf("o motivo da pausa tem de 1 a %d caracteres", domain.MaxHoldReason))
	}
	var out TaskView
	err := s.withTx(ctx, func(t *txc) error {
		if err := t.q.LockTaskHoldsExclusive(ctx, companyID); err != nil {
			return fmt.Errorf("lock task holds: %w", err)
		}
		task, err := t.q.GetTaskByIDForUpdate(ctx, store.GetTaskByIDForUpdateParams{CompanyID: companyID, ID: taskID})
		if err != nil {
			return mapErr(err, "get task")
		}
		if !domain.TaskStatus(task.Status).Open() {
			return domain.Invalid("task", "a task já está fechada")
		}
		if system, err := t.isSystemTask(task.ID); err != nil {
			return err
		} else if system {
			return domain.Invalid("task", errSystemTaskWake)
		}
		if _, err := t.q.GetTaskHold(ctx, task.ID); err == nil {
			return fmt.Errorf("a task já está pausada: %w", domain.ErrConflict)
		} else if !isNoRows(err) {
			return fmt.Errorf("get hold: %w", err)
		}
		if err := t.q.UpsertTaskHold(ctx, store.UpsertTaskHoldParams{TaskID: task.ID, CompanyID: companyID, Reason: reason,
			HeldByType: string(actor.Type), HeldByID: actor.ID, Now: t.now}); err != nil {
			return fmt.Errorf("hold task: %w", err)
		}
		sub, err := t.q.ListTaskSubtree(ctx, store.ListTaskSubtreeParams{RootID: task.ID, CompanyID: companyID})
		if err != nil {
			return fmt.Errorf("task subtree: %w", err)
		}
		queued, err := t.q.ListQueuedRunsInSubtree(ctx, store.ListQueuedRunsInSubtreeParams{RootID: task.ID, CompanyID: companyID})
		if err != nil {
			return fmt.Errorf("queued runs in subtree: %w", err)
		}
		for _, id := range queued {
			cur, err := t.q.GetRunByIDForUpdate(ctx, store.GetRunByIDForUpdateParams{CompanyID: companyID, ID: id})
			if err != nil {
				return mapErr(err, "get run")
			}
			if domain.RunStatus(cur.Status) != domain.RunQueued {
				continue
			}
			if err := t.cancelRun(actor, companyID, cur, map[string]any{"reason": "task_held", "held_task_id": task.ID}); err != nil {
				return err
			}
		}
		if err := t.activity(actor, companyID, "task.held", "task", task.ID, map[string]any{
			"reason": reason, "descendants": len(sub) - 1, "queued_runs_cancelled": len(queued)}, ""); err != nil {
			return err
		}
		countCoord(ctx, "hold", "held")
		if out, err = loadTaskView(ctx, t.q, companyID, taskID); err != nil {
			return err
		}
		t.publish(companyTopic(companyID), "task.updated", out)
		return nil
	})
	return out, err
}

// ResumeTask apaga a pausa da task e reenvia os wakes que foram recusados enquanto ela durou, nas tasks da
// subárvore que não continuam pausadas por outra raiz.
func (s *Service) ResumeTask(ctx context.Context, actor domain.Actor, companyID, taskID domain.ID) (TaskView, error) {
	ctx, endSpan := coordSpan(ctx, "service.resume_task", attribute.String("company_id", companyID.String()), attribute.String("task_id", taskID.String()))
	defer endSpan()
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return TaskView{}, err
	}
	if actor.Type == domain.ActorPersona {
		return TaskView{}, fmt.Errorf("a persona não retoma tasks: %w", domain.ErrNotAllowed)
	}
	var out TaskView
	err := s.withTx(ctx, func(t *txc) error {
		if err := t.q.LockTaskHoldsExclusive(ctx, companyID); err != nil {
			return fmt.Errorf("lock task holds: %w", err)
		}
		task, err := t.q.GetTaskByIDForUpdate(ctx, store.GetTaskByIDForUpdateParams{CompanyID: companyID, ID: taskID})
		if err != nil {
			return mapErr(err, "get task")
		}
		n, err := t.q.DeleteTaskHold(ctx, store.DeleteTaskHoldParams{CompanyID: companyID, TaskID: task.ID})
		if err != nil {
			return fmt.Errorf("resume task: %w", err)
		}
		if n == 0 {
			if holds, herr := t.q.ListEffectiveHolds(ctx, store.ListEffectiveHoldsParams{CompanyID: companyID, Ids: []domain.ID{task.ID}}); herr == nil && len(holds) > 0 {
				return domain.Invalid("task", fmt.Sprintf("a pausa vem de %s: retome essa task", domain.TaskKey(holds[0].TaskPrefix, holds[0].HoldNumber)))
			}
			return domain.Invalid("task", "a task não está pausada")
		}
		pending, err := t.q.ListHeldWakesInSubtree(ctx, store.ListHeldWakesInSubtreeParams{RootID: task.ID, CompanyID: companyID})
		if err != nil {
			return fmt.Errorf("held wakes: %w", err)
		}
		woken := 0
		for _, w := range pending {
			// ainda pausada por outra raiz? o wake fica guardado
			if holds, err := t.q.ListEffectiveHolds(ctx, store.ListEffectiveHoldsParams{CompanyID: companyID, Ids: []domain.ID{w.TaskID}}); err != nil {
				return fmt.Errorf("effective hold: %w", err)
			} else if len(holds) > 0 {
				continue
			}
			if err := t.q.DeleteHeldWake(ctx, w.TaskID); err != nil {
				return fmt.Errorf("delete held wake: %w", err)
			}
			wt, err := t.q.GetTaskByID(ctx, store.GetTaskByIDParams{CompanyID: companyID, ID: w.TaskID})
			if err != nil {
				return mapErr(err, "get task")
			}
			trigger := domain.RunTrigger(w.Trigger)
			before := t.wakeCount
			if err := t.wake(companyID, wt.ID, &w.PersonaID, domain.TaskStatus(wt.Status), trigger, trigger != domain.TriggerComment); err != nil {
				return err
			}
			if t.wakeCount > before {
				woken++
			}
		}
		if err := t.activity(actor, companyID, "task.resumed", "task", task.ID, map[string]any{"woken": woken}, ""); err != nil {
			return err
		}
		countCoord(ctx, "hold", "resumed")
		if out, err = loadTaskView(ctx, t.q, companyID, taskID); err != nil {
			return err
		}
		t.publish(companyTopic(companyID), "task.updated", out)
		return nil
	})
	return out, err
}

// HeldTask é uma raiz de pausa para a Caixa.
type HeldTask struct {
	Task TaskView
	Hold domain.TaskHold
}

// ListHeldTasks devolve as raízes de pausa da empresa (as pausas gravadas em uma task aberta), mais novas primeiro.
func (s *Service) ListHeldTasks(ctx context.Context, companyID domain.ID) ([]HeldTask, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	q := s.read()
	rows, err := q.ListHeldRoots(ctx, store.ListHeldRootsParams{CompanyID: companyID, Lim: 100})
	if err != nil {
		return nil, fmt.Errorf("list held tasks: %w", err)
	}
	out := make([]HeldTask, len(rows))
	views := make([]*TaskView, len(rows))
	for i, r := range rows {
		tv := taskView(r.Task, r.TaskPrefix, r.PersonaName, "", nil)
		out[i] = HeldTask{Task: tv, Hold: domain.TaskHold{Reason: r.Reason, By: actorOf(r.HeldByType, r.HeldByID), At: r.HeldAt,
			OriginID: r.Task.ID, OriginKey: tv.Key}}
		views[i] = &out[i].Task
	}
	if err := enrichTasks(ctx, q, companyID, views); err != nil {
		return nil, err
	}
	return out, nil
}

// ---------- política de revisão ----------

func overrideFromTaskRow(r store.TaskReviewOverride) domain.ReviewOverride {
	return domain.ReviewOverride{RequireReview: boolPtr(r.RequireReview), ReviewerMode: domain.ReviewerMode(r.ReviewerMode),
		ReviewerPersonaID: r.ReviewerPersonaID, ReviewerAgent: r.ReviewerAgent, ReviewerModel: r.ReviewerModel}
}

func overrideFromProjectRow(r store.ProjectReviewPolicy) domain.ReviewOverride {
	return domain.ReviewOverride{RequireReview: boolPtr(r.RequireReview), ReviewerMode: domain.ReviewerMode(r.ReviewerMode),
		ReviewerPersonaID: r.ReviewerPersonaID, ReviewerAgent: r.ReviewerAgent, ReviewerModel: r.ReviewerModel}
}

func personaReviewOf(p store.Persona) domain.PersonaReview {
	return domain.PersonaReview{RequireReview: p.RequireReview, ReviewerPersonaID: p.ReviewerPersonaID,
		ReviewerAgent: p.ReviewerAgent, ReviewerModel: p.ReviewerModel}
}

// reviewLayers lê as sobreposições de task e de projeto.
func reviewLayers(ctx context.Context, q *store.Queries, taskID domain.ID, projectID *domain.ID) (task, project domain.ReviewOverride, err error) {
	if r, err := q.GetTaskReviewOverride(ctx, taskID); err == nil {
		task = overrideFromTaskRow(r)
	} else if !isNoRows(err) {
		return task, project, fmt.Errorf("task review override: %w", err)
	}
	if projectID != nil {
		if r, err := q.GetProjectReviewPolicy(ctx, *projectID); err == nil {
			project = overrideFromProjectRow(r)
		} else if !isNoRows(err) {
			return task, project, fmt.Errorf("project review policy: %w", err)
		}
	}
	return task, project, nil
}

// resolveReviewPolicy aplica task > projeto > persona para a task e a persona que trabalha nela.
func (t *txc) resolveReviewPolicy(task store.Task, persona store.Persona) (domain.ReviewPolicy, error) {
	to, po, err := reviewLayers(t.ctx, t.q, task.ID, task.ProjectID)
	if err != nil {
		return domain.ReviewPolicy{}, err
	}
	return domain.ResolveReviewPolicy(to, po, personaReviewOf(persona)), nil
}

// reviewRequiredFor diz se o fechamento da task por este ator passa por revisão: só persona é revisada, e a
// obrigatoriedade vem da camada que vence (task > projeto > persona).
func (t *txc) reviewRequiredFor(actor domain.Actor, companyID domain.ID, task store.Task) (bool, error) {
	if actor.Type != domain.ActorPersona || actor.ID == nil {
		return false, nil
	}
	p, err := t.q.GetPersonaByID(t.ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: *actor.ID})
	if isNoRows(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get actor persona: %w", err)
	}
	pol, err := t.resolveReviewPolicy(task, p)
	if err != nil {
		return false, err
	}
	// a origem da política que valeu no fechamento fica na run da autora (mesmo quando a revisão é dispensada)
	if run, rerr := t.q.ActiveRunForTask(t.ctx, task.ID); rerr == nil && run.PersonaID == p.ID {
		if err := t.recordPolicyOrigin(companyID, run.ID, pol); err != nil {
			return false, err
		}
	} else if rerr != nil && !isNoRows(rerr) {
		return false, fmt.Errorf("active run: %w", rerr)
	}
	return pol.Required, nil
}

// recordPolicyOrigin grava na run a origem da política que valeu nela.
func (t *txc) recordPolicyOrigin(companyID, runID domain.ID, pol domain.ReviewPolicy) error {
	if err := t.q.UpsertRunPolicyOrigin(t.ctx, store.UpsertRunPolicyOriginParams{RunID: runID, CompanyID: companyID,
		ReviewRequired: pol.Required, ReviewRequiredSource: string(pol.RequiredSource), ReviewerSource: string(pol.ReviewerSource), Now: t.now}); err != nil {
		return fmt.Errorf("record run policy origin: %w", err)
	}
	return nil
}

// RunPolicyOrigin devolve a origem da política de revisão gravada na run (nil se a run não registrou).
func (s *Service) RunPolicyOrigin(ctx context.Context, companyID, runID domain.ID) (*domain.RunPolicyOrigin, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	r, err := s.read().GetRunPolicyOrigin(ctx, runID)
	if isNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("run policy origin: %w", err)
	}
	if r.CompanyID != companyID {
		return nil, nil
	}
	return &domain.RunPolicyOrigin{ReviewRequired: r.ReviewRequired, ReviewRequiredSource: domain.ReviewLayer(r.ReviewRequiredSource),
		ReviewerSource: domain.ReviewLayer(r.ReviewerSource)}, nil
}

// TaskReviewPolicy mostra a política de revisão da task por camada e o resultado da precedência. A camada da
// persona é a do responsável atual (vazia sem responsável).
func (s *Service) TaskReviewPolicy(ctx context.Context, companyID, taskID domain.ID) (ReviewPolicyView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return ReviewPolicyView{}, err
	}
	q := s.read()
	task, err := q.GetTaskByID(ctx, store.GetTaskByIDParams{CompanyID: companyID, ID: taskID})
	if err != nil {
		return ReviewPolicyView{}, mapErr(err, "get task")
	}
	to, po, err := reviewLayers(ctx, q, task.ID, task.ProjectID)
	if err != nil {
		return ReviewPolicyView{}, err
	}
	var pr domain.PersonaReview
	if task.PersonaID != nil {
		p, err := q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: *task.PersonaID})
		if err != nil {
			return ReviewPolicyView{}, mapErr(err, "get persona")
		}
		pr = personaReviewOf(p)
	}
	return ReviewPolicyView{Task: to, Project: po, Persona: pr, Effective: domain.ResolveReviewPolicy(to, po, pr)}, nil
}

// checkReviewOverride valida a sobreposição e as personas citadas.
func (t *txc) checkReviewOverride(companyID domain.ID, o domain.ReviewOverride) (domain.ReviewOverride, error) {
	if o.ReviewerMode == "" {
		o.ReviewerMode = domain.ReviewerInherit
	}
	o.ReviewerModel = strings.TrimSpace(o.ReviewerModel)
	if err := o.Validate(); err != nil {
		return o, err
	}
	if o.ReviewerPersonaID != nil {
		if _, err := t.q.GetPersonaByID(t.ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: *o.ReviewerPersonaID}); err != nil {
			return o, domain.Invalid("reviewer_persona_id", "persona not found in company")
		}
	}
	return o, nil
}

// SetTaskReviewPolicy define (ou, vazia, remove) a sobreposição de revisão da task. Governança: papel admin.
func (s *Service) SetTaskReviewPolicy(ctx context.Context, actor domain.Actor, companyID, taskID domain.ID, in domain.ReviewOverride) (ReviewPolicyView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return ReviewPolicyView{}, err
	}
	if actor.Type == domain.ActorPersona {
		return ReviewPolicyView{}, fmt.Errorf("a persona não altera a política de revisão: %w", domain.ErrNotAllowed)
	}
	err := s.withTx(ctx, func(t *txc) error {
		task, err := t.q.GetTaskByIDForUpdate(ctx, store.GetTaskByIDForUpdateParams{CompanyID: companyID, ID: taskID})
		if err != nil {
			return mapErr(err, "get task")
		}
		in, err = t.checkReviewOverride(companyID, in)
		if err != nil {
			return err
		}
		if in.Empty() {
			if _, err := t.q.DeleteTaskReviewOverride(ctx, store.DeleteTaskReviewOverrideParams{CompanyID: companyID, TaskID: task.ID}); err != nil {
				return fmt.Errorf("clear task review override: %w", err)
			}
		} else if _, err := t.q.UpsertTaskReviewOverride(ctx, store.UpsertTaskReviewOverrideParams{TaskID: task.ID, CompanyID: companyID,
			RequireReview: pgBool(in.RequireReview), ReviewerMode: string(in.ReviewerMode), ReviewerPersonaID: in.ReviewerPersonaID,
			ReviewerAgent: in.ReviewerAgent, ReviewerModel: in.ReviewerModel, UpdatedByType: string(actor.Type), UpdatedByID: actor.ID, Now: t.now}); err != nil {
			return fmt.Errorf("set task review override: %w", err)
		}
		return t.activity(actor, companyID, "task.review_policy_changed", "task", task.ID, map[string]any{
			"require_review": in.RequireReview, "reviewer_mode": in.ReviewerMode, "reviewer_persona_id": in.ReviewerPersonaID,
			"reviewer_agent": in.ReviewerAgent, "reviewer_model": in.ReviewerModel}, "")
	})
	if err != nil {
		return ReviewPolicyView{}, err
	}
	return s.TaskReviewPolicy(ctx, companyID, taskID)
}

// SetProjectReviewPolicy define (ou, vazia, remove) a política de revisão do projeto. Governança: papel admin.
func (s *Service) SetProjectReviewPolicy(ctx context.Context, actor domain.Actor, companyID, projectID domain.ID, in domain.ReviewOverride) (domain.ReviewOverride, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.ReviewOverride{}, err
	}
	if actor.Type == domain.ActorPersona {
		return domain.ReviewOverride{}, fmt.Errorf("a persona não altera a política de revisão: %w", domain.ErrNotAllowed)
	}
	err := s.withTx(ctx, func(t *txc) error {
		pr, err := t.q.GetProjectByID(ctx, store.GetProjectByIDParams{CompanyID: companyID, ID: projectID})
		if err != nil {
			return mapErr(err, "get project")
		}
		in, err = t.checkReviewOverride(companyID, in)
		if err != nil {
			return err
		}
		if in.Empty() {
			if _, err := t.q.DeleteProjectReviewPolicy(ctx, store.DeleteProjectReviewPolicyParams{CompanyID: companyID, ProjectID: pr.ID}); err != nil {
				return fmt.Errorf("clear project review policy: %w", err)
			}
		} else if _, err := t.q.UpsertProjectReviewPolicy(ctx, store.UpsertProjectReviewPolicyParams{ProjectID: pr.ID, CompanyID: companyID,
			RequireReview: pgBool(in.RequireReview), ReviewerMode: string(in.ReviewerMode), ReviewerPersonaID: in.ReviewerPersonaID,
			ReviewerAgent: in.ReviewerAgent, ReviewerModel: in.ReviewerModel, UpdatedByType: string(actor.Type), UpdatedByID: actor.ID, Now: t.now}); err != nil {
			return fmt.Errorf("set project review policy: %w", err)
		}
		return t.activity(actor, companyID, "project.review_policy_changed", "project", pr.ID, map[string]any{
			"require_review": in.RequireReview, "reviewer_mode": in.ReviewerMode, "reviewer_persona_id": in.ReviewerPersonaID,
			"reviewer_agent": in.ReviewerAgent, "reviewer_model": in.ReviewerModel}, "")
	})
	if err != nil {
		return domain.ReviewOverride{}, err
	}
	r, err := s.read().GetProjectReviewPolicy(ctx, projectID)
	if isNoRows(err) {
		return domain.ReviewOverride{ReviewerMode: domain.ReviewerInherit}, nil
	}
	if err != nil {
		return domain.ReviewOverride{}, fmt.Errorf("project review policy: %w", err)
	}
	return overrideFromProjectRow(r), nil
}

// ProjectReviewPolicy lê a política de revisão do projeto (inherit/vazia se não houver).
func (s *Service) ProjectReviewPolicy(ctx context.Context, companyID, projectID domain.ID) (domain.ReviewOverride, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return domain.ReviewOverride{}, err
	}
	q := s.read()
	if _, err := q.GetProjectByID(ctx, store.GetProjectByIDParams{CompanyID: companyID, ID: projectID}); err != nil {
		return domain.ReviewOverride{}, mapErr(err, "get project")
	}
	r, err := q.GetProjectReviewPolicy(ctx, projectID)
	if isNoRows(err) {
		return domain.ReviewOverride{ReviewerMode: domain.ReviewerInherit}, nil
	}
	if err != nil {
		return domain.ReviewOverride{}, fmt.Errorf("project review policy: %w", err)
	}
	return overrideFromProjectRow(r), nil
}

// boolPtr/pgBool convertem o booleano anulável do banco (NULL = herda) de e para *bool.
func boolPtr(b pgtype.Bool) *bool {
	if !b.Valid {
		return nil
	}
	v := b.Bool
	return &v
}

func pgBool(b *bool) pgtype.Bool {
	if b == nil {
		return pgtype.Bool{}
	}
	return pgtype.Bool{Bool: *b, Valid: true}
}

// mapCoordErr traduz as violações dos triggers de coordenação para erros de domínio.
func mapCoordErr(err error) error {
	switch constraintOf(err) {
	case "tasks_parent_no_cycle":
		return domain.Invalid("parent_id", "a task não pode ser ancestral de si mesma")
	case "tasks_parent_company":
		return domain.Invalid("parent_id", "task not found in company")
	case "tasks_parent_depth":
		return domain.Invalid("parent_id", fmt.Sprintf("a árvore de tasks tem no máximo %d níveis", domain.MaxTaskDepth))
	case "task_dependencies_no_cycle":
		return domain.ErrDependencyCycle
	}
	return err
}
