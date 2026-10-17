package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/renatobardi/hanko/internal/domain"
)

// skillBackend é o que as tools de skill pedem ao service. Fica fora de Backend para que fakes que não
// usam skills continuem compilando; sem ele as tools respondem que o recurso está indisponível.
type skillBackend interface {
	PersonaSkill(ctx context.Context, companyID, personaID domain.ID, slug string) (domain.Skill, error)
	PersonaSkills(ctx context.Context, companyID, personaID domain.ID) ([]domain.Skill, error)
}

type skillListIn struct {
	Company string `json:"company,omitempty" jsonschema:"slug ou id da empresa (obrigatório só sem token de run)"`
}

type skillGetIn struct {
	Company string `json:"company,omitempty" jsonschema:"slug ou id da empresa (obrigatório só sem token de run)"`
	Slug    string `json:"slug" jsonschema:"slug da skill, como aparece no índice de skills ou em hanko_skill_list"`
}

type skillOut struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     int    `json:"version"`
	Body        string `json:"body,omitempty" jsonschema:"instruções completas em markdown (só em hanko_skill_get)"`
}

type skillListOut struct {
	Skills []skillOut `json:"skills"`
}

type skillGetOut struct {
	Skill skillOut `json:"skill"`
}

func registerSkillTools(s *sdk.Server, t *tools) {
	sdk.AddTool(s, &sdk.Tool{
		Name: "hanko_skill_list",
		Description: "Lista as skills (habilidades reutilizáveis) habilitadas para você: slug e descrição. " +
			"Use quando o seu índice de skills não estiver à vista ou para conferir o que está disponível.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true},
	}, t.skillList)
	sdk.AddTool(s, &sdk.Tool{
		Name: "hanko_skill_get",
		Description: "Carrega as instruções completas de uma skill habilitada para você, pelo slug. " +
			"Chame antes de agir quando a descrição de uma skill bater com o trabalho, e siga o que ela diz. " +
			"Só devolve skills habilitadas para a sua persona.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true},
	}, t.skillGet)
}

// skillScope resolve a empresa e a persona da chamada: skills são por persona, então exigem token de run.
func (t *tools) skillScope(ctx context.Context, company string) (skillBackend, domain.ID, domain.ID, error) {
	sb, ok := t.b.(skillBackend)
	if !ok {
		return nil, domain.ID{}, domain.ID{}, errors.New("skills indisponíveis neste servidor")
	}
	id, cid, err := t.scope(ctx, company)
	if err != nil {
		return nil, domain.ID{}, domain.ID{}, err
	}
	if id.PersonaID == nil {
		return nil, domain.ID{}, domain.ID{}, errors.New("skills são por persona: use uma conexão com token de run")
	}
	return sb, cid, *id.PersonaID, nil
}

func toSkill(s domain.Skill, withBody bool) skillOut {
	o := skillOut{Slug: s.Slug, Name: s.Name, Description: s.Description, Version: s.Version}
	if withBody {
		o.Body = s.Body
	}
	return o
}

func (t *tools) skillList(ctx context.Context, _ *sdk.CallToolRequest, in skillListIn) (*sdk.CallToolResult, skillListOut, error) {
	sb, company, persona, err := t.skillScope(ctx, in.Company)
	if err != nil {
		return nil, skillListOut{}, err
	}
	var list []domain.Skill
	if spec, isTest, terr := testSpec(ctx, t.b); terr != nil {
		return nil, skillListOut{}, opErr("listar skills", terr)
	} else if isTest { // run de teste do estúdio: só a skill testada (ADR-0035)
		list = []domain.Skill{spec.Skill}
	} else if list, err = sb.PersonaSkills(ctx, company, persona); err != nil {
		return nil, skillListOut{}, opErr("listar skills", err)
	}
	out := skillListOut{Skills: make([]skillOut, 0, len(list))}
	var b strings.Builder
	fmt.Fprintf(&b, "%d skill(s) habilitada(s)", len(list))
	for _, s := range list {
		out.Skills = append(out.Skills, toSkill(s, false))
		fmt.Fprintf(&b, "\n- %s: %s", s.Slug, strings.Join(strings.Fields(s.Description), " "))
	}
	return textResult("%s", b.String()), out, nil
}

func (t *tools) skillGet(ctx context.Context, _ *sdk.CallToolRequest, in skillGetIn) (*sdk.CallToolResult, skillGetOut, error) {
	slug := strings.TrimSpace(in.Slug)
	if slug == "" {
		return nil, skillGetOut{}, errors.New("o argumento slug é obrigatório")
	}
	sb, company, persona, err := t.skillScope(ctx, in.Company)
	if err != nil {
		return nil, skillGetOut{}, err
	}
	var s domain.Skill
	if spec, isTest, terr := testSpec(ctx, t.b); terr != nil {
		err = terr
	} else if isTest { // run de teste do estúdio: só a skill testada, na versão do teste (ADR-0035)
		s, err = spec.Skill, nil
		if spec.Skill.Slug != slug {
			err = domain.ErrNotFound
		}
	} else {
		s, err = sb.PersonaSkill(ctx, company, persona, slug)
	}
	if errors.Is(err, domain.ErrNotFound) {
		return nil, skillGetOut{}, fmt.Errorf("skill %q não encontrada ou não habilitada para você; use hanko_skill_list", slug)
	}
	if err != nil {
		return nil, skillGetOut{}, opErr("carregar skill "+slug, err)
	}
	// O texto é o próprio corpo: é o que o modelo lê e segue.
	return textResult("# Skill %s (v%d)\n\n%s", s.Slug, s.Version, s.Body), skillGetOut{Skill: toSkill(s, true)}, nil
}
