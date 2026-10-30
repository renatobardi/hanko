package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

// Contrato das tags de persona e dos filtros do organograma (ADR-0087, #118). Cada operação já tem entrada e saída no
// OpenAPI e responde 501 até a fatia 1; a descrição diz isso. A autorização fica no service (ADR-0087 item 4): viewer lê,
// member cria, edita, aplica e tira, admin arquiva, desarquiva e deleta. Toda escrita gera `activity_event` na mesma
// transação, com os verbos de `domain.VerbPersonaTag*` (item 12).
//
// A CLI segue as mesmas operações (item 9): `hanko persona tag list [--archived]`, `hanko persona tag create <nome>
// [--color] [--description]`, `hanko persona tag edit <tag> [--name] [--color] [--description]`, `hanko persona tag
// archive|unarchive <tag>`, `hanko persona tag delete <tag> [--yes]`, `hanko persona tag add <persona> <tag> [--create]`,
// `hanko persona tag remove <persona> <tag>` e `hanko persona tag personas [--tag] [--match] [--untagged] [--level]
// [--max-depth] [--archived]`. `<tag>` é o id ou o nome, sem distinguir caixa.
//
// Na fatia 1 também mudam, sem operação nova: `Persona.tags` passa a vir preenchido; o MCP `hanko_persona_list` devolve
// as tags e aceita o filtro `tag`, e `hanko_whoami` devolve as tags da própria persona, só leitura (item 9); o export da
// empresa leva o cadastro e as ligações (item 11).

// pendingTagOp registra uma operação do contrato das tags que ainda responde 501.
func pendingTagOp[I, O any](api huma.API, o huma.Operation) {
	o.Description = "Contrato das tags de persona (ADR-0087, #118). Responde 501 até a fatia 1."
	o.Errors = append(o.Errors, http.StatusNotImplemented)
	huma.Register(api, o, func(context.Context, *I) (*O, error) {
		return nil, problem(http.StatusNotImplemented, "operação ainda não implementada")
	})
}

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

// ---------- registro ----------

func (s *server) registerPersonaTags(api huma.API) {
	const tag = "personas"
	b := "/companies/{company}"

	pendingTagOp[listPersonaTagsIn, ListOut[PersonaTag]](api, op("list-persona-tags", http.MethodGet, b+"/persona-tags", tag,
		"Lista o cadastro de tags de persona, sem as arquivadas por padrão"))
	pendingTagOp[createPersonaTagIn, ItemOut[PersonaTag]](api, created(op("create-persona-tag", http.MethodPost, b+"/persona-tags", tag,
		"Cria uma tag de persona (member; até 200 por empresa, contando as arquivadas)")))
	pendingTagOp[PersonaTagPath, ItemOut[PersonaTag]](api, op("get-persona-tag", http.MethodGet, b+"/persona-tags/{tag}", tag,
		"Obtém uma tag, com a contagem de personas"))
	pendingTagOp[updatePersonaTagIn, ItemOut[PersonaTag]](api, op("update-persona-tag", http.MethodPatch, b+"/persona-tags/{tag}", tag,
		"Edita nome, cor ou descrição de uma tag (member)"))
	pendingTagOp[PersonaTagPath, ItemOut[PersonaTag]](api, op("archive-persona-tag", http.MethodPost, b+"/persona-tags/{tag}/archive", tag,
		"Arquiva uma tag (admin; se desfaz)"))
	pendingTagOp[PersonaTagPath, ItemOut[PersonaTag]](api, op("unarchive-persona-tag", http.MethodPost, b+"/persona-tags/{tag}/unarchive", tag,
		"Desarquiva uma tag (admin)"))
	pendingTagOp[PersonaTagPath, struct{}](api, noContent(op("delete-persona-tag", http.MethodDelete, b+"/persona-tags/{tag}", tag,
		"Deleta uma tag e tira de todas as personas (admin; não se desfaz)")))

	pendingTagOp[PersonaTagLinkPath, struct{}](api, noContent(op("tag-persona", http.MethodPut, b+"/personas/{persona}/tags/{tag}", tag,
		"Aplica uma tag ativa a uma persona (member; até 20 por persona; aplicar de novo não muda nada)")))
	pendingTagOp[createAndTagPersonaIn, ItemOut[PersonaTagRef]](api, created(op("create-and-tag-persona", http.MethodPost, b+"/personas/{persona}/tags", tag,
		"Cria a tag pelo nome, se não existe, e aplica à persona numa transação só (member)")))
	pendingTagOp[PersonaTagLinkPath, struct{}](api, noContent(op("untag-persona", http.MethodDelete, b+"/personas/{persona}/tags/{tag}", tag,
		"Tira uma tag de uma persona (member; vale também para tag arquivada)")))

	pendingTagOp[listOrgPersonasIn, ListOut[OrgPersona]](api, op("list-org-personas", http.MethodGet, b+"/org-chart/personas", tag,
		"Lista as personas do recorte do organograma por tag e por nível, com o nível de cada uma"))
}
