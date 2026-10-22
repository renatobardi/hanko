package export

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

// TeamFormat é o identificador e a versão do pacote de time (ADR-0044): um subset do hanko.company.v1 (personas,
// skills, fluxos e projetos) que se instala numa empresa existente, sempre pausado.
const TeamFormat = "hanko.team.v1"

// Tetos de um pacote de time (o catálogo embutido e os arquivos de fora obedecem aos mesmos).
const (
	MaxTeamPersonas = 50
	MaxTeamSkills   = 100
	MaxTeamFlows    = 50
	MaxTeamProjects = 20
	MaxTeamBytes    = 2 << 20
)

// TeamPackage é o pacote inteiro. Referências entre itens são por slug, como no company.v1. Nunca carrega segredo,
// assinatura, workspace (caminho de máquina), task nem histórico.
type TeamPackage struct {
	Format     string       `json:"format" enum:"hanko.team.v1" doc:"Identificador e versão do formato."`
	ExportedAt time.Time    `json:"exported_at,omitempty"`
	Package    TeamInfo     `json:"package"`
	Requires   TeamRequires `json:"requires" doc:"O que a empresa de destino precisa ter (calculado ao exportar; o instalador recalcula)."`
	Projects   []Project    `json:"projects,omitempty"`
	Personas   []Persona    `json:"personas"`
	Skills     []Skill      `json:"skills,omitempty" doc:"Skills usadas pelas personas; a ligação está em personas[].skills."`
	Flows      []Flow       `json:"flows,omitempty" doc:"Rotinas agendadas das personas. Entram desligadas."`
}

// TeamInfo identifica o pacote.
type TeamInfo struct {
	Slug        string `json:"slug" example:"squad-de-produto"`
	Name        string `json:"name" example:"Squad de produto"`
	Description string `json:"description,omitempty"`
	Version     string `json:"version,omitempty" example:"1.0.0"`
	Author      string `json:"author,omitempty"`
}

// TeamRequires lista o que o pacote exige do destino.
type TeamRequires struct {
	// Agents são os harnesses das personas (claude, codex, acp...): a empresa precisa de login/assinatura para eles.
	Agents []string `json:"agents,omitempty"`
	// Secrets são os nomes citados como ${secret:NOME} na configuração das personas; precisam existir na empresa.
	Secrets []string `json:"secrets,omitempty"`
}

var (
	teamSlugRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	teamSecretRe = regexp.MustCompile(`\$\{secret:(?:user/)?([A-Za-z][A-Za-z0-9_.-]{0,63})\}`)
)

// TeamError é um problema de validação do pacote, com o caminho do campo.
type TeamError struct{ Field, Msg string }

func (e *TeamError) Error() string { return e.Field + ": " + e.Msg }

func terr(field, format string, a ...any) *TeamError {
	return &TeamError{Field: field, Msg: fmt.Sprintf(format, a...)}
}

// Validate confere a forma do pacote: formato, identificação, tetos, unicidade e referências internas. A validação de
// cada item (agente, cron, tetos de texto...) é a do import com merge, que o instalador roda em seguida.
func (p TeamPackage) Validate() error {
	if p.Format != TeamFormat {
		return terr("format", "formato não suportado %q (esperado %q)", p.Format, TeamFormat)
	}
	if !teamSlugRe.MatchString(p.Package.Slug) {
		return terr("package.slug", "use minúsculas, dígitos e hífen (1-63)")
	}
	if n := len([]rune(strings.TrimSpace(p.Package.Name))); n < 1 || n > 120 {
		return terr("package.name", "de 1 a 120 caracteres")
	}
	if len([]rune(p.Package.Description)) > 2000 {
		return terr("package.description", "no máximo 2000 caracteres")
	}
	if len(p.Package.Version) > 40 || len([]rune(p.Package.Author)) > 120 {
		return terr("package.version", "version até 40 e author até 120 caracteres")
	}
	if len(p.Personas) == 0 {
		return terr("personas", "o pacote precisa de pelo menos uma persona")
	}
	for _, c := range []struct {
		name string
		n, m int
	}{{"personas", len(p.Personas), MaxTeamPersonas}, {"skills", len(p.Skills), MaxTeamSkills}, {"flows", len(p.Flows), MaxTeamFlows}, {"projects", len(p.Projects), MaxTeamProjects}} {
		if c.n > c.m {
			return terr(c.name, "no máximo %d", c.m)
		}
	}
	pe, err := uniq("personas", p.Personas, func(x Persona) string { return x.Slug })
	if err != nil {
		return err
	}
	sk, err := uniq("skills", p.Skills, func(x Skill) string { return x.Slug })
	if err != nil {
		return err
	}
	pr, err := uniq("projects", p.Projects, func(x Project) string { return x.Slug })
	if err != nil {
		return err
	}
	if _, err := uniq("flows", p.Flows, func(x Flow) string { return x.Slug }); err != nil {
		return err
	}
	for i, x := range p.Personas {
		at := func(f string) string { return fmt.Sprintf("personas[%d].%s", i, f) }
		if x.Workspace != "" {
			return terr(at("workspace"), "pacote de time não carrega workspace (o caminho é da máquina)")
		}
		if x.Subscription != "" {
			return terr(at("subscription"), "pacote de time não carrega assinatura; escolha na instalação")
		}
		if x.ReportsTo != "" && !pe[x.ReportsTo] {
			return terr(at("reports_to"), "%q não está no pacote", x.ReportsTo)
		}
		if x.ReportsTo == x.Slug {
			return terr(at("reports_to"), "a persona não pode reportar a si mesma")
		}
		if x.Reviewer != "" && !pe[x.Reviewer] {
			return terr(at("reviewer"), "%q não está no pacote", x.Reviewer)
		}
		for _, s := range x.Skills {
			if !sk[s] {
				return terr(at("skills"), "skill %q não está no pacote", s)
			}
		}
	}
	for i, x := range p.Flows {
		at := func(f string) string { return fmt.Sprintf("flows[%d].%s", i, f) }
		if !pe[x.Persona] {
			return terr(at("persona"), "%q não está no pacote", x.Persona)
		}
		if x.Project != "" && !pr[x.Project] {
			return terr(at("project"), "%q não está no pacote", x.Project)
		}
	}
	for i, x := range p.Projects {
		if x.Workspace != "" {
			return terr(fmt.Sprintf("projects[%d].workspace", i), "pacote de time não carrega workspace")
		}
	}
	return nil
}

func uniq[T any](path string, items []T, key func(T) string) (map[string]bool, error) {
	set := make(map[string]bool, len(items))
	for i, it := range items {
		k := key(it)
		if k == "" {
			return nil, terr(fmt.Sprintf("%s[%d].slug", path, i), "obrigatório")
		}
		if set[k] {
			return nil, terr(fmt.Sprintf("%s[%d].slug", path, i), "%q repetido", k)
		}
		set[k] = true
	}
	return set, nil
}

// ComputeRequires calcula os harnesses e os segredos que o pacote cita.
func (p TeamPackage) ComputeRequires() TeamRequires {
	var r TeamRequires
	for _, x := range p.Personas {
		if x.Agent != "" && !slices.Contains(r.Agents, x.Agent) {
			r.Agents = append(r.Agents, x.Agent)
		}
		raw, err := json.Marshal(x.AdapterConfig)
		if err != nil {
			continue
		}
		for _, m := range teamSecretRe.FindAllStringSubmatch(string(raw), -1) {
			if !slices.Contains(r.Secrets, m[1]) {
				r.Secrets = append(r.Secrets, m[1])
			}
		}
	}
	sort.Strings(r.Agents)
	sort.Strings(r.Secrets)
	return r
}

// Document converte o pacote num company.v1 que o motor de import com merge entende. Os dados da empresa são
// só para a validação (o destino já existe e não muda). Tudo entra pausado/desligado: o instalador não passa Active.
func (p TeamPackage) Document() Document {
	d := Document{Format: Format, ExportedAt: p.ExportedAt,
		Company:  Company{Name: p.Package.Name, Slug: p.Package.Slug, TaskPrefix: "HNK", Timezone: "UTC", NextTaskNumber: 1},
		Projects: slices.Clone(p.Projects), Personas: slices.Clone(p.Personas), Skills: slices.Clone(p.Skills), Flows: slices.Clone(p.Flows)}
	for i := range d.Personas {
		d.Personas[i].Status = "paused"
		d.Personas[i].Workspace, d.Personas[i].Subscription = "", ""
	}
	for i := range d.Flows {
		d.Flows[i].Enabled = false
	}
	return d
}
