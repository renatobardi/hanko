package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Precedência do ambiente de execução (ADR-0055): persona > projeto > padrão da empresa > local. A decisão é tomada
// na criação da run e gravada nela (runs.environment_id/environment_source); o executor a obedece.

// envResolution é o resultado de resolver a precedência com os ambientes carregados.
type envResolution struct {
	choice   domain.EnvChoice
	env      *store.Environment // nil = local implícito
	warnings []string
}

// resolveEnvChoice carrega os três níveis (persona, projeto, padrão) e aplica domain.ChooseEnvironment.
func resolveEnvChoice(ctx context.Context, q *store.Queries, companyID, personaID uuid.UUID, projectID *uuid.UUID) (envResolution, error) {
	p, err := q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: personaID})
	if err != nil {
		return envResolution{}, mapErr(err, "get persona")
	}
	rows := map[uuid.UUID]store.Environment{}
	load := func(src domain.EnvSource, id *uuid.UUID) (domain.EnvCandidate, error) {
		c := domain.EnvCandidate{Source: src}
		if id == nil {
			return c, nil
		}
		r, err := q.GetEnvironmentByID(ctx, store.GetEnvironmentByIDParams{CompanyID: companyID, ID: *id})
		if err != nil {
			if isNoRows(err) {
				return c, nil // o ambiente sumiu (FK nulificada): equivale a não ter escolhido
			}
			return c, fmt.Errorf("get environment: %w", err)
		}
		rows[r.ID] = r
		c.ID, c.Active = &r.ID, r.Status == string(domain.EnvActive)
		return c, nil
	}
	pc, err := load(domain.EnvFromPersona, p.EnvironmentID)
	if err != nil {
		return envResolution{}, err
	}
	var projEnv *uuid.UUID
	if projectID != nil {
		pr, err := q.GetProjectByID(ctx, store.GetProjectByIDParams{CompanyID: companyID, ID: *projectID})
		if err != nil && !isNoRows(err) {
			return envResolution{}, fmt.Errorf("get project: %w", err)
		}
		if err == nil {
			projEnv = pr.EnvironmentID
		}
	}
	prc, err := load(domain.EnvFromProject, projEnv)
	if err != nil {
		return envResolution{}, err
	}
	dc := domain.EnvCandidate{Source: domain.EnvFromDefault}
	if d, err := q.GetDefaultEnvironment(ctx, companyID); err == nil {
		rows[d.ID] = d
		dc.ID, dc.Active = &d.ID, true
	} else if !isNoRows(err) {
		return envResolution{}, fmt.Errorf("get default environment: %w", err)
	}
	out := envResolution{choice: domain.ChooseEnvironment(pc, prc, dc)}
	for _, src := range out.choice.Skipped {
		name := "(sem nome)"
		for _, c := range []domain.EnvCandidate{pc, prc} {
			if c.Source == src && c.ID != nil {
				name = rows[*c.ID].Name
			}
		}
		out.warnings = append(out.warnings, fmt.Sprintf("o ambiente %q escolhido na %s está arquivado; seguindo a precedência", name, envSourceLabel(src)))
	}
	if out.choice.ID != nil {
		r := rows[*out.choice.ID]
		out.env = &r
	}
	return out, nil
}

func envSourceLabel(s domain.EnvSource) string {
	switch s {
	case domain.EnvFromPersona:
		return "persona"
	case domain.EnvFromProject:
		return "projeto"
	}
	return "empresa"
}

// recordRunEnvironment decide e grava o ambiente de uma run recém-criada, na mesma transação que a criou.
func (t *txc) recordRunEnvironment(run store.Run) (store.Run, error) {
	var projectID *uuid.UUID
	if run.TaskID != nil {
		task, err := t.q.GetTaskByID(t.ctx, store.GetTaskByIDParams{CompanyID: run.CompanyID, ID: *run.TaskID})
		if err != nil {
			return run, mapErr(err, "get task")
		}
		projectID = task.ProjectID
	}
	res, err := resolveEnvChoice(t.ctx, t.q, run.CompanyID, run.PersonaID, projectID)
	if err != nil {
		return run, err
	}
	row, err := t.q.SetRunEnvironment(t.ctx, store.SetRunEnvironmentParams{EnvironmentID: res.choice.ID, Source: string(res.choice.Source), CompanyID: run.CompanyID, ID: run.ID})
	if err != nil {
		return run, mapErr(err, "set run environment")
	}
	return row, nil
}

// RunEnvironment resolve o ambiente das runs da persona (sem projeto): o dela, senão o padrão da empresa, senão o
// local. Atalho de RunEnvironmentFor para quem não tem a run (testes, readiness).
func (s *Service) RunEnvironment(ctx context.Context, companyID, personaID domain.ID) (RunEnvironment, error) {
	res, err := resolveEnvChoice(ctx, s.read(), companyID, personaID, nil)
	if err != nil {
		return RunEnvironment{Source: string(domain.EnvFromLocal), Config: s.Environments}, err
	}
	return s.runEnvironmentOf(ctx, res)
}

// RunEnvironmentFor devolve o ambiente em que a run deve rodar. Vale a decisão gravada na criação da run; se aquele
// ambiente foi arquivado (ou a run é anterior ao M11), a precedência é resolvida de novo e o resultado é gravado na
// run, com aviso no log. Chamado pelo executor (sem identidade de usuário): não aplica papéis.
func (s *Service) RunEnvironmentFor(ctx context.Context, companyID domain.ID, run domain.Run, projectID *domain.ID) (RunEnvironment, error) {
	ctx, span := otel.Tracer("hanko/service").Start(ctx, "hanko.environment.resolve")
	defer span.End()
	q := s.read()
	var warnings []string
	if run.EnvironmentSource != "" {
		switch {
		case run.EnvironmentID == nil && run.EnvironmentSource == domain.EnvFromLocal:
			span.SetAttributes(attribute.String("hanko.environment.source", "local"))
			return RunEnvironment{Source: string(domain.EnvFromLocal), Config: s.Environments}, nil
		case run.EnvironmentID != nil:
			r, err := q.GetEnvironmentByID(ctx, store.GetEnvironmentByIDParams{CompanyID: companyID, ID: *run.EnvironmentID})
			if err == nil && r.Status == string(domain.EnvActive) {
				e := environmentFromRow(r)
				out := RunEnvironment{Environment: &e, Source: string(run.EnvironmentSource), Config: s.Environments}
				span.SetAttributes(attribute.String("hanko.environment.source", out.Source), attribute.String("hanko.environment.driver", string(e.Driver)))
				if out.Target, err = s.target(ctx, r); err != nil {
					return out, err
				}
				return out, nil
			}
			if err != nil && !isNoRows(err) {
				return RunEnvironment{}, fmt.Errorf("get environment: %w", err)
			}
			warnings = append(warnings, "o ambiente decidido para esta execução não está mais ativo; resolvendo a precedência de novo")
		}
	}
	res, err := resolveEnvChoice(ctx, q, companyID, run.PersonaID, projectID)
	if err != nil {
		return RunEnvironment{Source: string(domain.EnvFromLocal), Config: s.Environments}, err
	}
	res.warnings = append(warnings, res.warnings...)
	if run.EnvironmentSource != res.choice.Source || !sameID(run.EnvironmentID, res.choice.ID) {
		err := s.withTx(ctx, func(t *txc) error {
			if _, err := t.q.SetRunEnvironment(ctx, store.SetRunEnvironmentParams{EnvironmentID: res.choice.ID, Source: string(res.choice.Source), CompanyID: companyID, ID: run.ID}); err != nil {
				return mapErr(err, "set run environment")
			}
			return t.activity(domain.SystemActor, companyID, "run.environment_resolved", "run", run.ID,
				map[string]any{"source": res.choice.Source, "environment_id": res.choice.ID}, "")
		})
		if err != nil {
			return RunEnvironment{Source: string(domain.EnvFromLocal), Config: s.Environments}, err
		}
	}
	out, err := s.runEnvironmentOf(ctx, res)
	span.SetAttributes(attribute.String("hanko.environment.source", out.Source))
	return out, err
}

func (s *Service) runEnvironmentOf(ctx context.Context, res envResolution) (RunEnvironment, error) {
	out := RunEnvironment{Source: string(res.choice.Source), Config: s.Environments, Warnings: res.warnings}
	if res.env == nil {
		return out, nil
	}
	e := environmentFromRow(*res.env)
	out.Environment = &e
	var err error
	out.Target, err = s.target(ctx, *res.env)
	return out, err
}

// SetProjectEnvironment escolhe o ambiente das runs das tasks do projeto (nil = o padrão da empresa). A persona
// continua tendo precedência sobre o projeto.
func (s *Service) SetProjectEnvironment(ctx context.Context, actor domain.Actor, companyID, projectID domain.ID, envID *domain.ID) (domain.Project, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.Project{}, err
	}
	var out domain.Project
	err := s.withTx(ctx, func(t *txc) error {
		data := map[string]any{"environment": "default"}
		if envID != nil {
			e, err := t.q.GetEnvironmentByID(ctx, store.GetEnvironmentByIDParams{CompanyID: companyID, ID: *envID})
			if err != nil {
				return domain.Invalid("environment", "environment not found in company")
			}
			if e.Status != string(domain.EnvActive) {
				return domain.Invalid("environment", "the environment is archived")
			}
			data["environment"] = e.Slug
		}
		row, err := t.q.SetProjectEnvironment(ctx, store.SetProjectEnvironmentParams{EnvironmentID: envID, Now: t.now, CompanyID: companyID, ID: projectID})
		if err != nil {
			return mapErr(err, "set project environment")
		}
		out = projectFromRow(row)
		data["project"] = row.Slug
		return t.activity(actor, companyID, "project.environment_changed", "project", projectID, data, "")
	})
	return out, err
}
