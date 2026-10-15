package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Metas (ADR-0028). Criar, editar e arquivar é de admin (a barreira é o service); registrar progresso é de member ou
// da persona com run token; ler é de qualquer membro.

const dateLayout = "2006-01-02"

// Goal é uma meta com progresso e as tasks ligadas por status.
type Goal struct {
	ID           string  `json:"id" format:"uuid"`
	CompanyID    string  `json:"company_id" format:"uuid"`
	Slug         string  `json:"slug" example:"cem-usuarios"`
	Title        string  `json:"title" example:"Chegar a 100 usuários ativos"`
	Description  string  `json:"description,omitempty" doc:"Markdown."`
	Metric       string  `json:"metric,omitempty" example:"usuários ativos"`
	Unit         string  `json:"unit,omitempty" example:"usuários"`
	StartValue   float64 `json:"start_value"`
	TargetValue  float64 `json:"target_value" doc:"Pode ser menor que o inicial (meta de redução)."`
	CurrentValue float64 `json:"current_value"`
	// ProgressPercent é (atual − inicial) / (alvo − inicial), limitado a 0..100.
	ProgressPercent float64 `json:"progress_percent"`
	Status          string  `json:"status" enum:"active,achieved,abandoned"`
	DueDate         string  `json:"due_date,omitempty" format:"date" doc:"AAAA-MM-DD."`
	ProjectID       string  `json:"project_id,omitempty" format:"uuid"`
	ProjectSlug     string  `json:"project_slug,omitempty"`
	ProjectName     string  `json:"project_name,omitempty"`
	OwnerPersonaID  string  `json:"owner_persona_id,omitempty" format:"uuid"`
	OwnerSlug       string  `json:"owner_slug,omitempty"`
	OwnerName       string  `json:"owner_name,omitempty"`
	// Tasks conta as tasks ligadas à meta por status.
	Tasks      map[string]int `json:"tasks"`
	TasksTotal int            `json:"tasks_total"`
	CreatedBy  ActorRef       `json:"created_by"`
	AchievedAt *time.Time     `json:"achieved_at,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	// Updates só vem no detalhe: o histórico de progresso, do mais antigo para o mais novo (até 200).
	Updates []GoalUpdate `json:"updates,omitempty"`
}

// GoalUpdate é um registro de progresso.
type GoalUpdate struct {
	ID        string    `json:"id" format:"uuid"`
	Value     float64   `json:"value"`
	Note      string    `json:"note,omitempty"`
	Author    ActorRef  `json:"author"`
	CreatedAt time.Time `json:"created_at"`
}

type GoalPath struct {
	CompanyPath
	Goal string `path:"goal" doc:"Id ou slug da meta." example:"cem-usuarios"`
}

type listGoalsIn struct {
	CompanyPath
	Page
	Status  string `query:"status" enum:"active,achieved,abandoned" doc:"Filtra por status."`
	Project string `query:"project" doc:"Id ou slug do projeto."`
}

type createGoalIn struct {
	CompanyPath
	Body struct {
		Title       string   `json:"title" minLength:"1" maxLength:"200"`
		Slug        string   `json:"slug,omitempty" doc:"Derivado do título quando vazio."`
		Description string   `json:"description,omitempty"`
		Metric      string   `json:"metric,omitempty" maxLength:"120"`
		Unit        string   `json:"unit,omitempty" maxLength:"40"`
		Start       float64  `json:"start_value" doc:"Valor de partida."`
		Target      float64  `json:"target_value" doc:"Valor a atingir; diferente do inicial."`
		Current     *float64 `json:"current_value,omitempty" doc:"Valor atual; padrão o inicial."`
		DueDate     string   `json:"due_date,omitempty" format:"date" doc:"AAAA-MM-DD."`
		Project     string   `json:"project,omitempty" doc:"Id ou slug do projeto."`
		Owner       string   `json:"owner,omitempty" doc:"Id ou slug da persona dona da meta."`
	}
}

type updateGoalIn struct {
	GoalPath
	Body struct {
		Title        *string  `json:"title,omitempty" minLength:"1" maxLength:"200"`
		Description  *string  `json:"description,omitempty"`
		Metric       *string  `json:"metric,omitempty" maxLength:"120"`
		Unit         *string  `json:"unit,omitempty" maxLength:"40"`
		Start        *float64 `json:"start_value,omitempty"`
		Target       *float64 `json:"target_value,omitempty"`
		DueDate      string   `json:"due_date,omitempty" format:"date"`
		ClearDue     bool     `json:"clear_due,omitempty"`
		Project      string   `json:"project,omitempty"`
		ClearProject bool     `json:"clear_project,omitempty"`
		Owner        string   `json:"owner,omitempty"`
		ClearOwner   bool     `json:"clear_owner,omitempty"`
		Status       *string  `json:"status,omitempty" enum:"active,abandoned" doc:"active reabre; abandoned arquiva. achieved só acontece ao cruzar o alvo."`
	}
}

type goalProgressIn struct {
	GoalPath
	Body struct {
		Value float64 `json:"value" doc:"Valor atual da métrica."`
		Note  string  `json:"note,omitempty" maxLength:"500"`
	}
}

func goalOut(v service.GoalView) Goal {
	g := Goal{ID: idStr(v.ID), CompanyID: idStr(v.CompanyID), Slug: v.Slug, Title: v.Title, Description: v.Description,
		Metric: v.Metric, Unit: v.Unit, StartValue: v.StartValue, TargetValue: v.TargetValue, CurrentValue: v.CurrentValue,
		ProgressPercent: v.ProgressPercent, Status: string(v.Status), ProjectID: idPtr(v.ProjectID), ProjectSlug: v.ProjectSlug,
		ProjectName: v.ProjectName, OwnerPersonaID: idPtr(v.OwnerPersonaID), OwnerSlug: v.OwnerSlug, OwnerName: v.OwnerName,
		Tasks: map[string]int{}, TasksTotal: v.TasksTotal, CreatedBy: actorRef(v.CreatedBy), AchievedAt: v.AchievedAt,
		CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
	if v.DueDate != nil {
		g.DueDate = v.DueDate.Format(dateLayout)
	}
	for st, n := range v.Tasks {
		g.Tasks[string(st)] = n
	}
	for _, u := range v.Updates {
		g.Updates = append(g.Updates, GoalUpdate{ID: idStr(u.ID), Value: u.Value, Note: u.Note, Author: actorRef(u.Author), CreatedAt: u.CreatedAt})
	}
	return g
}

// parseDue lê AAAA-MM-DD; vazio = ausente.
func parseDue(field, v string) (*time.Time, error) {
	if v == "" {
		return nil, nil
	}
	t, err := time.Parse(dateLayout, v)
	if err != nil {
		return nil, domain.Invalid(field, "use AAAA-MM-DD")
	}
	return &t, nil
}

func (s *server) goalRef(ctx context.Context, cid domain.ID, ref string) (*domain.ID, error) {
	return optID(ctx, s, ref, func(ctx context.Context, r string) (domain.Goal, error) {
		return s.svc.GetGoal(ctx, cid, r)
	}, func(g domain.Goal) domain.ID { return g.ID })
}

func (s *server) registerGoals(api huma.API) {
	huma.Register(api, created(op("create-goal", http.MethodPost, "/companies/{company}/goals", "goals", "Cria uma meta (admin)")),
		func(ctx context.Context, in *createGoalIn) (*ItemOut[Goal], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			cid := sc.Company.ID
			b := in.Body
			due, err := parseDue("due_date", b.DueDate)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			project, err := s.projectRef(ctx, cid, b.Project)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			owner, err := s.personaRef(ctx, cid, b.Owner)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			g, err := s.svc.CreateGoal(ctx, sc.Actor, cid, service.CreateGoalInput{Title: b.Title, Slug: b.Slug, Description: b.Description,
				Metric: b.Metric, Unit: b.Unit, ProjectID: project, Start: b.Start, Target: b.Target, Current: b.Current,
				DueDate: due, OwnerPersonaID: owner})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			v, err := s.svc.GetGoalView(ctx, cid, g.ID.String())
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Goal]{Body: goalOut(v)}, nil
		})

	huma.Register(api, op("list-goals", http.MethodGet, "/companies/{company}/goals", "goals", "Lista metas com progresso e contagem de tasks"),
		func(ctx context.Context, in *listGoalsIn) (*ListOut[Goal], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			f := service.GoalFilter{Status: domain.GoalStatus(in.Status)}
			if in.Status != "" && !f.Status.Valid() {
				return nil, s.fail(ctx, domain.Invalid("status", "valor desconhecido"))
			}
			if f.ProjectID, err = s.projectRef(ctx, sc.Company.ID, in.Project); err != nil {
				return nil, s.fail(ctx, err)
			}
			vs, err := s.svc.ListGoalViews(ctx, sc.Company.ID, f)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listOf(vs, in.Page, goalOut), nil
		})

	huma.Register(api, op("get-goal", http.MethodGet, "/companies/{company}/goals/{goal}", "goals", "Obtém uma meta com o histórico de progresso"),
		func(ctx context.Context, in *GoalPath) (*ItemOut[Goal], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			v, err := s.svc.GetGoalView(ctx, sc.Company.ID, in.Goal)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Goal]{Body: goalOut(v)}, nil
		})

	huma.Register(api, op("update-goal", http.MethodPatch, "/companies/{company}/goals/{goal}", "goals", "Atualiza uma meta, reabre ou abandona (admin)"),
		func(ctx context.Context, in *updateGoalIn) (*ItemOut[Goal], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			cid := sc.Company.ID
			cur, err := s.svc.GetGoal(ctx, cid, in.Goal)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			b := in.Body
			up := service.UpdateGoalInput{Title: b.Title, Description: b.Description, Metric: b.Metric, Unit: b.Unit,
				Start: b.Start, Target: b.Target, ClearDue: b.ClearDue, ClearProject: b.ClearProject, ClearOwner: b.ClearOwner}
			if up.DueDate, err = parseDue("due_date", b.DueDate); err != nil {
				return nil, s.fail(ctx, err)
			}
			if up.ProjectID, err = s.projectRef(ctx, cid, b.Project); err != nil {
				return nil, s.fail(ctx, err)
			}
			if up.OwnerPersonaID, err = s.personaRef(ctx, cid, b.Owner); err != nil {
				return nil, s.fail(ctx, err)
			}
			if b.Status != nil {
				st := domain.GoalStatus(*b.Status)
				if !st.Valid() {
					return nil, s.fail(ctx, domain.Invalid("status", "valor desconhecido"))
				}
				up.Status = &st
			}
			if _, err := s.svc.UpdateGoal(ctx, sc.Actor, cid, cur.ID, up); err != nil {
				return nil, s.fail(ctx, err)
			}
			v, err := s.svc.GetGoalView(ctx, cid, cur.ID.String())
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Goal]{Body: goalOut(v)}, nil
		})

	huma.Register(api, created(op("record-goal-progress", http.MethodPost, "/companies/{company}/goals/{goal}/progress", "goals",
		"Registra o valor atual da meta (member ou persona); ao cruzar o alvo a meta é atingida")),
		func(ctx context.Context, in *goalProgressIn) (*ItemOut[Goal], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			cur, err := s.svc.GetGoal(ctx, sc.Company.ID, in.Goal)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			v, err := s.svc.RecordGoalProgress(ctx, sc.Actor, sc.Company.ID, cur.ID, in.Body.Value, in.Body.Note)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Goal]{Body: goalOut(v)}, nil
		})
}
