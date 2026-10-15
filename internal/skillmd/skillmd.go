// Package skillmd lê o formato SKILL.md: frontmatter YAML com name e description, seguido do corpo markdown
// (ADR-0029). Serve à CLI, à API e à UI para aceitar um SKILL.md inteiro em vez de campos separados.
package skillmd

import (
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/renatobardi/hanko/internal/domain"
)

// Doc é um SKILL.md lido.
type Doc struct {
	Name        string
	Description string
	Body        string
	// HasFrontmatter: o texto tinha um bloco --- ... ---.
	HasFrontmatter bool
}

const bom = "\xef\xbb\xbf"

// MaxSourceBytes é o maior SKILL.md que Parse aceita: o corpo (domain.MaxSkillBodyBytes) mais folga para o
// frontmatter. O teto vale antes do parse YAML, que não deve processar um texto arbitrariamente grande.
const MaxSourceBytes = domain.MaxSkillBodyBytes + 8<<10

// Parse separa o frontmatter do corpo. Sem frontmatter, o texto todo é o corpo. Um frontmatter aberto e não
// fechado, ou com YAML inválido, é erro (melhor recusar do que gravar o YAML dentro do corpo).
func Parse(src string) (Doc, error) {
	if len(src) > MaxSourceBytes {
		return Doc{}, fmt.Errorf("at most %d KiB (the body plus the frontmatter)", MaxSourceBytes>>10)
	}
	src = strings.TrimPrefix(src, bom)
	src = strings.ReplaceAll(src, "\r\n", "\n")
	lines := strings.Split(strings.TrimLeft(src, "\n"), "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], " \t") != "---" {
		return Doc{Body: strings.TrimSpace(src)}, nil
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " \t") == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return Doc{}, errors.New("frontmatter aberto com --- e não fechado")
	}
	var fm struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal([]byte(strings.Join(lines[1:end], "\n")), &fm); err != nil {
		return Doc{}, errors.New("frontmatter YAML inválido")
	}
	return Doc{
		Name:           strings.TrimSpace(fm.Name),
		Description:    strings.Join(strings.Fields(fm.Description), " "),
		Body:           strings.TrimSpace(strings.Join(lines[end+1:], "\n")),
		HasFrontmatter: true,
	}, nil
}
