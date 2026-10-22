package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Fork de skill e as revisões de skill de uma run (ADR-0044). Bifurcar é de admin; ler as revisões é de qualquer membro.

type skillForkBackend interface {
	ForkSkill(ctx context.Context, actor domain.Actor, companyID, skillID domain.ID, in service.ForkSkillInput) (domain.Skill, error)
	ListSkillForks(ctx context.Context, companyID, skillID domain.ID) ([]domain.Skill, error)
	ListRunSkills(ctx context.Context, companyID, runID domain.ID) ([]service.RunSkill, error)
}

func (s *server) skillForks() (skillForkBackend, error) {
	b, ok := s.svc.(skillForkBackend)
	if !ok {
		return nil, problem(http.StatusNotImplemented, "fork de skills indisponível neste servidor")
	}
	return b, nil
}

// RunSkill é uma skill que uma execução recebeu, com a revisão efetiva.
type RunSkill struct {
	Slug     string `json:"slug"`
	Revision int    `json:"revision" doc:"A versão da skill que a run recebeu (a fixada para a persona, senão a atual no despacho)."`
	SHA256   string `json:"sha256" doc:"sha256 do corpo naquela revisão."`
	Pinned   bool   `json:"pinned" doc:"A revisão estava fixada para a persona."`
}

type forkSkillIn struct {
	SkillPath
	Body struct {
		Slug         string `json:"slug,omitempty" doc:"Slug da cópia; vazio usa <slug>-fork."`
		Name         string `json:"name,omitempty" doc:"Nome da cópia; vazio mantém o da original."`
		Revision     int    `json:"revision,omitempty" minimum:"0" doc:"Revisão a copiar; 0 ou ausente = a atual."`
		MovePersonas bool   `json:"move_personas,omitempty" doc:"Passa as personas ligadas à original para a cópia (a original fica desligada nelas)."`
	}
}

func (s *server) registerSkillForks(api huma.API) {
	huma.Register(api, created(op("fork-skill", http.MethodPost, "/companies/{company}/skills/{skill}/fork", "skills",
		"Bifurca uma skill (por exemplo, uma de fonte) em uma cópia local editável; admin")),
		func(ctx context.Context, in *forkSkillIn) (*ItemOut[Skill], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			fb, err := s.skillForks()
			if err != nil {
				return nil, err
			}
			sk, err := s.skills()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			k, err := sk.GetSkill(ctx, sc.Company.ID, in.Skill)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			f, err := fb.ForkSkill(ctx, sc.Actor, sc.Company.ID, k.ID, service.ForkSkillInput{Slug: in.Body.Slug, Name: in.Body.Name,
				Revision: in.Body.Revision, MovePersonas: in.Body.MovePersonas})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Skill]{Body: skillOut(f, true)}, nil
		})

	huma.Register(api, op("list-skill-forks", http.MethodGet, "/companies/{company}/skills/{skill}/forks", "skills", "Lista as cópias locais de uma skill"),
		func(ctx context.Context, in *SkillPath) (*ListOut[Skill], error) {
			fb, err := s.skillForks()
			if err != nil {
				return nil, err
			}
			sk, err := s.skills()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			k, err := sk.GetSkill(ctx, sc.Company.ID, in.Skill)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			list, err := fb.ListSkillForks(ctx, sc.Company.ID, k.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(list, Page{Limit: len(list)}, func(x domain.Skill) Skill { return skillOut(x, false) }), nil
		})

	huma.Register(api, op("list-run-skills", http.MethodGet, "/companies/{company}/runs/{run}/skills", "runs",
		"As skills (e revisões) que a execução recebeu"),
		func(ctx context.Context, in *RunPath) (*ListOut[RunSkill], error) {
			fb, err := s.skillForks()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			rid, err := s.runID(in.Run)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			if _, err := s.svc.GetRun(ctx, sc.Company.ID, rid); err != nil {
				return nil, s.fail(ctx, err)
			}
			list, err := fb.ListRunSkills(ctx, sc.Company.ID, rid)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(list, Page{Limit: len(list)}, func(x service.RunSkill) RunSkill {
				return RunSkill{Slug: x.Slug, Revision: x.Revision, SHA256: x.SHA256, Pinned: x.Pinned}
			}), nil
		})
}
