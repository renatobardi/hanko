package domain

import (
	"encoding/json"
	"strings"
	"time"
)

// Limites de skills (ADR-0029). O tamanho vale para o corpo em bytes; o total por run protege o contexto do
// agente e o disco do diretório temporário.
const (
	MaxSkillBodyBytes     = 32 << 10
	MaxSkillNameRunes     = 120
	MaxSkillDescRunes     = 400
	MaxSkillsPerRun       = 50
	MaxSkillsBytesPerRun  = 256 << 10
	SkillFrontmatterDelim = "---"
)

// Skill é uma habilidade reutilizável: instruções em markdown que o harness carrega sob demanda.
// Body não inclui o frontmatter; MD() produz o SKILL.md completo.
type Skill struct {
	ID          ID        `json:"id"`
	CompanyID   ID        `json:"company_id"`
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Body        string    `json:"body"`
	Version     int       `json:"version"`
	CreatedBy   Actor     `json:"created_by"`
	UpdatedBy   Actor     `json:"updated_by"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	// Origem (ADR-0035): a fonte de onde a skill veio, o diretório e o commit importados. SourceModified diz
	// que a skill foi editada aqui depois de importada (a sincronização não a sobrescreve sem force).
	SourceID       *ID    `json:"source_id,omitempty"`
	SourcePath     string `json:"source_path,omitempty"`
	SourceCommit   string `json:"source_commit,omitempty"`
	SourceModified bool   `json:"source_modified,omitempty"`
	// Pinned: o conteúdo é o de uma versão fixada para a persona, não o atual (só nas leituras da run).
	Pinned bool `json:"pinned,omitempty"`
}

// MD devolve o arquivo SKILL.md: frontmatter com name (o slug, que é o nome que o harness aceita) e description,
// seguido do corpo.
func (s Skill) MD() string { return SkillMD(s.Slug, s.Description, s.Body) }

// SkillMD monta um SKILL.md. Os valores do frontmatter saem como string JSON, que é YAML válido e não
// quebra com dois-pontos, aspas ou acentos.
func SkillMD(slug, description, body string) string {
	q := func(v string) string {
		b, _ := json.Marshal(strings.Join(strings.Fields(v), " "))
		return string(b)
	}
	var sb strings.Builder
	sb.WriteString(SkillFrontmatterDelim + "\nname: " + slug + "\ndescription: " + q(description) + "\n" + SkillFrontmatterDelim + "\n\n")
	sb.WriteString(strings.TrimSpace(body))
	sb.WriteString("\n")
	return sb.String()
}

// PersonaSkill é o estado de uma skill para uma persona.
type PersonaSkill struct {
	SkillID     ID     `json:"skill_id"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
	// Version é a versão atual da skill; PinnedRevision, quando há, é a versão fixada para a persona (ADR-0035).
	Version        int  `json:"version"`
	PinnedRevision *int `json:"pinned_revision,omitempty"`
	FromSource     bool `json:"from_source,omitempty"`
}

// SkillPersona é o estado de uma persona para uma skill.
type SkillPersona struct {
	PersonaID ID     `json:"persona_id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
	// PinnedRevision é a versão fixada para a persona (nil = sempre a mais nova).
	PinnedRevision *int `json:"pinned_revision,omitempty"`
}
