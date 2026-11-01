package memory

import "sort"

// Contrato de argumentos do proxy de memória: quais tools e argumentos uma persona pode usar. O ai-memory aceita o
// token de serviço do Hanko em qualquer escopo, então cada argumento novo pode ser um caminho para fora do workspace
// da run. Por isso a lista é de permitidos: tool ou argumento fora dela é recusado até alguém revisar a versão nova.
// TestContractRealServer compara esta tabela com o tools/list do servidor.

const (
	// QueryTool é a tool de busca. O proxy a envia com um único item em scopes: é o que deixa de fora os
	// global_scope_hits, a área que todas as empresas compartilham.
	QueryTool = "memory_query"
	// MessageSendTool é a tool de recado entre escopos, com destino e origem em argumentos próprios.
	MessageSendTool = "memory_message_send"
)

type toolSpec struct {
	scoped  bool // a tool recebe workspace e project
	allowed []string
	refused []string // existem no servidor, mas saem do escopo da run
}

var toolContract = map[string]toolSpec{
	"memory_auto_improve": {scoped: true, allowed: []string{"include_raw_fallback", "max_input_tokens", "max_proposals",
		"min_confidence", "min_observations", "min_session_duration_secs", "session_id"}},
	"memory_briefing":       {scoped: true, allowed: []string{"recent_pages_limit", "settled_first"}},
	"memory_consolidate":    {allowed: []string{"dry_run", "instructions", "multi_page", "session_id"}},
	"memory_delete_page":    {scoped: true, allowed: []string{"expected_page_id", "path"}, refused: []string{"scope"}},
	"memory_explore":        {scoped: true, allowed: []string{"focus", "reasoning", "recent_pages_limit"}},
	"memory_feedback":       {scoped: true, allowed: []string{"path", "reason", "signal"}},
	"memory_forget_sweep":   {scoped: true, allowed: []string{"dry_run"}},
	"memory_handoff_accept": {scoped: true, allowed: []string{"any_owner", "cwd", "handoff_id"}},
	"memory_handoff_begin": {scoped: true, allowed: []string{"cwd", "files_touched", "next_steps", "open_questions",
		"shared", "summary"}},
	"memory_handoff_cancel":       {scoped: true, allowed: []string{"any_owner", "handoff_id"}},
	"memory_handoff_list":         {scoped: true, allowed: []string{"any_owner", "limit"}},
	"memory_install_self_routing": {allowed: []string{"compact"}},
	"memory_lint":                 {scoped: true, allowed: []string{"dry_run", "no_llm"}},
	"memory_message_cancel":       {scoped: true, allowed: []string{"message_id"}},
	"memory_message_list":         {scoped: true, allowed: []string{"box", "limit"}},
	"memory_message_pop":          {scoped: true, allowed: []string{"agent", "message_id"}},
	MessageSendTool: {allowed: []string{"body", "evidence", "nature", "subject", "to_project", "to_workspace"},
		refused: []string{"from_project", "from_workspace"}},
	"memory_patch_page": {scoped: true, allowed: []string{"edits", "expected_page_id", "path"}},
	QueryTool: {scoped: true, allowed: []string{"answer", "as_of", "explain", "include_expired", "include_superseded",
		"kind", "limit", "path_prefix", "pin_first", "query", "reasoning", "tag", "tier"}, refused: []string{"global", "scopes"}},
	"memory_read_page": {scoped: true, allowed: []string{"include_related", "max_chars", "path", "query", "related_depth"},
		refused: []string{"scope"}},
	"memory_read_session_observations": {scoped: true, allowed: []string{"body_max_chars", "kinds", "limit", "offset",
		"order", "query", "session_id"}},
	"memory_recent":       {scoped: true, allowed: []string{"limit"}},
	"memory_restore_page": {scoped: true, allowed: []string{"limit", "path", "rev"}},
	"memory_status":       {scoped: true},
	// relations fica de fora: uma relação pode apontar para página de outro escopo, e include_related a seguiria.
	"memory_write_page": {scoped: true, allowed: []string{"abstract", "applies_to", "body", "create_only", "enforced_by",
		"entities", "expected_page_id", "expires_at", "kind", "path", "pinned", "session_id", "tags", "tier", "title"},
		refused: []string{"relations", "scope"}},
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// ScopedTool diz se a tool recebe workspace e project. O proxy os preenche em toda chamada: sem eles o servidor
// resolve um escopo padrão, compartilhado por quem usa o mesmo token.
func ScopedTool(tool string) bool { return toolContract[tool].scoped }

// KnownTool diz se a tool está no contrato.
func KnownTool(tool string) bool {
	_, ok := toolContract[tool]
	return ok
}

// KnownArg diz se o contrato já decidiu sobre o argumento, para permitir ou recusar.
func KnownArg(tool, arg string) bool {
	spec := toolContract[tool]
	return contains(spec.allowed, arg) || contains(spec.refused, arg) ||
		(spec.scoped && (arg == "workspace" || arg == "project"))
}

// ContractTools lista as tools do contrato.
func ContractTools() []string {
	out := make([]string, 0, len(toolContract))
	for t := range toolContract {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// RefusedArgs devolve, em ordem, os argumentos da chamada que o proxy não aceita.
func RefusedArgs(tool string, args map[string]any) []string {
	spec := toolContract[tool]
	var out []string
	for k := range args {
		if !contains(spec.allowed, k) && (!spec.scoped || (k != "workspace" && k != "project")) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
