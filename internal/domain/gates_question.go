package domain

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Gate de pergunta (ADR-0041): a persona monta um formulário (escolha única, múltipla ou texto, N campos) e o humano
// responde de uma vez; a resposta estruturada volta no wake. Regras puras, sem infraestrutura.

// QuestionFieldType é o tipo de um campo do formulário.
type QuestionFieldType string

const (
	QuestionSingle QuestionFieldType = "single" // escolha única entre as opções
	QuestionMulti  QuestionFieldType = "multi"  // uma ou mais opções
	QuestionText   QuestionFieldType = "text"   // texto livre
)

const (
	// MaxQuestionFields é o teto de campos de um formulário.
	MaxQuestionFields = 12
	// MaxQuestionOptions é o teto de opções de um campo de escolha.
	MaxQuestionOptions = 20
	// MaxQuestionLabel limita o rótulo de campo e de opção.
	MaxQuestionLabel = 300
	// MaxQuestionAnswer limita a resposta de texto livre (em caracteres).
	MaxQuestionAnswer = 10000
)

var questionKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)

// QuestionOption é uma opção de um campo de escolha. Value é o que volta na resposta.
type QuestionOption struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"` // vazio = o próprio Value
}

// QuestionField é um campo do formulário.
type QuestionField struct {
	Key      string            `json:"key"`
	Label    string            `json:"label"`
	Type     QuestionFieldType `json:"type"`
	Options  []QuestionOption  `json:"options,omitempty"`
	Required bool              `json:"required,omitempty"`
	// Help é uma dica curta exibida sob o campo.
	Help string `json:"help,omitempty"`
}

// QuestionForm é o formulário de um gate de pergunta.
type QuestionForm struct {
	Fields []QuestionField `json:"fields"`
}

// QuestionAnswer é a resposta: chave do campo → valores (um só em single e text; vários em multi).
type QuestionAnswer struct {
	Values map[string][]string `json:"values"`
}

// Validate confere o formulário: chaves únicas e legíveis, tipos conhecidos, opções coerentes com o tipo.
func (f QuestionForm) Validate() error {
	if len(f.Fields) == 0 {
		return Invalid("form", "pelo menos um campo")
	}
	if len(f.Fields) > MaxQuestionFields {
		return Invalid("form", fmt.Sprintf("no máximo %d campos", MaxQuestionFields))
	}
	seen := map[string]bool{}
	for i, fl := range f.Fields {
		at := fmt.Sprintf("form.fields[%d]", i)
		if !questionKeyRe.MatchString(fl.Key) {
			return Invalid(at+".key", "minúsculas, dígitos e _, começando por letra (até 40)")
		}
		if seen[fl.Key] {
			return Invalid(at+".key", fmt.Sprintf("chave repetida %q", fl.Key))
		}
		seen[fl.Key] = true
		if l := strings.TrimSpace(fl.Label); l == "" || utf8.RuneCountInString(l) > MaxQuestionLabel {
			return Invalid(at+".label", fmt.Sprintf("1 a %d caracteres", MaxQuestionLabel))
		}
		switch fl.Type {
		case QuestionText:
			if len(fl.Options) > 0 {
				return Invalid(at+".options", "campo de texto não tem opções")
			}
		case QuestionSingle, QuestionMulti:
			if len(fl.Options) < 1 || len(fl.Options) > MaxQuestionOptions {
				return Invalid(at+".options", fmt.Sprintf("de 1 a %d opções", MaxQuestionOptions))
			}
			vals := map[string]bool{}
			for j, o := range fl.Options {
				v := strings.TrimSpace(o.Value)
				if v == "" || utf8.RuneCountInString(v) > MaxQuestionLabel || utf8.RuneCountInString(o.Label) > MaxQuestionLabel {
					return Invalid(fmt.Sprintf("%s.options[%d]", at, j), fmt.Sprintf("valor e rótulo até %d caracteres, valor obrigatório", MaxQuestionLabel))
				}
				if vals[v] {
					return Invalid(fmt.Sprintf("%s.options[%d]", at, j), fmt.Sprintf("valor repetido %q", v))
				}
				vals[v] = true
			}
		default:
			return Invalid(at+".type", fmt.Sprintf("tipo desconhecido %q (single, multi ou text)", fl.Type))
		}
	}
	return nil
}

// Field devolve o campo de chave key.
func (f QuestionForm) Field(key string) (QuestionField, bool) {
	for _, fl := range f.Fields {
		if fl.Key == key {
			return fl, true
		}
	}
	return QuestionField{}, false
}

// CheckAnswer confere a resposta contra o formulário e a devolve normalizada: valores aparados, campos opcionais
// vazios omitidos. Chave que não existe no formulário, opção desconhecida, mais de um valor em single/text e campo
// obrigatório vazio são erros de validação.
func (f QuestionForm) CheckAnswer(in map[string][]string) (QuestionAnswer, error) {
	for k := range in {
		if _, ok := f.Field(k); !ok {
			return QuestionAnswer{}, Invalid("answers."+k, "campo que não existe no formulário")
		}
	}
	out := QuestionAnswer{Values: map[string][]string{}}
	for _, fl := range f.Fields {
		var vals []string
		for _, v := range in[fl.Key] {
			if v = strings.TrimSpace(v); v != "" {
				vals = append(vals, v)
			}
		}
		at := "answers." + fl.Key
		if len(vals) == 0 {
			if fl.Required {
				return QuestionAnswer{}, Invalid(at, "obrigatório")
			}
			continue
		}
		switch fl.Type {
		case QuestionText:
			if len(vals) > 1 {
				return QuestionAnswer{}, Invalid(at, "uma resposta só")
			}
			if utf8.RuneCountInString(vals[0]) > MaxQuestionAnswer {
				return QuestionAnswer{}, Invalid(at, fmt.Sprintf("até %d caracteres", MaxQuestionAnswer))
			}
		case QuestionSingle, QuestionMulti:
			if fl.Type == QuestionSingle && len(vals) > 1 {
				return QuestionAnswer{}, Invalid(at, "escolha uma opção só")
			}
			ok := map[string]bool{}
			for _, o := range fl.Options {
				ok[strings.TrimSpace(o.Value)] = true
			}
			seen := map[string]bool{}
			for _, v := range vals {
				if !ok[v] {
					return QuestionAnswer{}, Invalid(at, fmt.Sprintf("opção desconhecida %q", v))
				}
				if seen[v] {
					return QuestionAnswer{}, Invalid(at, fmt.Sprintf("opção repetida %q", v))
				}
				seen[v] = true
			}
		}
		out.Values[fl.Key] = vals
	}
	return out, nil
}

// Render escreve a resposta em markdown legível (rótulo do campo → resposta), na ordem do formulário. É o texto que
// vai para a thread da task e para a memória.
func (f QuestionForm) Render(a QuestionAnswer) string {
	var b strings.Builder
	for _, fl := range f.Fields {
		vals := a.Values[fl.Key]
		fmt.Fprintf(&b, "- **%s**: ", strings.TrimSpace(fl.Label))
		if len(vals) == 0 {
			b.WriteString("_(sem resposta)_\n")
			continue
		}
		labels := make([]string, 0, len(vals))
		for _, v := range vals {
			labels = append(labels, optionLabel(fl, v))
		}
		b.WriteString(strings.Join(labels, ", "))
		b.WriteString("\n")
	}
	return b.String()
}

func optionLabel(fl QuestionField, v string) string {
	if fl.Type == QuestionText {
		return v
	}
	for _, o := range fl.Options {
		if strings.TrimSpace(o.Value) == v && strings.TrimSpace(o.Label) != "" {
			return fmt.Sprintf("%s (%s)", strings.TrimSpace(o.Label), v)
		}
	}
	return v
}
