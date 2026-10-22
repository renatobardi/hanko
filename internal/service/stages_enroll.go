package service

import (
	"fmt"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// stageForNewTask devolve a primeira etapa do pipeline quando a task nova entra nele (projeto com etapas, persona não
// escolhida por quem criou, criador humano) e ajusta a entrada da criação: persona e status da etapa. Task de backlog continua no
// backlog (a etapa vale quando ela sair de lá).
func (t *txc) stageForNewTask(actor domain.Actor, companyID domain.ID, in *CreateTaskInput, explicitPersona bool) (*domain.ProjectStage, error) {
	if in.ProjectID == nil || explicitPersona || in.noMemoryFact || actor.Type != domain.ActorHuman {
		return nil, nil
	}
	first, err := t.firstStageFor(companyID, *in.ProjectID)
	if err != nil || first == nil {
		return nil, err
	}
	in.PersonaID = first.PersonaID
	if in.Status == "" || in.Status == domain.TaskTodo {
		in.Status = first.Status
	}
	return first, nil
}

// stageEnrollNew registra a entrada de uma task recém-criada na primeira etapa (o status e a persona já foram aplicados
// na criação, que também acordou a persona). Idempotente: uma criação repetida pelo mesmo event_id não duplica.
func (t *txc) stageEnrollNew(actor domain.Actor, companyID, taskID domain.ID, st domain.ProjectStage) error {
	if _, err := t.q.GetTaskStage(t.ctx, store.GetTaskStageParams{CompanyID: companyID, TaskID: taskID}); err == nil {
		return nil
	} else if !isNoRows(err) {
		return fmt.Errorf("task stage: %w", err)
	}
	if _, err := t.q.UpsertTaskStage(t.ctx, store.UpsertTaskStageParams{TaskID: taskID, CompanyID: companyID, ProjectID: st.ProjectID,
		StageID: st.ID, State: string(domain.TaskStageWorking), Now: t.now}); err != nil {
		return mapErr(err, "upsert task stage")
	}
	if err := t.stageEvent(actor, companyID, st.ProjectID, taskID, domain.StageEvEnrolled, "", st.Key, nil, "", ""); err != nil {
		return err
	}
	return t.activity(actor, companyID, "task_stage.entered", "task", taskID,
		map[string]any{"stage": st.Key, "kind": domain.StageEvEnrolled, "persona_id": st.PersonaID}, "")
}
