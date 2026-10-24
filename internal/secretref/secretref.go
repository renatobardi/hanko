// Package secretref reconhece e expande referências a segredos (`${secret:NOME}` e `${secret:user/NOME}`,
// ADR-0050; `${secret:ITEM#campo}` e `${secret:user/ITEM#campo}`, ADR-0068 §1.4). É puro: não lê banco nem rede.
// Quem resolve o valor é o service, que passa uma função de busca.
package secretref

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// NamePattern é o formato do nome de um segredo (o mesmo da constraint do banco).
const NamePattern = `[A-Za-z][A-Za-z0-9_.-]{0,63}`

// FieldPattern é o formato do nome de um campo (o mesmo da constraint do banco, ADR-0068 §1.1).
const FieldPattern = `[A-Za-z][A-Za-z0-9_-]{0,63}`

// DefaultField é o campo de uma referência sem `#campo`: o segredo do ADR-0050 vira um item com esse campo
// (ADR-0068 §1.4: `${secret:X}` continua resolvendo como hoje).
const DefaultField = "value"

var (
	nameRe  = regexp.MustCompile(`^` + NamePattern + `$`)
	fieldRe = regexp.MustCompile(`^` + FieldPattern + `$`)
	refRe   = regexp.MustCompile(`\$\{secret:(user/)?(` + NamePattern + `)(?:#(` + FieldPattern + `))?\}`)
)

// Ref é uma referência encontrada em um texto.
type Ref struct {
	Name string
	// Field é o campo do item; vazio vale DefaultField. O `#` não pertence ao nome do segredo (ADR-0050 §A.1),
	// então a gramática não é ambígua.
	Field string
	User  bool // ${secret:user/NOME}
}

// FieldName devolve o campo a ler: o da referência ou o padrão.
func (r Ref) FieldName() string {
	if r.Field == "" {
		return DefaultField
	}
	return r.Field
}

// String devolve a forma textual `${secret:...}`, com `#campo` só quando difere do padrão.
func (r Ref) String() string {
	var b strings.Builder
	b.WriteString("${secret:")
	if r.User {
		b.WriteString("user/")
	}
	b.WriteString(r.Name)
	if r.Field != "" && r.Field != DefaultField {
		b.WriteByte('#')
		b.WriteString(r.Field)
	}
	b.WriteByte('}')
	return b.String()
}

// ValidName informa se name serve de nome de segredo.
func ValidName(name string) bool { return nameRe.MatchString(name) }

// ValidFieldName informa se name serve de nome de campo.
func ValidFieldName(name string) bool { return fieldRe.MatchString(name) }

// Has informa se o texto contém alguma referência.
func Has(text string) bool { return strings.Contains(text, "${secret:") && refRe.MatchString(text) }

// IsRef informa se o texto inteiro é uma única referência.
func IsRef(text string) bool {
	loc := refRe.FindStringIndex(text)
	return loc != nil && loc[0] == 0 && loc[1] == len(text)
}

// Find lista as referências do texto, sem repetição, na ordem em que aparecem.
func Find(text string) []Ref {
	var out []Ref
	seen := map[Ref]bool{}
	for _, m := range refRe.FindAllStringSubmatch(text, -1) {
		r := Ref{Name: m[2], Field: m[3], User: m[1] != ""}
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	return out
}

// ErrUnresolved é o erro base de uma referência que não pôde ser resolvida; o texto nunca leva valor.
var ErrUnresolved = errors.New("segredo não resolvido")

// Expand troca cada referência do texto pelo valor que lookup devolve. Se alguma falha, devolve o erro (com o nome
// do segredo, nunca o valor) e o texto original. used lista os valores inseridos, para a redação de segredos.
func Expand(text string, lookup func(Ref) (string, error)) (out string, used []string, err error) {
	if !Has(text) {
		return text, nil, nil
	}
	var firstErr error
	cache := map[Ref]string{}
	out = refRe.ReplaceAllStringFunc(text, func(m string) string {
		sm := refRe.FindStringSubmatch(m)
		r := Ref{Name: sm[2], Field: sm[3], User: sm[1] != ""}
		if v, ok := cache[r]; ok {
			return v
		}
		v, e := lookup(r)
		if e != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("%w: %s: %w", ErrUnresolved, r.String(), e)
			}
			return m
		}
		cache[r] = v
		used = append(used, v)
		return v
	})
	if firstErr != nil {
		return text, nil, firstErr
	}
	return out, used, nil
}

// ExpandJSON percorre um valor decodificado de JSON (map, slice, string...) expandindo as referências dos textos.
// Devolve a cópia expandida e os valores usados. Chaves não são expandidas.
func ExpandJSON(v any, lookup func(Ref) (string, error)) (any, []string, error) {
	var used []string
	var walk func(x any) (any, error)
	walk = func(x any) (any, error) {
		switch t := x.(type) {
		case string:
			s, u, err := Expand(t, lookup)
			if err != nil {
				return nil, err
			}
			used = append(used, u...)
			return s, nil
		case map[string]any:
			out := make(map[string]any, len(t))
			for k, e := range t {
				ne, err := walk(e)
				if err != nil {
					return nil, err
				}
				out[k] = ne
			}
			return out, nil
		case []any:
			out := make([]any, len(t))
			for i, e := range t {
				ne, err := walk(e)
				if err != nil {
					return nil, err
				}
				out[i] = ne
			}
			return out, nil
		default:
			return x, nil
		}
	}
	out, err := walk(v)
	if err != nil {
		return nil, nil, err
	}
	return out, used, nil
}
