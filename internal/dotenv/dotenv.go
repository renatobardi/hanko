// Package dotenv lê o texto de um arquivo .env (ADR-0068 §8.1): comentários, linhas vazias, `export`, aspas simples
// (literais) e duplas (com escapes e várias linhas). Não expande variáveis: `${X}` fica como está no valor.
package dotenv

import (
	"fmt"
	"strings"
)

// Entry é uma variável lida, com a linha onde começa.
type Entry struct {
	Key   string
	Value string
	Line  int
}

// Skip é uma linha que não virou variável, com o motivo. Linha vazia e comentário não contam.
type Skip struct {
	Line   int
	Reason string
}

// Result é o que Parse leu. Entries mantém a ordem da primeira aparição de cada chave; chave repetida fica com o último
// valor.
type Result struct {
	Entries []Entry
	Skipped []Skip
}

// Parse lê o conteúdo de um .env. Nunca falha: o que não der para ler vai em Skipped.
func Parse(text string) Result {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	text = strings.TrimPrefix(text, "\ufeff")
	lines := strings.Split(text, "\n")
	var res Result
	index := map[string]int{}
	for i := 0; i < len(lines); i++ {
		raw := strings.TrimSpace(lines[i])
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		start := i + 1
		raw = stripExport(raw)
		eq := strings.IndexByte(raw, '=')
		if eq < 0 {
			res.Skipped = append(res.Skipped, Skip{Line: start, Reason: "sem sinal de igual"})
			continue
		}
		key := strings.TrimSpace(raw[:eq])
		if !validKey(key) {
			res.Skipped = append(res.Skipped, Skip{Line: start, Reason: "nome de variável inválido"})
			continue
		}
		rest := strings.TrimLeft(raw[eq+1:], " \t")
		val, used, err := readValue(rest, lines, i)
		if err != nil {
			res.Skipped = append(res.Skipped, Skip{Line: start, Reason: err.Error()})
			continue
		}
		i = used
		if at, ok := index[key]; ok {
			res.Entries[at].Value = val
			continue
		}
		index[key] = len(res.Entries)
		res.Entries = append(res.Entries, Entry{Key: key, Value: val, Line: start})
	}
	return res
}

// stripExport tira o prefixo `export ` de uma linha.
func stripExport(s string) string {
	if strings.HasPrefix(s, "export") && len(s) > 6 && (s[6] == ' ' || s[6] == '\t') {
		return strings.TrimLeft(s[6:], " \t")
	}
	return s
}

// validKey aceita letra ou _ na frente e letras, números, _, . e - depois.
func validKey(k string) bool {
	if k == "" {
		return false
	}
	for i := 0; i < len(k); i++ {
		c := k[i]
		letter := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
		if letter || i > 0 && (c >= '0' && c <= '9' || c == '.' || c == '-') {
			continue
		}
		return false
	}
	return true
}

// readValue lê o valor que começa em rest (a linha lines[at] depois do `=`). Devolve o valor e o índice da última linha
// que ele consumiu (aspas abertas podem seguir por várias).
func readValue(rest string, lines []string, at int) (string, int, error) {
	if rest == "" {
		return "", at, nil
	}
	q := rest[0]
	if q != '"' && q != '\'' {
		return unquoted(rest), at, nil
	}
	var b strings.Builder
	cur, line := rest[1:], at
	for {
		end, esc := scanQuoted(cur, q, &b)
		if end >= 0 {
			if tail := strings.TrimSpace(cur[end+1:]); tail != "" && !strings.HasPrefix(tail, "#") {
				return "", line, fmt.Errorf("texto depois das aspas de fechamento")
			}
			return b.String(), line, nil
		}
		if esc {
			b.WriteByte('\\')
		}
		line++
		if line >= len(lines) {
			return "", at, fmt.Errorf("aspas sem fechamento")
		}
		b.WriteByte('\n')
		cur = lines[line]
	}
}

// scanQuoted copia para b o trecho de cur até a aspa de fechamento q e devolve o índice dela (-1 se a linha acabou
// antes). Entre aspas duplas valem os escapes \n \r \t \" \\ \$; entre simples, nada é escapado. O segundo retorno é
// verdadeiro quando a linha termina numa barra solta, que o chamador preserva.
func scanQuoted(cur string, q byte, b *strings.Builder) (int, bool) {
	for j := 0; j < len(cur); j++ {
		c := cur[j]
		switch {
		case c == q:
			return j, false
		case q == '"' && c == '\\' && j+1 < len(cur):
			j++
			switch cur[j] {
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case '"', '\\', '$':
				b.WriteByte(cur[j])
			default:
				b.WriteByte('\\')
				b.WriteByte(cur[j])
			}
		case q == '"' && c == '\\':
			return -1, true
		default:
			b.WriteByte(c)
		}
	}
	return -1, false
}

// unquoted corta o comentário (` #` ou tab + `#`) e as pontas de um valor sem aspas.
func unquoted(s string) string {
	if strings.HasPrefix(s, "#") {
		return ""
	}
	for i := 1; i < len(s); i++ {
		if s[i] == '#' && (s[i-1] == ' ' || s[i-1] == '\t') {
			s = s[:i]
			break
		}
	}
	return strings.TrimSpace(s)
}
