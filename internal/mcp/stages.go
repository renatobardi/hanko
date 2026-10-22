package mcp

import (
	"context"
	"errors"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Pipelines sobre tasks (ADR-0048): a persona da etapa conclui a etapa com hanko_stage_complete.

type stagesBackend interface {
	CompleteTaskStage(ctx context.Context, actor domain.Actor, companyID, taskID domain.ID, note string) (service.TaskStageView, error)
}

type stageCompleteIn struct {
	Company string `json:"company,omitempty" jsonschema:"slug ou id da empresa (obrigatório só sem token de run)"`
	Task    string `json:"task,omitempty" jsonschema:"chave da task; com token de run o padrão é a task da execução atual"`
	Note    string `json:"note,omitempty" jsonschema:"o que você entregou nesta etapa, em poucas linhas (vai para o gate e para a próxima persona)"`
}

type stageCompleteOut struct {
	Task      string `json:"task"`
	Stage     string `json:"stage"`
	State     string `json:"state"`
	NextStage string `json:"next_stage,omitempty"`
}

func registerStageTools(s *sdk.Server, t *tools) {
	if _, ok := t.b.(stagesBackend); !ok {
		return
	}
	sdk.AddTool(s, &sdk.Tool{
		Name: "hanko_stage_complete",
		Description: "Conclui a etapa do pipeline do projeto em que esta task está. Sem gate na etapa, a task avança para a próxima etapa " +
			"e a persona dela é acordada; com gate (aprovação ou revisão), um humano decide antes. Use no lugar de dar a task como concluída. " +
			"Depois de chamar, encerre a execução.",
	}, t.stageComplete)
}

func (t *tools) stageComplete(ctx context.Context, _ *sdk.CallToolRequest, in stageCompleteIn) (*sdk.CallToolResult, stageCompleteOut, error) {
	sb, ok := t.b.(stagesBackend)
	if !ok {
		return nil, stageCompleteOut{}, errors.New("pipelines indisponíveis neste servidor")
	}
	id, company, err := t.scope(ctx, in.Company)
	if err != nil {
		return nil, stageCompleteOut{}, err
	}
	task, err := t.gateTask(ctx, id, company, in.Task)
	if err != nil {
		return nil, stageCompleteOut{}, err
	}
	if task == nil {
		return nil, stageCompleteOut{}, errors.New("informe a task (sem token de run não há task padrão)")
	}
	v, err := sb.CompleteTaskStage(ctx, id.Actor, company, *task, strings.TrimSpace(in.Note))
	if err != nil {
		return nil, stageCompleteOut{}, opErr("concluir etapa", err)
	}
	out := stageCompleteOut{Task: task.String(), Stage: v.Stage.Key, State: string(v.State), NextStage: v.NextKey}
	msg := "Etapa concluída; a task está agora na etapa " + v.Stage.Name + ". Encerre esta execução."
	switch v.State {
	case domain.TaskStageCompleting:
		msg = "Etapa concluída; um gate decide o avanço. Encerre esta execução: você será acordada se pedirem mudanças."
	case domain.TaskStageFinished:
		msg = "Última etapa concluída: a task foi encerrada. Encerre esta execução."
	}
	return textResult("%s", msg), out, nil
}
