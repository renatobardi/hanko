package mcp

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Artefatos e metas pelo MCP (ADR-0028).

type artifactPutIn struct {
	Company       string `json:"company,omitempty" jsonschema:"slug ou id da empresa (obrigatório só sem token de run)"`
	Name          string `json:"name" jsonschema:"nome do arquivo, sem barras (ex. relatorio.md); publicar o mesmo nome na mesma task cria a versão seguinte"`
	Content       string `json:"content,omitempty" jsonschema:"conteúdo em texto (UTF-8): markdown, código, JSON, CSV. Para binário use content_base64"`
	ContentBase64 string `json:"content_base64,omitempty" jsonschema:"conteúdo em base64, para arquivos binários (imagens, PDF). Use content OU content_base64"`
	Mime          string `json:"mime,omitempty" jsonschema:"tipo do conteúdo (ex. text/markdown); vazio = deduzido do nome"`
	Task          string `json:"task,omitempty" jsonschema:"chave da task; com token de run é sempre a task da sua execução"`
	Project       string `json:"project,omitempty" jsonschema:"slug do projeto; vazio = o da task"`
}

type artifactOut struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Version   int    `json:"version" jsonschema:"1 na primeira publicação; sobe a cada versão nova com o mesmo nome na mesma task"`
	Mime      string `json:"mime"`
	SizeBytes int64  `json:"size_bytes"`
	TaskKey   string `json:"task_key,omitempty"`
	SHA256    string `json:"sha256"`
}

type artifactPutOut struct {
	Artifact artifactOut `json:"artifact"`
}

type goalListIn struct {
	Company string `json:"company,omitempty" jsonschema:"slug ou id da empresa (obrigatório só sem token de run)"`
	Status  string `json:"status,omitempty" jsonschema:"active (padrão), achieved, abandoned ou all"`
	Project string `json:"project,omitempty" jsonschema:"slug do projeto: só as metas dele"`
}

type goalOut struct {
	Slug        string  `json:"slug"`
	Title       string  `json:"title"`
	Metric      string  `json:"metric,omitempty"`
	Unit        string  `json:"unit,omitempty"`
	Start       float64 `json:"start_value"`
	Target      float64 `json:"target_value"`
	Current     float64 `json:"current_value"`
	Progress    float64 `json:"progress_percent" jsonschema:"0 a 100"`
	Status      string  `json:"status"`
	DueDate     string  `json:"due_date,omitempty"`
	ProjectSlug string  `json:"project_slug,omitempty"`
	Tasks       int     `json:"tasks" jsonschema:"quantas tasks servem a esta meta"`
}

type goalListOut struct {
	Goals []goalOut `json:"goals"`
}

type goalUpdateIn struct {
	Company string  `json:"company,omitempty" jsonschema:"slug ou id da empresa (obrigatório só sem token de run)"`
	Goal    string  `json:"goal" jsonschema:"slug da meta (veja hanko_goal_list)"`
	Value   float64 `json:"value" jsonschema:"o valor ATUAL da métrica (não o incremento); ao cruzar o alvo a meta é atingida"`
	Note    string  `json:"note,omitempty" jsonschema:"nota curta sobre a medição (de onde veio o número)"`
}

type goalUpdateOut struct {
	Goal goalOut `json:"goal"`
}

func registerArtifactTools(s *sdk.Server, t *tools) {
	sdk.AddTool(s, &sdk.Tool{
		Name: "hanko_artifact_put",
		Description: "Publica um arquivo que você produziu (relatório, plano, dados, imagem) como artefato da task, para o humano abrir na tela Artefatos. " +
			"Com token de run ele fica ligado à sua task e execução. Publicar de novo o mesmo nome na mesma task cria a versão seguinte (o mesmo conteúdo não duplica). " +
			"Prefira artefato a colar texto longo em comentário. Texto em content; binário em content_base64. O tamanho máximo do arquivo é HANKO_ARTIFACT_MAX_BYTES (padrão 5 MiB); acima disso a publicação é recusada.",
	}, t.artifactPut)
	sdk.AddTool(s, &sdk.Tool{
		Name:        "hanko_goal_list",
		Description: "Lista as metas da empresa com o progresso (padrão: as ativas). Use para saber o que o trabalho deve mover e quais metas pode medir.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true},
	}, t.goalList)
	sdk.AddTool(s, &sdk.Tool{
		Name: "hanko_goal_update",
		Description: "Registra o valor atual de uma meta (hanko_goal_list mostra as metas). Informe o valor medido, não o incremento. " +
			"Ao cruzar o alvo a meta é marcada como atingida e o humano é avisado. Só registre números que você mediu de fato.",
	}, t.goalUpdate)
}

func toArtifactOut(v service.ArtifactView) artifactOut {
	return artifactOut{ID: v.ID.String(), Name: v.Name, Version: v.Version, Mime: v.Mime, SizeBytes: v.SizeBytes, TaskKey: v.TaskKey, SHA256: v.SHA256}
}

func toGoalOut(v service.GoalView) goalOut {
	o := goalOut{Slug: v.Slug, Title: v.Title, Metric: v.Metric, Unit: v.Unit, Start: v.StartValue, Target: v.TargetValue, Current: v.CurrentValue,
		Progress: v.ProgressPercent, Status: string(v.Status), ProjectSlug: v.ProjectSlug, Tasks: v.TasksTotal}
	if v.DueDate != nil {
		o.DueDate = v.DueDate.Format("2006-01-02")
	}
	return o
}

func (t *tools) artifactPut(ctx context.Context, _ *sdk.CallToolRequest, in artifactPutIn) (*sdk.CallToolResult, artifactPutOut, error) {
	id, company, err := t.scope(ctx, in.Company)
	if err != nil {
		return nil, artifactPutOut{}, err
	}
	if strings.TrimSpace(in.Name) == "" {
		return nil, artifactPutOut{}, errors.New("name é obrigatório")
	}
	var content []byte
	switch {
	case in.Content != "" && in.ContentBase64 != "":
		return nil, artifactPutOut{}, errors.New("use content OU content_base64, não os dois")
	case in.ContentBase64 != "":
		if content, err = base64.StdEncoding.DecodeString(strings.TrimSpace(in.ContentBase64)); err != nil {
			return nil, artifactPutOut{}, errors.New("content_base64 não é base64 válido")
		}
	case in.Content != "":
		content = []byte(in.Content)
	default:
		return nil, artifactPutOut{}, errors.New("content (texto) ou content_base64 é obrigatório")
	}
	pi := service.PutArtifactInput{Name: in.Name, Mime: in.Mime, Content: content}
	if tk := strings.TrimSpace(in.Task); tk != "" {
		v, err := t.b.GetTask(ctx, company, tk)
		if err != nil {
			return nil, artifactPutOut{}, toolErr("task", tk, err)
		}
		if id.ViaToken && (id.TaskID == nil || v.ID != *id.TaskID) {
			return nil, artifactPutOut{}, errors.New("com token de run o artefato é sempre da task da sua execução; omita task")
		}
		pi.TaskID = &v.ID
	}
	if id.ViaToken {
		pi.TaskID, pi.RunID = id.TaskID, id.RunID
	}
	if p := strings.TrimSpace(in.Project); p != "" {
		pr, err := t.b.GetProject(ctx, company, p)
		if err != nil {
			return nil, artifactPutOut{}, toolErr("projeto", p, err)
		}
		pi.ProjectID = &pr.ID
	}
	v, err := t.b.PutArtifact(ctx, id.Actor, company, pi)
	if err != nil {
		return nil, artifactPutOut{}, opErr("publicar artefato", err)
	}
	where := ""
	if v.TaskKey != "" {
		where = " na task " + v.TaskKey
	}
	return textResult("Artefato %s v%d publicado%s (%s, %d bytes)", v.Name, v.Version, where, v.Mime, v.SizeBytes), artifactPutOut{Artifact: toArtifactOut(v)}, nil
}

func (t *tools) goalList(ctx context.Context, _ *sdk.CallToolRequest, in goalListIn) (*sdk.CallToolResult, goalListOut, error) {
	_, company, err := t.scope(ctx, in.Company)
	if err != nil {
		return nil, goalListOut{}, err
	}
	f := service.GoalFilter{Status: domain.GoalActive}
	switch s := strings.TrimSpace(in.Status); s {
	case "", "active":
	case "all":
		f.Status = ""
	default:
		f.Status = domain.GoalStatus(s)
		if !f.Status.Valid() {
			return nil, goalListOut{}, fmt.Errorf("status %q inválido; use active, achieved, abandoned ou all", s)
		}
	}
	if p := strings.TrimSpace(in.Project); p != "" {
		pr, err := t.b.GetProject(ctx, company, p)
		if err != nil {
			return nil, goalListOut{}, toolErr("projeto", p, err)
		}
		f.ProjectID = &pr.ID
	}
	list, err := t.b.ListGoalViews(ctx, company, f)
	if err != nil {
		return nil, goalListOut{}, opErr("listar metas", err)
	}
	out := goalListOut{Goals: make([]goalOut, 0, len(list))}
	var sb strings.Builder
	for _, g := range list {
		o := toGoalOut(g)
		out.Goals = append(out.Goals, o)
		fmt.Fprintf(&sb, "\n- %s — %s: %.0f%% (%v de %v %s), %s", o.Slug, o.Title, o.Progress, o.Current, o.Target, o.Unit, o.Status)
	}
	return textResult("%d meta(s)%s", len(out.Goals), sb.String()), out, nil
}

func (t *tools) goalUpdate(ctx context.Context, _ *sdk.CallToolRequest, in goalUpdateIn) (*sdk.CallToolResult, goalUpdateOut, error) {
	id, company, err := t.scope(ctx, in.Company)
	if err != nil {
		return nil, goalUpdateOut{}, err
	}
	ref := strings.TrimSpace(in.Goal)
	if ref == "" {
		return nil, goalUpdateOut{}, errors.New("goal (slug da meta) é obrigatório")
	}
	g, err := t.b.GetGoal(ctx, company, ref)
	if err != nil {
		return nil, goalUpdateOut{}, toolErr("meta", ref, err)
	}
	v, err := t.b.RecordGoalProgress(ctx, id.Actor, company, g.ID, in.Value, in.Note)
	if err != nil {
		return nil, goalUpdateOut{}, opErr("registrar progresso da meta", err)
	}
	msg := fmt.Sprintf("Meta %s: %.0f%% (%v de %v)", v.Slug, v.ProgressPercent, v.CurrentValue, v.TargetValue)
	if v.Status == domain.GoalAchieved {
		msg += " — meta atingida"
	}
	return textResult("%s", msg), goalUpdateOut{Goal: toGoalOut(v)}, nil
}
