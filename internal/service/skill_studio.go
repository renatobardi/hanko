package service

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Estúdio de skills (ADR-0035): testar uma skill numa versão fixa antes de ligá-la. Um teste é uma task de sistema
// atribuída à persona escolhida, acordada com o gatilho skill_test; a run recebe SÓ aquela skill naquela versão,
// a entrada como prompt e nenhum MCP da empresa, e passa pela máquina normal (checkout, orçamento, ledger). Ao fim
// da run o resumo vira a saída do teste e a task fecha. Rodar custa dinheiro: só admin roda, cancela e salva
// entradas; qualquer membro lê.

// skillTestStaleAfter é quanto um teste espera na fila sem nenhuma run ativa antes de falhar (persona parada,
// orçamento estourado).
const skillTestStaleAfter = 6 * time.Hour

// RunSkillTestInput pede uma execução de teste. Input (texto livre) ou InputID (entrada salva), um dos dois.
// Revision nil = a versão atual da skill.
type RunSkillTestInput struct {
	PersonaID domain.ID
	Input     string
	InputID   *domain.ID
	Revision  *int
}

// SkillTestSpec é o que o executor precisa para montar a run de teste.
type SkillTestSpec struct {
	TestID domain.ID
	Skill  domain.Skill // conteúdo da versão testada (Version = a revisão)
	Input  string
}

func testInputFromRow(r store.SkillTestInput) domain.SkillTestInput {
	return domain.SkillTestInput{ID: r.ID, SkillID: r.SkillID, Name: r.Name, Content: r.Content, CreatedBy: actorOf(r.CreatedByType, r.CreatedByID),
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

func normalizeTestInput(name, content string) (string, string, error) {
	n := strings.Join(strings.Fields(name), " ")
	c := strings.TrimSpace(strings.ReplaceAll(content, "\r\n", "\n"))
	switch {
	case n == "":
		return "", "", domain.Invalid("name", "dê um nome à entrada")
	case utf8.RuneCountInString(n) > 120:
		return "", "", domain.Invalid("name", "no máximo 120 caracteres")
	}
	if err := checkTestContent(c); err != nil {
		return "", "", err
	}
	return n, c, nil
}

func checkTestContent(c string) error {
	switch {
	case c == "":
		return domain.Invalid("input", "escreva a entrada do teste")
	case len(c) > domain.MaxSkillTestInputBytes:
		return domain.Invalid("input", fmt.Sprintf("no máximo %d KiB", domain.MaxSkillTestInputBytes>>10))
	case !utf8.ValidString(c):
		return domain.Invalid("input", "texto UTF-8 inválido")
	}
	return nil
}

// ListSkillTestInputs devolve as entradas salvas da skill.
func (s *Service) ListSkillTestInputs(ctx context.Context, companyID, skillID domain.ID) ([]domain.SkillTestInput, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	rows, err := s.read().ListSkillTestInputs(ctx, store.ListSkillTestInputsParams{CompanyID: companyID, SkillID: skillID})
	if err != nil {
		return nil, fmt.Errorf("list skill test inputs: %w", err)
	}
	out := make([]domain.SkillTestInput, len(rows))
	for i, r := range rows {
		out[i] = testInputFromRow(r)
	}
	return out, nil
}

// GetSkillTestInput devolve uma entrada salva.
func (s *Service) GetSkillTestInput(ctx context.Context, companyID, skillID, id domain.ID) (domain.SkillTestInput, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return domain.SkillTestInput{}, err
	}
	r, err := s.read().GetSkillTestInput(ctx, store.GetSkillTestInputParams{CompanyID: companyID, SkillID: skillID, ID: id})
	if err != nil {
		return domain.SkillTestInput{}, mapErr(err, "get skill test input")
	}
	return testInputFromRow(r), nil
}

// CreateSkillTestInput salva uma entrada (admin). Nome repetido na skill é ErrConflict.
func (s *Service) CreateSkillTestInput(ctx context.Context, actor domain.Actor, companyID, skillID domain.ID, name, content string) (domain.SkillTestInput, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.SkillTestInput{}, err
	}
	n, c, err := normalizeTestInput(name, content)
	if err != nil {
		return domain.SkillTestInput{}, err
	}
	var out domain.SkillTestInput
	err = s.withTx(ctx, func(t *txc) error {
		sk, err := t.q.GetSkillByIDForUpdate(t.ctx, store.GetSkillByIDForUpdateParams{CompanyID: companyID, ID: skillID})
		if err != nil {
			return mapErr(err, "get skill")
		}
		cnt, err := t.q.CountSkillTestInputs(t.ctx, store.CountSkillTestInputsParams{CompanyID: companyID, SkillID: skillID})
		if err != nil {
			return fmt.Errorf("count skill test inputs: %w", err)
		}
		if cnt >= domain.MaxSkillTestInputs {
			return domain.Invalid("name", fmt.Sprintf("a skill já tem %d entradas salvas; apague uma antes", domain.MaxSkillTestInputs))
		}
		row, err := t.q.CreateSkillTestInput(t.ctx, store.CreateSkillTestInputParams{ID: domain.NewID(), CompanyID: companyID, SkillID: skillID,
			Name: n, Content: c, ActorType: string(actor.Type), ActorID: actor.ID, Now: t.now})
		if err != nil {
			return mapErr(err, "create skill test input")
		}
		out = testInputFromRow(row)
		return t.activity(actor, companyID, "skill.test_input_created", "skill", skillID, map[string]any{"slug": sk.Slug, "input_id": row.ID, "name": n}, "")
	})
	return out, err
}

// UpdateSkillTestInput muda nome e/ou conteúdo de uma entrada (admin).
func (s *Service) UpdateSkillTestInput(ctx context.Context, actor domain.Actor, companyID, skillID, id domain.ID, name, content *string) (domain.SkillTestInput, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.SkillTestInput{}, err
	}
	var out domain.SkillTestInput
	err := s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetSkillTestInput(t.ctx, store.GetSkillTestInputParams{CompanyID: companyID, SkillID: skillID, ID: id})
		if err != nil {
			return mapErr(err, "get skill test input")
		}
		n, c := cur.Name, cur.Content
		if name != nil {
			n = *name
		}
		if content != nil {
			c = *content
		}
		n, c, err = normalizeTestInput(n, c)
		if err != nil {
			return err
		}
		row, err := t.q.UpdateSkillTestInput(t.ctx, store.UpdateSkillTestInputParams{CompanyID: companyID, SkillID: skillID, ID: id, Name: n, Content: c, Now: t.now})
		if err != nil {
			return mapErr(err, "update skill test input")
		}
		out = testInputFromRow(row)
		return t.activity(actor, companyID, "skill.test_input_updated", "skill", skillID, map[string]any{"input_id": id, "name": n}, "")
	})
	return out, err
}

// DeleteSkillTestInput apaga uma entrada (admin). Os testes que a usaram guardam a própria cópia.
func (s *Service) DeleteSkillTestInput(ctx context.Context, actor domain.Actor, companyID, skillID, id domain.ID) error {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return err
	}
	return s.withTx(ctx, func(t *txc) error {
		n, err := t.q.DeleteSkillTestInput(t.ctx, store.DeleteSkillTestInputParams{CompanyID: companyID, SkillID: skillID, ID: id})
		if err != nil {
			return fmt.Errorf("delete skill test input: %w", err)
		}
		if n == 0 {
			return notFound("skill test input")
		}
		return t.activity(actor, companyID, "skill.test_input_deleted", "skill", skillID, map[string]any{"input_id": id}, "")
	})
}

func testRunFromParts(t store.SkillTestRun, skillSlug, personaSlug, personaName string, liveRunID domain.ID, liveStatus, liveName string, cost *float64) domain.SkillTestRun {
	v := domain.SkillTestRun{ID: t.ID, CompanyID: t.CompanyID, SkillID: t.SkillID, SkillSlug: skillSlug, SkillRevision: int(t.SkillRevision),
		InputID: t.InputID, Input: t.Input, PersonaID: t.PersonaID, PersonaSlug: personaSlug, PersonaName: personaName, TaskID: t.TaskID,
		RunID: t.RunID, Status: domain.SkillTestStatus(t.Status), Output: t.Output, Error: t.Error, CreatedBy: actorOf(t.CreatedByType, t.CreatedByID),
		CreatedAt: t.CreatedAt, FinishedAt: t.FinishedAt, RunName: liveName, CostUSD: cost}
	if v.RunID == nil && liveRunID != (domain.ID{}) {
		id := liveRunID
		v.RunID = &id
	}
	// o estado ao vivo vem da run enquanto o teste não terminou
	if !v.Status.Terminal() && domain.RunStatus(liveStatus) == domain.RunRunning {
		v.Status = domain.SkillTestRunning
	}
	return v
}

// ListSkillTestRuns devolve os testes (de uma skill, ou de todas com skillID nil), mais recentes primeiro.
func (s *Service) ListSkillTestRuns(ctx context.Context, companyID domain.ID, skillID *domain.ID, limit int) ([]domain.SkillTestRun, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	rows, err := s.read().ListSkillTestRuns(ctx, store.ListSkillTestRunsParams{CompanyID: companyID, SkillID: skillID, Lim: i32(clamp(limit, 50, 200))})
	if err != nil {
		return nil, fmt.Errorf("list skill test runs: %w", err)
	}
	out := make([]domain.SkillTestRun, len(rows))
	for i, r := range rows {
		out[i] = testRunFromParts(store.SkillTestRun{ID: r.ID, CompanyID: r.CompanyID, SkillID: r.SkillID, SkillRevision: r.SkillRevision, InputID: r.InputID,
			Input: r.Input, PersonaID: r.PersonaID, TaskID: r.TaskID, RunID: r.RunID, Status: r.Status, Output: r.Output, Error: r.Error,
			CreatedByType: r.CreatedByType, CreatedByID: r.CreatedByID, CreatedAt: r.CreatedAt, FinishedAt: r.FinishedAt},
			r.SkillSlug, r.PersonaSlug, r.PersonaName, r.LiveRunID, r.LiveRunStatus, r.LiveRunName, r.RunCostUsd)
	}
	return out, nil
}

// GetSkillTestRun devolve um teste, com o estado ao vivo da run.
func (s *Service) GetSkillTestRun(ctx context.Context, companyID, id domain.ID) (domain.SkillTestRun, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return domain.SkillTestRun{}, err
	}
	r, err := s.read().GetSkillTestRun(ctx, store.GetSkillTestRunParams{CompanyID: companyID, ID: id})
	if err != nil {
		return domain.SkillTestRun{}, mapErr(err, "get skill test run")
	}
	return testRunFromParts(store.SkillTestRun{ID: r.ID, CompanyID: r.CompanyID, SkillID: r.SkillID, SkillRevision: r.SkillRevision, InputID: r.InputID,
		Input: r.Input, PersonaID: r.PersonaID, TaskID: r.TaskID, RunID: r.RunID, Status: r.Status, Output: r.Output, Error: r.Error,
		CreatedByType: r.CreatedByType, CreatedByID: r.CreatedByID, CreatedAt: r.CreatedAt, FinishedAt: r.FinishedAt},
		r.SkillSlug, r.PersonaSlug, r.PersonaName, r.LiveRunID, r.LiveRunStatus, r.LiveRunName, r.RunCostUsd), nil
}

// RunSkillTest cria um teste: a task de sistema da persona com a entrada, acordada com o gatilho skill_test (admin).
func (s *Service) RunSkillTest(ctx context.Context, actor domain.Actor, companyID, skillID domain.ID, in RunSkillTestInput) (domain.SkillTestRun, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.SkillTestRun{}, err
	}
	var id domain.ID
	err := s.withTx(ctx, func(t *txc) error {
		sk, err := t.q.GetSkillByID(t.ctx, store.GetSkillByIDParams{CompanyID: companyID, ID: skillID})
		if err != nil {
			return mapErr(err, "get skill")
		}
		rev := int(sk.Version)
		if in.Revision != nil {
			rev = *in.Revision
		}
		if _, err := t.q.GetSkillVersion(t.ctx, store.GetSkillVersionParams{CompanyID: companyID, SkillID: skillID, Revision: i32(rev)}); err != nil {
			if isNoRows(err) {
				return domain.Invalid("revision", fmt.Sprintf("a skill não tem a versão %d", rev))
			}
			return mapErr(err, "get skill version")
		}
		input := strings.TrimSpace(strings.ReplaceAll(in.Input, "\r\n", "\n"))
		switch {
		case in.InputID != nil && input != "":
			return domain.Invalid("input", "use a entrada salva ou o texto livre, não os dois")
		case in.InputID != nil:
			saved, err := t.q.GetSkillTestInput(t.ctx, store.GetSkillTestInputParams{CompanyID: companyID, SkillID: skillID, ID: *in.InputID})
			if err != nil {
				if isNoRows(err) {
					return domain.Invalid("input_id", "entrada salva não encontrada nesta skill")
				}
				return mapErr(err, "get skill test input")
			}
			input = saved.Content
		}
		if err := checkTestContent(input); err != nil {
			return err
		}
		tr, err := t.startSkillTest(actor, companyID, sk, rev, in.PersonaID, in.InputID, input)
		if err != nil {
			return err
		}
		id = tr.ID
		return nil
	})
	if err != nil {
		return domain.SkillTestRun{}, err
	}
	return s.GetSkillTestRun(ctx, companyID, id)
}

// RerunSkillTest repete um teste com a mesma versão, a mesma entrada e a mesma persona (admin).
func (s *Service) RerunSkillTest(ctx context.Context, actor domain.Actor, companyID, testID domain.ID) (domain.SkillTestRun, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.SkillTestRun{}, err
	}
	var id domain.ID
	err := s.withTx(ctx, func(t *txc) error {
		prev, err := t.q.GetSkillTestRunForUpdate(t.ctx, store.GetSkillTestRunForUpdateParams{CompanyID: companyID, ID: testID})
		if err != nil {
			return mapErr(err, "get skill test run")
		}
		sk, err := t.q.GetSkillByID(t.ctx, store.GetSkillByIDParams{CompanyID: companyID, ID: prev.SkillID})
		if err != nil {
			return mapErr(err, "get skill")
		}
		tr, err := t.startSkillTest(actor, companyID, sk, int(prev.SkillRevision), prev.PersonaID, prev.InputID, prev.Input)
		if err != nil {
			return err
		}
		id = tr.ID
		return nil
	})
	if err != nil {
		return domain.SkillTestRun{}, err
	}
	return s.GetSkillTestRun(ctx, companyID, id)
}

// startSkillTest grava o teste e cria a task de sistema que acorda a persona.
func (t *txc) startSkillTest(actor domain.Actor, companyID domain.ID, sk store.Skill, rev int, personaID domain.ID, inputID *domain.ID, input string) (store.SkillTestRun, error) {
	p, err := t.q.GetPersonaByID(t.ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: personaID})
	if err != nil {
		if isNoRows(err) {
			return store.SkillTestRun{}, domain.Invalid("persona", "persona não encontrada na empresa")
		}
		return store.SkillTestRun{}, mapErr(err, "get persona")
	}
	if !domain.PersonaStatus(p.Status).CanWork() {
		return store.SkillTestRun{}, domain.Invalid("persona", fmt.Sprintf("a persona não pode trabalhar agora (status %s)", p.Status))
	}
	tr, err := t.q.CreateSkillTestRun(t.ctx, store.CreateSkillTestRunParams{ID: domain.NewID(), CompanyID: companyID, SkillID: sk.ID,
		SkillRevision: i32(rev), InputID: inputID, Input: input, PersonaID: personaID, ActorType: string(actor.Type), ActorID: actor.ID, Now: t.now})
	if err != nil {
		return store.SkillTestRun{}, mapErr(err, "create skill test run")
	}
	pid := p.ID
	task, err := t.createTask(actor, companyID, CreateTaskInput{
		Title:       truncateRunes(fmt.Sprintf("Teste de skill — %s v%d", sk.Slug, rev), 500),
		Description: input, Priority: domain.PriorityMedium, PersonaID: &pid,
		trigger: domain.TriggerSkillTest, noMemoryFact: true})
	if err != nil {
		return store.SkillTestRun{}, fmt.Errorf("create skill test task: %w", err)
	}
	if err := t.q.SetSkillTestRunTask(t.ctx, store.SetSkillTestRunTaskParams{ID: tr.ID, TaskID: &task.ID}); err != nil {
		return store.SkillTestRun{}, fmt.Errorf("set skill test task: %w", err)
	}
	tr.TaskID = &task.ID
	return tr, t.activity(actor, companyID, "skill.test_started", "skill", sk.ID, map[string]any{"slug": sk.Slug, "revision": rev,
		"test_id": tr.ID, "persona": p.Slug, "task_id": task.ID, "task_key": task.Key}, "")
}

// CancelSkillTest cancela um teste em andamento (admin): cancela a run, se houver, e fecha a task de sistema.
func (s *Service) CancelSkillTest(ctx context.Context, actor domain.Actor, companyID, testID domain.ID) (domain.SkillTestRun, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.SkillTestRun{}, err
	}
	err := s.withTx(ctx, func(t *txc) error {
		peek, err := t.q.GetSkillTestRunForUpdate(t.ctx, store.GetSkillTestRunForUpdateParams{CompanyID: companyID, ID: testID})
		if err != nil {
			return mapErr(err, "get skill test run")
		}
		if domain.SkillTestStatus(peek.Status).Terminal() {
			return nil
		}
		if peek.TaskID != nil {
			if err := t.cancelTaskRun(actor, companyID, *peek.TaskID, "skill test cancelled"); err != nil {
				return err
			}
		}
		return t.finishSkillTest(peek, domain.SkillTestCancelled, nil, "", "cancelado")
	})
	if err != nil {
		return domain.SkillTestRun{}, err
	}
	return s.GetSkillTestRun(ctx, companyID, testID)
}

// finishSkillTest grava o desfecho e fecha a task de sistema. Teste já terminado não muda.
func (t *txc) finishSkillTest(cur store.SkillTestRun, st domain.SkillTestStatus, runID *domain.ID, output, errMsg string) error {
	row, err := t.q.FinishSkillTestRun(t.ctx, store.FinishSkillTestRunParams{ID: cur.ID, Status: string(st), RunID: runID,
		Output: clipText(strings.TrimSpace(output), domain.MaxSkillTestOutputBytes), Error: clipText(errMsg, 2000), Now: &t.now})
	if isNoRows(err) {
		return nil
	}
	if err != nil {
		return mapErr(err, "finish skill test run")
	}
	if row.TaskID != nil {
		ts := domain.TaskCancelled
		if st == domain.SkillTestDone {
			ts = domain.TaskDone
		}
		if err := t.closeSystemTask(row.CompanyID, *row.TaskID, ts); err != nil {
			return err
		}
	}
	return t.activity(domain.SystemActor, row.CompanyID, "skill.test_finished", "skill", row.SkillID,
		map[string]any{"test_id": row.ID, "status": st, "run_id": row.RunID, "revision": row.SkillRevision}, "")
}

// isSkillTestTask diz se a task é a task de sistema de um teste do estúdio.
func (t *txc) isSkillTestTask(taskID domain.ID) (bool, error) {
	ok, err := t.q.IsSkillTestTask(t.ctx, &taskID)
	if err != nil {
		return false, fmt.Errorf("is skill test task: %w", err)
	}
	return ok, nil
}

// errSkillTestWake é a recusa de acordar à mão a task de sistema de um teste.
const errSkillTestWake = "esta é a task de sistema de um teste de skill: o Hanko a acorda sozinho; use Reexecutar no Estúdio"

// skillTestRunEnded fecha o teste quando a run termina (qualquer desfecho): a saída é o resumo da run.
func (t *txc) skillTestRunEnded(run store.Run, status domain.RunStatus, summary, errMsg string) error {
	if run.TaskID == nil {
		return nil
	}
	cur, err := t.q.GetSkillTestRunByTaskForUpdate(t.ctx, run.TaskID)
	if isNoRows(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get skill test run: %w", err)
	}
	if domain.SkillTestStatus(cur.Status).Terminal() {
		return nil
	}
	st := domain.SkillTestFailed
	switch status {
	case domain.RunDone:
		st = domain.SkillTestDone
	case domain.RunCancelled:
		st = domain.SkillTestCancelled
	}
	if st == domain.SkillTestFailed && errMsg == "" {
		errMsg = string(status)
	}
	return t.finishSkillTest(cur, st, &run.ID, summary, errMsg)
}

// SkillTestRunSpec devolve a skill (na versão testada) e a entrada da run de teste de uma task. Chamado pelo
// executor e pelo MCP (sem identidade de usuário). ErrNotFound quando a task não é de teste.
func (s *Service) SkillTestRunSpec(ctx context.Context, companyID, taskID domain.ID) (SkillTestSpec, error) {
	q := s.read()
	tr, err := q.GetSkillTestRunByTask(ctx, store.GetSkillTestRunByTaskParams{CompanyID: companyID, TaskID: &taskID})
	if err != nil {
		return SkillTestSpec{}, mapErr(err, "get skill test run")
	}
	sk, err := q.GetSkillByID(ctx, store.GetSkillByIDParams{CompanyID: companyID, ID: tr.SkillID})
	if err != nil {
		return SkillTestSpec{}, mapErr(err, "get skill")
	}
	v, err := q.GetSkillVersion(ctx, store.GetSkillVersionParams{CompanyID: companyID, SkillID: tr.SkillID, Revision: tr.SkillRevision})
	if err != nil {
		return SkillTestSpec{}, mapErr(err, "get skill version")
	}
	k := skillFromRow(sk)
	k.Name, k.Description, k.Body, k.Version, k.Pinned = v.Name, v.Description, v.Body, int(v.Revision), true
	return SkillTestSpec{TestID: tr.ID, Skill: k, Input: tr.Input}, nil
}

// ResolveStaleSkillTests falha os testes que esperam há tempo demais sem nenhuma run ativa e fecha a task.
// Rede de segurança do tick.
func (s *Service) ResolveStaleSkillTests(ctx context.Context) (int, error) {
	rows, err := s.read().ListStaleSkillTestRuns(ctx, store.ListStaleSkillTestRunsParams{Before: s.Now().UTC().Add(-skillTestStaleAfter), Lim: 50})
	if err != nil {
		return 0, fmt.Errorf("list stale skill tests: %w", err)
	}
	n := 0
	for _, r := range rows {
		err := s.withTx(ctx, func(t *txc) error {
			if r.TaskID != nil { // ordem canônica: task antes do teste
				if _, err := t.q.GetTaskByIDForUpdate(t.ctx, store.GetTaskByIDForUpdateParams{CompanyID: r.CompanyID, ID: *r.TaskID}); err != nil && !isNoRows(err) {
					return mapErr(err, "get task")
				}
			}
			cur, err := t.q.GetSkillTestRunForUpdate(t.ctx, store.GetSkillTestRunForUpdateParams{CompanyID: r.CompanyID, ID: r.ID})
			if err != nil {
				return mapErr(err, "get skill test run")
			}
			if domain.SkillTestStatus(cur.Status).Terminal() {
				return nil
			}
			if cur.TaskID != nil {
				if active, err := t.q.ActiveRunForTask(t.ctx, *cur.TaskID); err == nil && active.ID != (domain.ID{}) {
					return nil // a run começou enquanto olhávamos
				} else if err != nil && !isNoRows(err) {
					return fmt.Errorf("active run for task: %w", err)
				}
			}
			n++
			return t.finishSkillTest(cur, domain.SkillTestFailed, nil, "", "a execução não chegou a rodar (persona parada ou orçamento esgotado)")
		})
		if err != nil {
			return n, err
		}
	}
	return n, nil
}
