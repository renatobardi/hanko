package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/renatobardi/hanko/internal/service"
)

// Coordenação de tasks (ADR-0040): a tool de dependências bloqueantes.

type taskDependencyIn struct {
	Company   string `json:"company,omitempty" jsonschema:"slug ou id da empresa (obrigatório só sem token de run)"`
	Action    string `json:"action" jsonschema:"add (a task passa a depender de depends_on), remove (desfaz) ou list (mostra dependências e dependentes)"`
	Key       string `json:"key,omitempty" jsonschema:"chave da task que depende; com token de run o padrão é a task da execução atual"`
	DependsOn string `json:"depends_on,omitempty" jsonschema:"chave da task de que a primeira depende (add e remove)"`
}

type dependencyRefOut struct {
	Key    string `json:"key"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Closed bool   `json:"closed" jsonschema:"done ou cancelled: já não bloqueia"`
}

type taskDependencyOut struct {
	Task       taskOut            `json:"task"`
	DependsOn  []dependencyRefOut `json:"depends_on"`
	Dependents []dependencyRefOut `json:"dependents"`
}

func registerCoordinationTools(s *sdk.Server, t *tools) {
	sdk.AddTool(s, &sdk.Tool{
		Name: "hanko_task_dependency",
		Description: "Ordena tasks por dependência bloqueante. action=add faz a task depender de outra (sem ciclos, mesma empresa): ela só é despachada quando TODAS as dependências fecharem (done ou cancelled) e o responsável é acordado automaticamente ao fechar a última. " +
			"action=remove desfaz; action=list mostra de quem a task depende e quem depende dela. Sem key, vale a task da sua execução. Para criar já com dependência, use depends_on em hanko_task_create.",
	}, t.taskDependency)
}

func refsOut(in []service.DependencyView) []dependencyRefOut {
	out := make([]dependencyRefOut, 0, len(in))
	for _, d := range in {
		out = append(out, dependencyRefOut{Key: d.Key, Title: d.Title, Status: string(d.Status), Closed: d.Closed})
	}
	return out
}

func (t *tools) taskDependency(ctx context.Context, _ *sdk.CallToolRequest, in taskDependencyIn) (*sdk.CallToolResult, taskDependencyOut, error) {
	id, company, err := t.scope(ctx, in.Company)
	if err != nil {
		return nil, taskDependencyOut{}, err
	}
	key := strings.TrimSpace(in.Key)
	var cur service.TaskView
	switch {
	case key != "":
		cur, err = t.b.GetTask(ctx, company, key)
	case id.ViaToken && id.TaskID != nil:
		cur, err = t.b.GetTask(ctx, company, id.TaskID.String())
	default:
		return nil, taskDependencyOut{}, errors.New("key é obrigatório")
	}
	if err != nil {
		return nil, taskDependencyOut{}, toolErr("task", key, err)
	}
	action := strings.ToLower(strings.TrimSpace(in.Action))
	switch action {
	case "add", "remove":
		ref := strings.TrimSpace(in.DependsOn)
		if ref == "" {
			return nil, taskDependencyOut{}, errors.New("depends_on é obrigatório em add e remove")
		}
		dep, err := t.b.GetTask(ctx, company, ref)
		if err != nil {
			return nil, taskDependencyOut{}, toolErr("dependência", ref, err)
		}
		if action == "add" {
			cur, err = t.b.AddTaskDependency(ctx, id.Actor, company, cur.ID, dep.ID)
		} else {
			cur, err = t.b.RemoveTaskDependency(ctx, id.Actor, company, cur.ID, dep.ID)
		}
		if err != nil {
			return nil, taskDependencyOut{}, opErr(action+" dependência "+cur.Key, err)
		}
	case "list":
	default:
		return nil, taskDependencyOut{}, fmt.Errorf("action %q inválida; use add, remove ou list", in.Action)
	}
	rel, err := t.b.TaskRelations(ctx, company, cur.ID)
	if err != nil {
		return nil, taskDependencyOut{}, opErr("ler dependências", err)
	}
	out := taskDependencyOut{Task: toTask(cur), DependsOn: refsOut(rel.DependsOn), Dependents: refsOut(rel.Dependents)}
	var sb strings.Builder
	sb.WriteString(taskLine(cur))
	if cur.Blocked {
		fmt.Fprintf(&sb, "\nBLOQUEADA por %d dependência(s) aberta(s)", cur.OpenDependencies)
	}
	for _, d := range out.DependsOn {
		fmt.Fprintf(&sb, "\n- depende de %s [%s] %s", d.Key, d.Status, d.Title)
	}
	for _, d := range out.Dependents {
		fmt.Fprintf(&sb, "\n- %s espera por esta [%s] %s", d.Key, d.Status, d.Title)
	}
	return textResult("%s", sb.String()), out, nil
}
