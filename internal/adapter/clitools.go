package adapter

import "strings"

// Harnesses sem MCP (pi, cursor) usam as tools do Hanko pela CLI `hanko`, autenticada pelo run token (ADR-0056).

// CLIToolsEnv são as variáveis que fazem a CLI `hanko` (já no PATH do servidor) agir como a run: o run token vira
// HANKO_TOKEN (a credencial da CLI), junto da URL e da empresa. Sem run token (run de revisão, sem credencial) nada
// é devolvido: a run nem fica sabendo onde o Hanko está.
func CLIToolsEnv(spec Spec) map[string]string {
	tok := spec.Env["HANKO_RUN_TOKEN"]
	if tok == "" {
		return nil
	}
	out := map[string]string{"HANKO_TOKEN": tok, "HANKO_COMPANY": spec.CompanyID.String()}
	if u := spec.Env["HANKO_URL"]; u != "" {
		out["HANKO_URL"] = u
	}
	return out
}

// CLIToolsProtocol é o trecho do system prompt que troca as tools MCP `hanko_*` pelos comandos da CLI. Vazio quando
// a run não tem credencial.
func CLIToolsProtocol(spec Spec) string {
	if spec.Env["HANKO_RUN_TOKEN"] == "" {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("## Ferramentas do Hanko pela CLI\n\n")
	sb.WriteString("Este harness não usa MCP: onde o protocolo acima cita as tools `hanko_*`, use a CLI `hanko` pelo shell. ")
	sb.WriteString("Ela já está autenticada como esta execução (variáveis HANKO_URL, HANKO_TOKEN e HANKO_COMPANY); não imprima nem grave essas variáveis.\n\n")
	if spec.TaskKey != "" {
		sb.WriteString("- Ler a task: `hanko task show " + spec.TaskKey + "`\n")
		sb.WriteString("- Comentar: `hanko task comment " + spec.TaskKey + " \"texto\"` (ou `-` para ler do stdin)\n")
		sb.WriteString("- Mudar o status: `hanko task status " + spec.TaskKey + " in_review` (ou `blocked`, `in_progress`)\n")
		sb.WriteString("- Criar subtask: `hanko task create \"título\" --parent " + spec.TaskKey + " [--depends-on OUTRA-TASK]`\n")
		sb.WriteString("- Entregar um arquivo: `hanko artifact put caminho --task " + spec.TaskKey + "` (o mesmo nome cria a versão seguinte)\n")
	}
	sb.WriteString("- Pedir decisão humana: comente o que precisa ser decidido, mude o status para `blocked` e encerre.\n")
	return strings.TrimRight(sb.String(), "\n")
}

// WithCLITools junta o protocolo da CLI ao system prompt.
func WithCLITools(spec Spec) string {
	p := CLIToolsProtocol(spec)
	switch {
	case p == "":
		return spec.System
	case strings.TrimSpace(spec.System) == "":
		return p
	}
	return strings.TrimSpace(spec.System) + "\n\n" + p
}
