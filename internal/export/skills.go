package export

import "time"

// Skill é uma habilidade reutilizável da empresa (ADR-0029). O corpo é o markdown do SKILL.md, sem o frontmatter.
type Skill struct {
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Body        string    `json:"body"`
	CreatedAt   time.Time `json:"created_at"`
}

// Integration é uma integração exportada SEM segredos: nenhum valor de header, de variável de ambiente ou
// segredo HMAC viaja, nem os argumentos de um comando stdio. Na importação ela entra desligada, e quem
// importa informa os segredos de novo.
type Integration struct {
	Kind        string    `json:"kind" enum:"mcp,webhook"`
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	Transport   string    `json:"transport,omitempty" enum:"http,stdio" doc:"MCP."`
	URL         string    `json:"url,omitempty" doc:"MCP http: endereço sem usuário, query e fragmento; webhook: só esquema e host."`
	Command     string    `json:"command,omitempty" doc:"MCP stdio: o executável, sem argumentos."`
	AllPersonas bool      `json:"all_personas,omitempty" doc:"MCP: vale para todas as personas."`
	Personas    []string  `json:"personas,omitempty" doc:"MCP: slugs das personas ligadas."`
	Events      []string  `json:"events,omitempty" doc:"Webhook: eventos assinados."`
	HeaderNames []string  `json:"header_names,omitempty" doc:"MCP http: só os NOMES dos headers que o original tinha; os valores precisam ser informados de novo."`
	EnvNames    []string  `json:"env_names,omitempty" doc:"MCP stdio: só os nomes das variáveis."`
	CreatedAt   time.Time `json:"created_at"`
}
