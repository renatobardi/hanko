package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Tags de persona e recorte do organograma (ADR-0087, #118). A autorização fica no service (item 4): viewer lê, member
// cria, edita, aplica e tira, admin arquiva, desarquiva e deleta; run token não escreve tag (item 9). Toda escrita gera
// `activity_event` na mesma transação, com os verbos de `domain.VerbPersonaTag*` (item 12).

// ---------- recursos ----------

// PersonaTag é uma tag do cadastro da empresa (ADR-0087 itens 1 a 3).
type PersonaTag struct {
	ID           string     `json:"id" format:"uuid"`
	Name         string     `json:"name" minLength:"1" maxLength:"40" example:"Revisão de código" doc:"Único na empresa sem distinguir maiúscula de minúscula."`
	Color        string     `json:"color" enum:"gray,red,amber,green,teal,blue,violet,pink" doc:"Nome da paleta fechada; a UI resolve o token de cada tema."`
	Description  string     `json:"description,omitempty" maxLength:"200" doc:"Aparece no tooltip do chip."`
	Archived     bool       `json:"archived" doc:"Arquivada: continua nas personas, esmaecida, e não se aplica a mais nenhuma."`
	ArchivedAt   *time.Time `json:"archived_at,omitempty"`
	PersonaCount int        `json:"persona_count" doc:"Personas com a tag; é o número que a confirmação de deletar mostra."`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// PersonaTagRef é a tag como aparece numa persona: o chip.
type PersonaTagRef struct {
	ID          string `json:"id" format:"uuid"`
	Name        string `json:"name"`
	Color       string `json:"color" enum:"gray,red,amber,green,teal,blue,violet,pink"`
	Description string `json:"description,omitempty"`
	Archived    bool   `json:"archived" doc:"O chip aparece esmaecido."`
}

// OrgPersona é uma persona do recorte do organograma, com o nível calculado (ADR-0087 item 6).
type OrgPersona struct {
	Persona Persona         `json:"persona"`
	Level   int             `json:"level" minimum:"1" doc:"1 na raiz; o nível do superior mais um. Calculado, não gravado."`
	Tags    []PersonaTagRef `json:"tags"`
}

// ---------- entradas ----------

type PersonaTagPath struct {
	CompanyPath
	Tag string `path:"tag" format:"uuid" doc:"Id da tag."`
}

type PersonaTagLinkPath struct {
	PersonaPath
	Tag string `path:"tag" format:"uuid" doc:"Id da tag."`
}

type listPersonaTagsIn struct {
	CompanyPath
	Page
	Archived bool `query:"archived" doc:"Inclui as arquivadas (\"mostrar arquivadas\")."`
}

type createPersonaTagIn struct {
	CompanyPath
	Body struct {
		Name        string `json:"name" minLength:"1" maxLength:"40" doc:"Apara as pontas; acento e espaço no meio valem."`
		Color       string `json:"color,omitempty" enum:"gray,red,amber,green,teal,blue,violet,pink" default:"gray"`
		Description string `json:"description,omitempty" maxLength:"200"`
	}
}

type updatePersonaTagIn struct {
	PersonaTagPath
	Body struct {
		Name        *string `json:"name,omitempty" minLength:"1" maxLength:"40" doc:"Renomear mantém o id e as ligações."`
		Color       *string `json:"color,omitempty" enum:"gray,red,amber,green,teal,blue,violet,pink"`
		Description *string `json:"description,omitempty" maxLength:"200"`
	}
}

type createAndTagPersonaIn struct {
	PersonaPath
	Body struct {
		Name  string `json:"name" minLength:"1" maxLength:"40" doc:"Nome da tag. Se já existe (sem distinguir caixa) e está ativa, só aplica; arquivada, recusa."`
		Color string `json:"color,omitempty" enum:"gray,red,amber,green,teal,blue,violet,pink" default:"gray" doc:"Cor da tag nova; ignorada se a tag já existe."`
	}
}

type listOrgPersonasIn struct {
	CompanyPath
	Page
	Tag      string `query:"tag" doc:"Ids de tag separados por vírgula." example:"0190f0c0-0000-7000-8000-000000000001"`
	Match    string `query:"match" enum:"any,all" default:"any" doc:"any: tem alguma das tags (OU); all: tem todas (E)."`
	Untagged bool   `query:"untagged" doc:"Só personas sem tag. Combina por OU com as tags escolhidas."`
	Level    string `query:"level" doc:"Níveis separados por vírgula (1 = raiz)." example:"1,2"`
	MaxDepth int    `query:"max_depth" minimum:"0" doc:"Corta a árvore abaixo deste nível antes dos outros critérios; 0 = sem corte."`
	Archived bool   `query:"archived" doc:"Aceita tags arquivadas no filtro."`
}

// ---------- conversões ----------

func personaTagOut(v service.PersonaTagView) PersonaTag {
	return PersonaTag{ID: idStr(v.ID), Name: v.Name, Color: string(v.Color), Description: v.Description, Archived: v.Archived(),
		ArchivedAt: v.ArchivedAt, PersonaCount: v.PersonaCount, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
}

func personaTagRefOut(t domain.PersonaTag) PersonaTagRef {
	return PersonaTagRef{ID: idStr(t.ID), Name: t.Name, Color: string(t.Color), Description: t.Description, Archived: t.Archived()}
}

func personaTagRefsOut(ts []domain.PersonaTag) []PersonaTagRef {
	if len(ts) == 0 {
		return nil
	}
	out := make([]PersonaTagRef, len(ts))
	for i, t := range ts {
		out[i] = personaTagRefOut(t)
	}
	return out
}

func orgPersonaOut(p service.OrgPersona) OrgPersona {
	tags := personaTagRefsOut(p.Persona.Tags)
	if tags == nil {
		tags = []PersonaTagRef{}
	}
	return OrgPersona{Persona: personaOut(p.Persona), Level: p.Level, Tags: tags}
}

// orgFilterIn converte os parâmetros do recorte. Id de tag ou nível malformado é erro de validação.
func orgFilterIn(in *listOrgPersonasIn) (service.OrgPersonaFilter, error) {
	f := service.OrgPersonaFilter{Match: domain.OrgTagMatch(in.Match), Untagged: in.Untagged, MaxDepth: in.MaxDepth, IncludeArchived: in.Archived}
	for _, part := range splitList(in.Tag) {
		id, err := uuid.Parse(part)
		if err != nil {
			return f, domain.Invalid("tag", fmt.Sprintf("tag id %q is not a uuid", part))
		}
		f.TagIDs = append(f.TagIDs, id)
	}
	for _, part := range splitList(in.Level) {
		n, err := strconv.Atoi(part)
		if err != nil {
			return f, domain.Invalid("level", fmt.Sprintf("level %q is not a number", part))
		}
		f.Levels = append(f.Levels, n)
	}
	return f, nil
}

// tagIDOf lê o id da tag do caminho. O esquema já exige uuid; um valor fora disso vira o id zero, que não existe.
func tagIDOf(ref string) domain.ID {
	id, _ := uuid.Parse(ref)
	return id
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// ---------- registro ----------

const (
	tagsOpTag      = "personas"
	tagsCompanyDir = "/companies/{company}"
)

func (s *server) registerPersonaTags(api huma.API) {
	s.registerTagRegistry(api)
	s.registerTagLinks(api)
}

// registerTagRegistry registra o cadastro: listar, criar, obter, editar, arquivar, desarquivar e deletar.
func (s *server) registerTagRegistry(api huma.API) {
	const tag, b = tagsOpTag, tagsCompanyDir
	one := b + "/persona-tags/{tag}"

	huma.Register(api, op("list-persona-tags", http.MethodGet, b+"/persona-tags", tag, "Lista o cadastro de tags de persona, sem as arquivadas por padrão"),
		func(ctx context.Context, in *listPersonaTagsIn) (*ListOut[PersonaTag], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			vs, err := s.svc.ListPersonaTags(ctx, sc.Company.ID, in.Archived)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listOf(vs, in.Page, personaTagOut), nil
		})

	huma.Register(api, created(op("create-persona-tag", http.MethodPost, b+"/persona-tags", tag, "Cria uma tag de persona (member; até 200 por empresa, contando as arquivadas)")),
		func(ctx context.Context, in *createPersonaTagIn) (*ItemOut[PersonaTag], error) {
			sc, err := s.tagWriteScope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			v, err := s.svc.CreatePersonaTag(ctx, sc.Actor, sc.Company.ID, service.PersonaTagInput{Name: in.Body.Name,
				Color: domain.PersonaTagColor(in.Body.Color), Description: in.Body.Description})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[PersonaTag]{Body: personaTagOut(v)}, nil
		})

	huma.Register(api, op("get-persona-tag", http.MethodGet, one, tag, "Obtém uma tag, com a contagem de personas"),
		func(ctx context.Context, in *PersonaTagPath) (*ItemOut[PersonaTag], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			v, err := s.svc.GetPersonaTag(ctx, sc.Company.ID, tagIDOf(in.Tag))
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[PersonaTag]{Body: personaTagOut(v)}, nil
		})

	huma.Register(api, op("update-persona-tag", http.MethodPatch, one, tag, "Edita nome, cor ou descrição de uma tag (member)"),
		func(ctx context.Context, in *updatePersonaTagIn) (*ItemOut[PersonaTag], error) {
			sc, err := s.tagWriteScope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			up := service.UpdatePersonaTagInput{Name: in.Body.Name, Description: in.Body.Description}
			if in.Body.Color != nil {
				c := domain.PersonaTagColor(*in.Body.Color)
				up.Color = &c
			}
			v, err := s.svc.UpdatePersonaTag(ctx, sc.Actor, sc.Company.ID, tagIDOf(in.Tag), up)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[PersonaTag]{Body: personaTagOut(v)}, nil
		})

	archive := func(fn func(context.Context, domain.Actor, domain.ID, domain.ID) (service.PersonaTagView, error)) func(context.Context, *PersonaTagPath) (*ItemOut[PersonaTag], error) {
		return func(ctx context.Context, in *PersonaTagPath) (*ItemOut[PersonaTag], error) {
			sc, err := s.tagWriteScope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			v, err := fn(ctx, sc.Actor, sc.Company.ID, tagIDOf(in.Tag))
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[PersonaTag]{Body: personaTagOut(v)}, nil
		}
	}
	huma.Register(api, op("archive-persona-tag", http.MethodPost, one+"/archive", tag, "Arquiva uma tag (admin; se desfaz)"), archive(func(ctx context.Context, a domain.Actor, c, t domain.ID) (service.PersonaTagView, error) {
		return s.svc.ArchivePersonaTag(ctx, a, c, t)
	}))
	huma.Register(api, op("unarchive-persona-tag", http.MethodPost, one+"/unarchive", tag, "Desarquiva uma tag (admin)"), archive(func(ctx context.Context, a domain.Actor, c, t domain.ID) (service.PersonaTagView, error) {
		return s.svc.UnarchivePersonaTag(ctx, a, c, t)
	}))

	huma.Register(api, noContent(op("delete-persona-tag", http.MethodDelete, one, tag, "Deleta uma tag e tira de todas as personas (admin; não se desfaz)")),
		func(ctx context.Context, in *PersonaTagPath) (*struct{}, error) {
			sc, err := s.tagWriteScope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			if _, err := s.svc.DeletePersonaTag(ctx, sc.Actor, sc.Company.ID, tagIDOf(in.Tag)); err != nil {
				return nil, s.fail(ctx, err)
			}
			return nil, nil
		})

}

// registerTagLinks registra aplicar, criar e aplicar, tirar e o recorte do organograma.
func (s *server) registerTagLinks(api huma.API) {
	const tag, b = tagsOpTag, tagsCompanyDir
	link := func(fn func(context.Context, domain.Actor, domain.ID, domain.ID, domain.ID) error) func(context.Context, *PersonaTagLinkPath) (*struct{}, error) {
		return func(ctx context.Context, in *PersonaTagLinkPath) (*struct{}, error) {
			sc, err := s.tagWriteScope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			p, err := s.svc.GetPersona(ctx, sc.Company.ID, in.Persona)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			if err := fn(ctx, sc.Actor, sc.Company.ID, p.ID, tagIDOf(in.Tag)); err != nil {
				return nil, s.fail(ctx, err)
			}
			return nil, nil
		}
	}
	huma.Register(api, noContent(op("tag-persona", http.MethodPut, b+"/personas/{persona}/tags/{tag}", tag,
		"Aplica uma tag ativa a uma persona (member; até 20 por persona; aplicar de novo não muda nada)")), link(func(ctx context.Context, a domain.Actor, c, p, t domain.ID) error {
		return s.svc.TagPersona(ctx, a, c, p, t)
	}))
	huma.Register(api, noContent(op("untag-persona", http.MethodDelete, b+"/personas/{persona}/tags/{tag}", tag,
		"Tira uma tag de uma persona (member; vale também para tag arquivada)")), link(func(ctx context.Context, a domain.Actor, c, p, t domain.ID) error {
		return s.svc.UntagPersona(ctx, a, c, p, t)
	}))

	huma.Register(api, created(op("create-and-tag-persona", http.MethodPost, b+"/personas/{persona}/tags", tag,
		"Cria a tag pelo nome, se não existe, e aplica à persona numa transação só (member)")),
		func(ctx context.Context, in *createAndTagPersonaIn) (*ItemOut[PersonaTagRef], error) {
			sc, err := s.tagWriteScope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			p, err := s.svc.GetPersona(ctx, sc.Company.ID, in.Persona)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			t, err := s.svc.CreateAndTagPersona(ctx, sc.Actor, sc.Company.ID, p.ID, service.PersonaTagInput{Name: in.Body.Name, Color: domain.PersonaTagColor(in.Body.Color)})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[PersonaTagRef]{Body: personaTagRefOut(t)}, nil
		})

	huma.Register(api, op("list-org-personas", http.MethodGet, b+"/org-chart/personas", tag,
		"Lista as personas do recorte do organograma por tag e por nível, com o nível de cada uma"),
		func(ctx context.Context, in *listOrgPersonasIn) (*ListOut[OrgPersona], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			f, err := orgFilterIn(in)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			ps, err := s.svc.ListOrgPersonas(ctx, sc.Company.ID, f)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listOf(ps, in.Page, orgPersonaOut), nil
		})
}

// tagWriteScope resolve a empresa de uma escrita de tag; run token não escreve (ADR-0087 item 9).
func (s *server) tagWriteScope(ctx context.Context, company string) (scope, error) {
	if err := forbidPersona(ctx); err != nil {
		return scope{}, err
	}
	return s.scope(ctx, company)
}
