package memory

import (
	"fmt"
	"sort"
	"strings"
)

// Política de escrita da curadoria (ADR-0030).
//
// A run de consolidação fala com o ai-memory pelo proxy /memory-mcp. O proxy classifica cada chamada de
// tool e decide o destino: leitura e escrita aditiva seguem direto; o que remove, funde ou reescreve
// conteúdo vira proposta e só é aplicado depois da decisão de um humano.
//
// Os nomes de tool do ai-memory confirmados contra um servidor real (tools/list, 2026-10-15) estão em
// confirmedClass. Para qualquer outro nome (versões futuras) a política não presume nada: ela
// olha as palavras do nome (get_x, list_x, search_x… são leitura; add_x, append_x… são aditivas) e, na
// dúvida, RETÉM. Reter é o lado seguro: uma tool desconhecida vira proposta que um humano lê, nunca uma
// remoção silenciosa.

// Class é o destino de uma chamada de tool numa run de curadoria.
type Class int

const (
	// ClassRead não altera conteúdo: segue direto.
	ClassRead Class = iota
	// ClassAdditive só acrescenta conteúdo novo: segue direto.
	ClassAdditive
	// ClassHeld pode remover, fundir ou reescrever (ou é desconhecida): vira proposta com gate humano.
	ClassHeld
	// ClassDeny não cabe numa curadoria (consome handoff, encerra sessão): recusada.
	ClassDeny
)

func (c Class) String() string {
	switch c {
	case ClassRead:
		return "read"
	case ClassAdditive:
		return "additive"
	case ClassHeld:
		return "held"
	case ClassDeny:
		return "deny"
	}
	return fmt.Sprintf("class(%d)", int(c))
}

// WritePageTool é a tool de escrita de página (confirmada). Escrever num caminho existente substitui a página.
const WritePageTool = "memory_write_page"

// CuratorReportPrefix é a pasta de relatórios da própria curadoria: páginas novas ali são aditivas.
const CuratorReportPrefix = "hanko/curadoria/"

// confirmedClass são as tools do ai-memory vistas num servidor real. Valem antes da heurística de palavras
// (ex.: memory_read_session_observations só lê, mas "session" a faria ser recusada).
var confirmedClass = map[string]Class{
	"memory_query":                     ClassRead,
	"memory_read_page":                 ClassRead,
	"memory_recent":                    ClassRead,
	"memory_explore":                   ClassRead,
	"memory_briefing":                  ClassRead,
	"memory_status":                    ClassRead,
	"memory_lint":                      ClassRead,
	"memory_read_session_observations": ClassRead,
	"memory_delete_page":               ClassHeld,
	"memory_forget_sweep":              ClassHeld,
	"memory_consolidate":               ClassHeld,
	"memory_auto_improve":              ClassHeld,
	"memory_feedback":                  ClassHeld,
	"memory_message_send":              ClassHeld,
	"memory_message_pop":               ClassHeld,
	"memory_message_cancel":            ClassHeld,
	"memory_message_list":              ClassHeld,
	"memory_install_self_routing":      ClassHeld,
	"memory_handoff_accept":            ClassDeny,
	"memory_handoff_begin":             ClassDeny,
	"memory_handoff_cancel":            ClassDeny,
	"memory_handoff_list":              ClassDeny,
}

var (
	denyTokens = tokenSet("handoff", "finalize", "session", "hook")
	// destructiveTokens: uma palavra destas em qualquer posição do nome retém a chamada.
	destructiveTokens = tokenSet("delete", "remove", "forget", "prune", "purge", "archive", "drop", "evict",
		"merge", "consolidate", "supersede", "rename", "move", "replace", "rewrite", "update", "edit", "write",
		"set", "patch", "overwrite", "obsolete", "deprecate", "clear", "reset", "compact", "dedupe", "dedup",
		"promote", "demote", "restore", "revert")
	readTokens = tokenSet("get", "list", "read", "search", "find", "query", "recall", "show", "briefing",
		"status", "stats", "history", "diff", "graph", "backlinks", "links", "lint", "health", "timeline",
		"related", "entities", "tags", "outline", "fetch", "lookup", "count", "export")
	additiveTokens = tokenSet("add", "append", "create", "remember", "observe", "record", "log", "note")
)

func tokenSet(words ...string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}

func tokens(tool string) []string {
	n := strings.ToLower(strings.TrimSpace(tool))
	n = strings.TrimPrefix(n, "memory_")
	return strings.FieldsFunc(n, func(r rune) bool { return r == '_' || r == '-' || r == '.' || r == '/' || r == ' ' })
}

// Classify decide o destino de uma chamada de tool de uma run de curadoria.
func Classify(tool string, args map[string]any) Class {
	if strings.EqualFold(strings.TrimSpace(tool), WritePageTool) {
		// Escrever página: aditivo só nos relatórios da própria curadoria; no resto pode substituir uma página.
		ts := TargetsOf(args)
		if len(ts) == 1 && strings.HasPrefix(ts[0], CuratorReportPrefix) && !strings.Contains(ts[0], "..") {
			return ClassAdditive
		}
		return ClassHeld
	}
	if c, ok := confirmedClass[strings.ToLower(strings.TrimSpace(tool))]; ok {
		return c
	}
	toks := tokens(tool)
	if len(toks) == 0 {
		return ClassHeld
	}
	for _, t := range toks {
		if denyTokens[t] {
			return ClassDeny
		}
	}
	for _, t := range toks {
		if destructiveTokens[t] {
			return ClassHeld
		}
	}
	if readTokens[toks[0]] {
		return ClassRead
	}
	if additiveTokens[toks[0]] {
		return ClassAdditive
	}
	return ClassHeld
}

// InferKind diz o que uma chamada retida faz, pelo nome da tool: fundir, remover ou (o resto) reescrever.
func InferKind(tool string) string {
	for _, t := range tokens(tool) {
		switch t {
		case "merge", "consolidate", "dedupe", "dedup":
			return "merge"
		case "delete", "remove", "forget", "prune", "purge", "archive", "drop", "evict", "obsolete", "deprecate":
			return "delete"
		}
	}
	return "rewrite"
}

var (
	targetKeys   = []string{"path", "paths", "page", "pages", "source", "sources", "from", "target", "targets", "into", "dest", "destination", "old_path", "new_path"}
	proposedKeys = []string{"body", "content", "text", "merged", "markdown", "new_body", "new_content"}
)

const maxTargets = 50

// TargetsOf extrai das argumentos de uma tool os caminhos de página que ela afeta (melhor esforço, pelos
// nomes comuns de argumento). Devolve na ordem em que aparecem, sem repetir.
func TargetsOf(args map[string]any) []string {
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] || len(out) >= maxTargets {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	for _, k := range targetKeys {
		switch v := args[k].(type) {
		case string:
			add(v)
		case []any:
			for _, e := range v {
				if s, ok := e.(string); ok {
					add(s)
				}
			}
		case []string:
			for _, s := range v {
				add(s)
			}
		}
	}
	return out
}

// ProposedOf extrai das argumentos o texto novo que a chamada escreve (vazio quando só remove).
func ProposedOf(args map[string]any) string {
	for _, k := range proposedKeys {
		if s, ok := args[k].(string); ok && strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// ---- diff ----

const (
	diffContext  = 3
	maxDiffCells = 4_000_000
	maxDiffBytes = 60_000
)

// UnifiedDiff compara dois textos por linha e devolve um diff legível (prefixos " ", "-", "+"), com as
// sequências longas de linhas iguais recolhidas. Entradas enormes caem num diff "tudo sai, tudo entra".
func UnifiedDiff(oldText, newText string) string {
	a, b := splitLines(oldText), splitLines(newText)
	var ops []diffOp
	if len(a)*len(b) > maxDiffCells {
		for _, l := range a {
			ops = append(ops, diffOp{'-', l})
		}
		for _, l := range b {
			ops = append(ops, diffOp{'+', l})
		}
	} else {
		ops = lcsDiff(a, b)
	}
	out := renderDiff(ops)
	if len(out) > maxDiffBytes {
		out = strings.ToValidUTF8(out[:maxDiffBytes], "") + "\n… [diff truncado]\n"
	}
	return out
}

type diffOp struct {
	op   byte // ' ', '-', '+'
	line string
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func lcsDiff(a, b []string) []diffOp {
	n, m := len(a), len(b)
	// l[i][j] = tamanho da LCS de a[i:] e b[j:]
	l := make([][]int32, n+1)
	for i := range l {
		l[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			switch {
			case a[i] == b[j]:
				l[i][j] = l[i+1][j+1] + 1
			case l[i+1][j] >= l[i][j+1]:
				l[i][j] = l[i+1][j]
			default:
				l[i][j] = l[i][j+1]
			}
		}
	}
	var ops []diffOp
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{' ', a[i]})
			i++
			j++
		case l[i+1][j] >= l[i][j+1]:
			ops = append(ops, diffOp{'-', a[i]})
			i++
		default:
			ops = append(ops, diffOp{'+', b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{'-', a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{'+', b[j]})
	}
	return ops
}

// renderDiff mostra as mudanças com até diffContext linhas iguais em volta e recolhe o resto.
func renderDiff(ops []diffOp) string {
	keep := make([]bool, len(ops))
	changed := false
	for i, o := range ops {
		if o.op == ' ' {
			continue
		}
		changed = true
		for k := max(0, i-diffContext); k <= min(len(ops)-1, i+diffContext); k++ {
			keep[k] = true
		}
	}
	if !changed {
		return ""
	}
	var sb strings.Builder
	skipped := 0
	flush := func() {
		if skipped > 0 {
			fmt.Fprintf(&sb, "@@ %d linhas iguais @@\n", skipped)
			skipped = 0
		}
	}
	for i, o := range ops {
		if !keep[i] {
			skipped++
			continue
		}
		flush()
		sb.WriteByte(o.op)
		sb.WriteString(o.line)
		sb.WriteByte('\n')
	}
	flush()
	return sb.String()
}

// SortedTargets devolve uma cópia ordenada (para hash estável).
func SortedTargets(ts []string) []string {
	out := append([]string(nil), ts...)
	sort.Strings(out)
	return out
}

// IsNotFound diz se o erro do provedor significa que o alvo (página) não existe. Heurística sobre a
// mensagem, porque o provedor devolve o erro da tool em texto: serve só para tratar a repetição de uma
// aplicação que talvez já tenha tido efeito.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, m := range []string{"not found", "does not exist", "no such", "não encontrad", "nao encontrad", "inexistente", "não existe", "nao existe"} {
		if strings.Contains(msg, m) {
			return true
		}
	}
	return false
}
