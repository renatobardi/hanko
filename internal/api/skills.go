package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Skills (ADR-0029): leitura de qualquer membro; escrita de admin (o service confere o papel).

// skillsBackend é o subconjunto do service para skills. Fica fora de backend para que os fakes dos testes
// unitários que não as usam continuem compilando; sem ele, as rotas respondem 501.
type skillsBackend interface {
	CreateSkill(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateSkillInput) (domain.Skill, error)
	UpdateSkill(ctx context.Context, actor domain.Actor, companyID, skillID domain.ID, in service.UpdateSkillInput) (domain.Skill, error)
	DeleteSkill(ctx context.Context, actor domain.Actor, companyID, skillID domain.ID) error
	ListSkills(ctx context.Context, companyID domain.ID) ([]service.SkillView, error)
	GetSkill(ctx context.Context, companyID domain.ID, ref string) (domain.Skill, error)
	SetPersonaSkill(ctx context.Context, actor domain.Actor, companyID, personaID, skillID domain.ID, enabled bool) error
	ListPersonaSkills(ctx context.Context, companyID, personaID domain.ID) ([]domain.PersonaSkill, error)
	ListSkillPersonas(ctx context.Context, companyID, skillID domain.ID) ([]domain.SkillPersona, error)
}

func (s *server) skills() (skillsBackend, error) {
	b, ok := s.svc.(skillsBackend)
	if !ok {
		return nil, problem(http.StatusNotImplemented, "skills indisponíveis neste servidor")
	}
	return b, nil
}

// Skill é uma habilidade reutilizável: um SKILL.md que as personas habilitadas carregam sob demanda.
type Skill struct {
	ID          string    `json:"id" format:"uuid"`
	CompanyID   string    `json:"company_id" format:"uuid"`
	Slug        string    `json:"slug" example:"revisar-pr" doc:"Nome da skill no harness (a pasta e o campo name do SKILL.md)."`
	Name        string    `json:"name" example:"Revisar PR"`
	Description string    `json:"description" doc:"Quando usar a skill; é o que o agente lê para decidir carregá-la."`
	Body        string    `json:"body,omitempty" doc:"Instruções em markdown, sem o frontmatter. Ausente nas listagens."`
	Version     int       `json:"version" doc:"Sobe a cada mudança de nome, descrição ou corpo."`
	Personas    *int      `json:"personas,omitempty" doc:"Quantas personas a têm habilitada (só nas listagens)."`
	CreatedBy   ActorRef  `json:"created_by"`
	UpdatedBy   ActorRef  `json:"updated_by"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// SkillPersona é o estado de uma persona para uma skill.
type SkillPersona struct {
	PersonaID string `json:"persona_id" format:"uuid"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
	// PinnedRevision é a versão fixada para a persona (ADR-0035); ausente = sempre a mais nova.
	PinnedRevision *int `json:"pinned_revision,omitempty"`
}

// PersonaSkill é o estado de uma skill para uma persona.
type PersonaSkill struct {
	SkillID     string `json:"skill_id" format:"uuid"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
	// Version é a versão atual; PinnedRevision, a fixada para a persona (ADR-0035).
	Version        int  `json:"version"`
	PinnedRevision *int `json:"pinned_revision,omitempty"`
	FromSource     bool `json:"from_source,omitempty" doc:"A skill veio de uma fonte git."`
}

func skillOut(k domain.Skill, withBody bool) Skill {
	o := Skill{ID: idStr(k.ID), CompanyID: idStr(k.CompanyID), Slug: k.Slug, Name: k.Name, Description: k.Description,
		Version: k.Version, CreatedBy: actorRef(k.CreatedBy), UpdatedBy: actorRef(k.UpdatedBy), CreatedAt: k.CreatedAt, UpdatedAt: k.UpdatedAt}
	if withBody {
		o.Body = k.Body
	}
	return o
}

func skillViewOut(v service.SkillView) Skill {
	o := skillOut(v.Skill, false)
	n := v.Personas
	o.Personas = &n
	return o
}

type SkillPath struct {
	CompanyPath
	Skill string `path:"skill" doc:"Id ou slug da skill." example:"revisar-pr"`
}

type SkillPersonaPath struct {
	SkillPath
	Persona string `path:"persona" doc:"Id ou slug da persona." example:"ana"`
}

type PersonaSkillPath struct {
	CompanyPath
	Persona string `path:"persona" doc:"Id ou slug da persona." example:"ana"`
	Skill   string `path:"skill" doc:"Id ou slug da skill." example:"revisar-pr"`
}

type EnabledBody struct {
	Enabled bool `json:"enabled" doc:"Liga ou desliga."`
	// PinnedRevision fixa a versão que a persona usa (ADR-0035): N ≥ 1 fixa, 0 solta (volta à mais nova); ausente
	// não muda.
	PinnedRevision *int `json:"pinned_revision,omitempty" minimum:"0" doc:"Versão fixada para a persona: N fixa, 0 solta; ausente não muda."`
}

type createSkillIn struct {
	CompanyPath
	Body struct {
		Name        string `json:"name,omitempty" maxLength:"120" example:"Revisar PR" doc:"Obrigatório, a menos que venha no frontmatter do body."`
		Slug        string `json:"slug,omitempty" pattern:"^[a-z0-9][a-z0-9-]{0,62}$" doc:"Derivado do nome quando omitido."`
		Description string `json:"description,omitempty" maxLength:"1000" doc:"Quando usar a skill. Pode vir do frontmatter do body."`
		BodyMD      string `json:"body" doc:"Instruções em markdown. Um SKILL.md inteiro (com frontmatter) é aceito: o frontmatter completa nome e descrição e não é gravado no corpo. Máximo de 32 KiB."`
	}
}

type updateSkillIn struct {
	SkillPath
	Body struct {
		Name        *string `json:"name,omitempty" maxLength:"120"`
		Description *string `json:"description,omitempty" maxLength:"1000"`
		BodyMD      *string `json:"body,omitempty" doc:"Substitui o corpo (máximo de 32 KiB)."`
	}
}

type listSkillsIn struct {
	CompanyPath
	Page
}

type personaSkillsIn struct {
	CompanyPath
	Persona string `path:"persona" doc:"Id ou slug da persona." example:"ana"`
}

type setEnabledSkillIn struct {
	PersonaSkillPath
	Body EnabledBody
}

type setSkillPersonaIn struct {
	SkillPersonaPath
	Body EnabledBody
}

// maxSkillRequestBytes é o teto do corpo de requisição das rotas que gravam uma skill: o corpo da skill
// (32 KiB) pode crescer até 6x ao ser escapado em JSON, mais os outros campos.
const maxSkillRequestBytes = 256 << 10

func capBody(o huma.Operation) huma.Operation { o.MaxBodyBytes = maxSkillRequestBytes; return o }

func (s *server) registerSkills(api huma.API) {
	huma.Register(api, capBody(created(op("create-skill", http.MethodPost, "/companies/{company}/skills", "skills", "Cria uma skill"))),
		func(ctx context.Context, in *createSkillIn) (*ItemOut[Skill], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sb, err := s.skills()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			b := in.Body
			k, err := sb.CreateSkill(ctx, sc.Actor, sc.Company.ID, service.CreateSkillInput{Name: b.Name, Slug: b.Slug, Description: b.Description, Body: b.BodyMD})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Skill]{Body: skillOut(k, true)}, nil
		})

	huma.Register(api, op("list-skills", http.MethodGet, "/companies/{company}/skills", "skills", "Lista as skills da empresa"),
		func(ctx context.Context, in *listSkillsIn) (*ListOut[Skill], error) {
			sb, err := s.skills()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			list, err := sb.ListSkills(ctx, sc.Company.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listOf(list, in.Page, skillViewOut), nil
		})

	huma.Register(api, op("get-skill", http.MethodGet, "/companies/{company}/skills/{skill}", "skills", "Obtém uma skill, com o corpo"),
		func(ctx context.Context, in *SkillPath) (*ItemOut[Skill], error) {
			sb, err := s.skills()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			k, err := sb.GetSkill(ctx, sc.Company.ID, in.Skill)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Skill]{Body: skillOut(k, true)}, nil
		})

	huma.Register(api, capBody(op("update-skill", http.MethodPatch, "/companies/{company}/skills/{skill}", "skills", "Atualiza uma skill (a versão sobe)")),
		func(ctx context.Context, in *updateSkillIn) (*ItemOut[Skill], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sb, err := s.skills()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			cur, err := sb.GetSkill(ctx, sc.Company.ID, in.Skill)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			b := in.Body
			k, err := sb.UpdateSkill(ctx, sc.Actor, sc.Company.ID, cur.ID, service.UpdateSkillInput{Name: b.Name, Description: b.Description, Body: b.BodyMD})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Skill]{Body: skillOut(k, true)}, nil
		})

	huma.Register(api, func() huma.Operation {
		o := op("delete-skill", http.MethodDelete, "/companies/{company}/skills/{skill}", "skills", "Apaga uma skill (e os vínculos com personas)")
		o.DefaultStatus = http.StatusNoContent
		return o
	}(), func(ctx context.Context, in *SkillPath) (*struct{}, error) {
		if err := forbidPersona(ctx); err != nil {
			return nil, err
		}
		sb, err := s.skills()
		if err != nil {
			return nil, err
		}
		sc, err := s.scope(ctx, in.Company)
		if err != nil {
			return nil, err
		}
		cur, err := sb.GetSkill(ctx, sc.Company.ID, in.Skill)
		if err != nil {
			return nil, s.fail(ctx, err)
		}
		if err := sb.DeleteSkill(ctx, sc.Actor, sc.Company.ID, cur.ID); err != nil {
			return nil, s.fail(ctx, err)
		}
		return &struct{}{}, nil
	})

	huma.Register(api, op("list-skill-personas", http.MethodGet, "/companies/{company}/skills/{skill}/personas", "skills", "Lista as personas com o estado da skill (habilitada ou não)"),
		func(ctx context.Context, in *SkillPath) (*ListOut[SkillPersona], error) {
			sb, err := s.skills()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			cur, err := sb.GetSkill(ctx, sc.Company.ID, in.Skill)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			list, err := sb.ListSkillPersonas(ctx, sc.Company.ID, cur.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(list, Page{Limit: len(list)}, func(p domain.SkillPersona) SkillPersona {
				return SkillPersona{PersonaID: idStr(p.PersonaID), Slug: p.Slug, Name: p.Name, Enabled: p.Enabled, PinnedRevision: p.PinnedRevision}
			}), nil
		})

	huma.Register(api, noContent(op("set-skill-persona", http.MethodPut, "/companies/{company}/skills/{skill}/personas/{persona}", "skills", "Liga ou desliga a skill para uma persona")),
		func(ctx context.Context, in *setSkillPersonaIn) (*struct{}, error) {
			return s.setPersonaSkill(ctx, in.CompanyPath, in.Persona, in.Skill, in.Body)
		})

	huma.Register(api, op("list-persona-skills", http.MethodGet, "/companies/{company}/personas/{persona}/skills", "skills", "Lista as skills com o estado para a persona"),
		func(ctx context.Context, in *personaSkillsIn) (*ListOut[PersonaSkill], error) {
			sb, err := s.skills()
			if err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			p, err := s.svc.GetPersona(ctx, sc.Company.ID, in.Persona)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			list, err := sb.ListPersonaSkills(ctx, sc.Company.ID, p.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(list, Page{Limit: len(list)}, func(k domain.PersonaSkill) PersonaSkill {
				return PersonaSkill{SkillID: idStr(k.SkillID), Slug: k.Slug, Name: k.Name, Description: k.Description, Enabled: k.Enabled,
					Version: k.Version, PinnedRevision: k.PinnedRevision, FromSource: k.FromSource}
			}), nil
		})

	huma.Register(api, noContent(op("set-persona-skill", http.MethodPut, "/companies/{company}/personas/{persona}/skills/{skill}", "skills", "Liga ou desliga uma skill para a persona")),
		func(ctx context.Context, in *setEnabledSkillIn) (*struct{}, error) {
			return s.setPersonaSkill(ctx, in.CompanyPath, in.Persona, in.Skill, in.Body)
		})
}

func (s *server) setPersonaSkill(ctx context.Context, cp CompanyPath, personaRef, skillRef string, body EnabledBody) (*struct{}, error) {
	if err := forbidPersona(ctx); err != nil {
		return nil, err
	}
	sb, err := s.skills()
	if err != nil {
		return nil, err
	}
	sc, err := s.scope(ctx, cp.Company)
	if err != nil {
		return nil, err
	}
	p, err := s.svc.GetPersona(ctx, sc.Company.ID, personaRef)
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	k, err := sb.GetSkill(ctx, sc.Company.ID, skillRef)
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	if err := sb.SetPersonaSkill(ctx, sc.Actor, sc.Company.ID, p.ID, k.ID, body.Enabled); err != nil {
		return nil, s.fail(ctx, err)
	}
	if body.PinnedRevision != nil { // versão fixada (ADR-0035)
		st, err := s.studio()
		if err != nil {
			return nil, err
		}
		var rev *int
		if *body.PinnedRevision > 0 {
			rev = body.PinnedRevision
		}
		if err := st.SetPersonaSkillPin(ctx, sc.Actor, sc.Company.ID, p.ID, k.ID, rev); err != nil {
			return nil, s.fail(ctx, err)
		}
	}
	return &struct{}{}, nil
}

func noContent(o huma.Operation) huma.Operation { o.DefaultStatus = http.StatusNoContent; return o }
