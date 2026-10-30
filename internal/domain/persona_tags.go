package domain

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// Tags de persona e filtros do organograma (ADR-0087). Os valores são os da migration 00212. As tags de artefato
// (ADR-0064, NormalizeTags) não mudam.

// PersonaTagColor é um nome da paleta fechada de cores de tag (ADR-0087 item 2). A UI resolve cada nome num token
// `--tag-<cor>` com valor para o tema claro e para o escuro; a API nunca aceita hex livre.
type PersonaTagColor string

const (
	TagGray   PersonaTagColor = "gray" // padrão
	TagRed    PersonaTagColor = "red"
	TagAmber  PersonaTagColor = "amber"
	TagGreen  PersonaTagColor = "green"
	TagTeal   PersonaTagColor = "teal"
	TagBlue   PersonaTagColor = "blue"
	TagViolet PersonaTagColor = "violet"
	TagPink   PersonaTagColor = "pink"
)

// PersonaTagColors lista a paleta na ordem do seletor (a mesma da constraint do banco).
var PersonaTagColors = []PersonaTagColor{TagGray, TagRed, TagAmber, TagGreen, TagTeal, TagBlue, TagViolet, TagPink}

// Valid informa se a cor é da paleta.
func (c PersonaTagColor) Valid() bool { return slices.Contains(PersonaTagColors, c) }

const (
	// PersonaTagNameMax é o tamanho máximo do nome, em caracteres.
	PersonaTagNameMax = 40
	// PersonaTagDescriptionMax é o tamanho máximo da descrição, em caracteres.
	PersonaTagDescriptionMax = 200
	// MaxTagsPerPersona é o teto de tags ligadas a uma persona.
	MaxTagsPerPersona = 20
	// MaxPersonaTagsPerCompany é o teto do cadastro de uma empresa, contando as arquivadas.
	MaxPersonaTagsPerCompany = 200
)

// PersonaTag é uma tag do cadastro da empresa. Renomear ou trocar a cor mantém o ID (ADR-0087 item 2).
type PersonaTag struct {
	ID          ID              `json:"id"`
	CompanyID   ID              `json:"company_id"`
	Name        string          `json:"name"`
	Color       PersonaTagColor `json:"color"`
	Description string          `json:"description"`
	ArchivedAt  *time.Time      `json:"archived_at,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// Archived informa se a tag está arquivada: continua nas personas, mas não se aplica a mais nenhuma (ADR-0087 item 3).
func (t PersonaTag) Archived() bool { return t.ArchivedAt != nil }

// NormalizePersonaTagName apara o nome e confere o tamanho (1 a 40 caracteres). Acento e espaço no meio valem.
func NormalizePersonaTagName(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", Invalid("name", "tag name is required")
	}
	if utf8.RuneCountInString(s) > PersonaTagNameMax {
		return "", Invalid("name", fmt.Sprintf("tag name too long (max %d characters)", PersonaTagNameMax))
	}
	return s, nil
}

// PersonaTagKey é a chave de unicidade do nome na empresa: o nome em minúsculas, como o índice `lower(name)` do banco.
// A importação usa a mesma chave para reaproveitar uma tag que já existe (ADR-0087 item 11).
func PersonaTagKey(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// ValidPersonaTagDescription confere o tamanho da descrição.
func ValidPersonaTagDescription(s string) error {
	if utf8.RuneCountInString(s) > PersonaTagDescriptionMax {
		return Invalid("description", fmt.Sprintf("description too long (max %d characters)", PersonaTagDescriptionMax))
	}
	return nil
}

// Verbos de `activity_event` das tags (ADR-0087 item 12). `persona_tag.deleted` leva o nome e a contagem de personas
// que perderam a tag; não há um evento por persona afetada.
const (
	VerbPersonaTagCreated    = "persona_tag.created"
	VerbPersonaTagUpdated    = "persona_tag.updated"
	VerbPersonaTagArchived   = "persona_tag.archived"
	VerbPersonaTagUnarchived = "persona_tag.unarchived"
	VerbPersonaTagDeleted    = "persona_tag.deleted"
	VerbPersonaTagged        = "persona.tagged"
	VerbPersonaUntagged      = "persona.untagged"
)

// ---------- filtros do organograma ----------

// OrgTagMatch combina as tags escolhidas no filtro (ADR-0087 item 7): `any` (OU, o padrão) ou `all` (E).
type OrgTagMatch string

const (
	OrgTagAny OrgTagMatch = "any"
	OrgTagAll OrgTagMatch = "all"
)

// Valid informa se a combinação é conhecida.
func (m OrgTagMatch) Valid() bool { return m == OrgTagAny || m == OrgTagAll }

// OrgFilterMode é o modo do recorte (ADR-0087 item 7): `highlight` esmaece quem está fora; `prune` mostra só o recorte e
// os superiores até a raiz, esmaecidos.
type OrgFilterMode string

const (
	OrgHighlight OrgFilterMode = "highlight"
	OrgPrune     OrgFilterMode = "prune"
)

// Valid informa se o modo é conhecido.
func (m OrgFilterMode) Valid() bool { return m == OrgHighlight || m == OrgPrune }

// OrgLevel é o nível de uma persona no organograma: 1 na raiz, e o do superior mais um (ADR-0087 item 6). É calculado a
// partir da profundidade do nó (`OrgNode.Depth`, 0 na raiz) e não fica gravado.
func OrgLevel(depth int) int { return depth + 1 }
