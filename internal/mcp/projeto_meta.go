package mcp

import (
	"context"
	"errors"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Criar projeto e meta pelo MCP (ADR-0052), inclusive a partir de uma run de chat (que não tem task). Chamar de novo
// com o mesmo slug devolve o recurso existente em vez de falhar.

type projectGoalBackend interface {
	CreateProject(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateProjectInput) (domain.Project, error)
	CreateGoal(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateGoalInput) (domain.Goal, error)
	GetWorkspace(ctx context.Context, companyID domain.ID, ref string) (domain.Workspace, error)
}

type projectCreateIn struct {
	Company     string `json:"company,omitempty" jsonschema:"slug ou id da empresa (obrigatório só sem token de run)"`
	Name        string `json:"name" jsonschema:"nome do projeto (até 120 caracteres)"`
	Slug        string `json:"slug,omitempty" jsonschema:"identificador curto; vazio = derivado do nome. Se já existe um projeto com esse slug, ele é devolvido (created=false)"`
	Description string `json:"description,omitempty" jsonschema:"descrição em markdown: objetivo e escopo"`
	Workspace   string `json:"workspace,omitempty" jsonschema:"slug do workspace padrão do projeto"`
	BudgetCents *int64 `json:"budget_monthly_cents,omitempty" jsonschema:"teto mensal em centavos de dólar; vazio = sem teto"`
}

type projectCreateOut struct {
	Project projectOut `json:"project"`
	Created bool       `json:"created" jsonschema:"false quando o slug já existia e o projeto existente foi devolvido"`
}

type goalCreateIn struct {
	Company     string   `json:"company,omitempty" jsonschema:"slug ou id da empresa (obrigatório só sem token de run)"`
	Title       string   `json:"title" jsonschema:"o que a meta quer mover, curto (ex. Reduzir o tempo de resposta)"`
	Slug        string   `json:"slug,omitempty" jsonschema:"identificador curto; vazio = derivado do título. Se já existe uma meta com esse slug, ela é devolvida (created=false)"`
	Description string   `json:"description,omitempty" jsonschema:"descrição em markdown"`
	Metric      string   `json:"metric,omitempty" jsonschema:"o que se mede (ex. p95 em ms)"`
	Unit        string   `json:"unit,omitempty" jsonschema:"unidade (ex. ms, %, US$)"`
	StartValue  float64  `json:"start_value" jsonschema:"valor inicial da métrica; precisa ser diferente de target_value"`
	TargetValue float64  `json:"target_value" jsonschema:"valor alvo; pode ser menor que o inicial (meta de redução)"`
	CurrentVal  *float64 `json:"current_value,omitempty" jsonschema:"valor atual, se já medido; vazio = o inicial"`
	DueDate     string   `json:"due_date,omitempty" jsonschema:"prazo, AAAA-MM-DD"`
	Project     string   `json:"project,omitempty" jsonschema:"slug do projeto da meta"`
	Owner       string   `json:"owner,omitempty" jsonschema:"slug da persona dona da meta, ou 'me' para você; vazio = o time humano"`
}

type goalCreateOut struct {
	Goal    goalOut `json:"goal"`
	Created bool    `json:"created" jsonschema:"false quando o slug já existia e a meta existente foi devolvida"`
}

func registerProjectGoalTools(s *sdk.Server, t *tools) {
	if _, ok := t.b.(projectGoalBackend); !ok {
		return
	}
	sdk.AddTool(s, &sdk.Tool{
		Name: "hanko_project_create",
		Description: "Cria um projeto (agrupa tasks, tem workspace e orçamento opcionais). Use quando o trabalho pedido merece um projeto próprio; confira antes com hanko_project_list. " +
			"Repetir o mesmo slug devolve o projeto existente. Funciona também numa conversa de chat.",
	}, t.projectCreate)
	sdk.AddTool(s, &sdk.Tool{
		Name: "hanko_goal_create",
		Description: "Cria uma meta mensurável (métrica, valor inicial, alvo e prazo opcional), opcionalmente de um projeto. Depois registre o valor medido com hanko_goal_update. " +
			"Repetir o mesmo slug devolve a meta existente. Funciona também numa conversa de chat.",
	}, t.goalCreate)
}

func (t *tools) projectGoalScope(ctx context.Context, company string) (projectGoalBackend, identity, domain.ID, error) {
	pb, ok := t.b.(projectGoalBackend)
	if !ok {
		return nil, identity{}, domain.ID{}, errors.New("criar projeto e meta indisponível neste servidor")
	}
	id, cid, err := t.scope(ctx, company)
	return pb, id, cid, err
}

func (t *tools) projectCreate(ctx context.Context, _ *sdk.CallToolRequest, in projectCreateIn) (*sdk.CallToolResult, projectCreateOut, error) {
	pb, id, cid, err := t.projectGoalScope(ctx, in.Company)
	if err != nil {
		return nil, projectCreateOut{}, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, projectCreateOut{}, errors.New("name é obrigatório")
	}
	slug := strings.TrimSpace(in.Slug)
	if slug == "" {
		slug = domain.Slugify(name)
	}
	if ex, err := t.b.GetProject(ctx, cid, slug); err == nil {
		return textResult("Projeto %s já existe (%s); nada foi criado.", ex.Slug, ex.Name),
			projectCreateOut{Project: projectOut{ID: ex.ID.String(), Slug: ex.Slug, Name: ex.Name, Description: ex.Description, Status: string(ex.Status)}}, nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return nil, projectCreateOut{}, opErr("consultar projeto", err)
	}
	ci := service.CreateProjectInput{Name: name, Slug: strings.TrimSpace(in.Slug), Description: in.Description, BudgetMonthlyCents: in.BudgetCents}
	if w := strings.TrimSpace(in.Workspace); w != "" {
		ws, err := pb.GetWorkspace(ctx, cid, w)
		if err != nil {
			return nil, projectCreateOut{}, toolErr("workspace", w, err)
		}
		ci.WorkspaceID = &ws.ID
	}
	p, err := pb.CreateProject(ctx, id.Actor, cid, ci)
	if err != nil {
		return nil, projectCreateOut{}, opErr("criar projeto", err)
	}
	return textResult("Projeto %s criado (%s).", p.Slug, p.Name),
		projectCreateOut{Created: true, Project: projectOut{ID: p.ID.String(), Slug: p.Slug, Name: p.Name, Description: p.Description, Status: string(p.Status), BudgetState: "none"}}, nil
}

func goalOutOf(g domain.Goal) goalOut {
	o := goalOut{Slug: g.Slug, Title: g.Title, Metric: g.Metric, Unit: g.Unit, Start: g.StartValue, Target: g.TargetValue, Current: g.CurrentValue,
		Progress: domain.GoalProgress(g.StartValue, g.TargetValue, g.CurrentValue) * 100, Status: string(g.Status)}
	if g.DueDate != nil {
		o.DueDate = g.DueDate.Format("2006-01-02")
	}
	return o
}

func (t *tools) goalCreate(ctx context.Context, _ *sdk.CallToolRequest, in goalCreateIn) (*sdk.CallToolResult, goalCreateOut, error) {
	pb, id, cid, err := t.projectGoalScope(ctx, in.Company)
	if err != nil {
		return nil, goalCreateOut{}, err
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return nil, goalCreateOut{}, errors.New("title é obrigatório")
	}
	slug := strings.TrimSpace(in.Slug)
	if slug == "" {
		slug = domain.Slugify(title)
	}
	if ex, err := t.b.GetGoal(ctx, cid, slug); err == nil {
		return textResult("Meta %s já existe (%s); nada foi criada.", ex.Slug, ex.Title), goalCreateOut{Goal: goalOutOf(ex)}, nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return nil, goalCreateOut{}, opErr("consultar meta", err)
	}
	ci := service.CreateGoalInput{Title: title, Slug: strings.TrimSpace(in.Slug), Description: in.Description, Metric: in.Metric, Unit: in.Unit,
		Start: in.StartValue, Target: in.TargetValue, Current: in.CurrentVal}
	if d := strings.TrimSpace(in.DueDate); d != "" {
		due, err := time.Parse("2006-01-02", d)
		if err != nil {
			return nil, goalCreateOut{}, errors.New("due_date deve ser AAAA-MM-DD")
		}
		ci.DueDate = &due
	}
	if p := strings.TrimSpace(in.Project); p != "" {
		pr, err := t.b.GetProject(ctx, cid, p)
		if err != nil {
			return nil, goalCreateOut{}, toolErr("projeto", p, err)
		}
		ci.ProjectID = &pr.ID
	}
	switch o := strings.TrimSpace(in.Owner); o {
	case "":
	case "me":
		if id.PersonaID == nil {
			return nil, goalCreateOut{}, errors.New("owner 'me' só vale com token de run")
		}
		ci.OwnerPersonaID = id.PersonaID
	default:
		pe, err := t.b.GetPersona(ctx, cid, o)
		if err != nil {
			return nil, goalCreateOut{}, toolErr("persona", o, err)
		}
		ci.OwnerPersonaID = &pe.ID
	}
	g, err := pb.CreateGoal(ctx, id.Actor, cid, ci)
	if err != nil {
		return nil, goalCreateOut{}, opErr("criar meta", err)
	}
	return textResult("Meta %s criada (%s).", g.Slug, g.Title), goalCreateOut{Created: true, Goal: goalOutOf(g)}, nil
}
