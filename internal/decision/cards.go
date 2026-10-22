package decision

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// MaxCardSummaryBytes é o tamanho máximo do resumo de um card de status (o mesmo teto do card).
const MaxCardSummaryBytes = 64 << 10

// CardSummaryPrompt pede o resumo de um card de status (ADR-0048): a pergunta salva e o retrato das tasks são dados.
func CardSummaryPrompt(interest, tasks string) Prompt {
	sys := "Você mantém um card de status de uma empresa: um resumo curto, em markdown, que responde à pergunta salva por quem " +
		"acompanha o trabalho, usando só as tasks listadas. " + dataNotice +
		" Cite as tasks pela chave (ex. HNK-12). Não invente tasks nem fatos que não estejam nos dados; se faltar informação, diga. " +
		`Responda só com um objeto JSON: {"title":"<título curto, até 80 caracteres>","summary":"<resumo em markdown>","mentioned":["<chave de task citada>"]}, ` +
		"no idioma da pergunta."
	return Prompt{System: sys, User: "<dados>\n## Pergunta\n\n" + Clip(interest, MaxInputRunes) + "\n\n" + tasks + "\n</dados>"}
}

// CardSummaryResult é o resumo devolvido pelo modelo.
type CardSummaryResult struct {
	Title     string   `json:"title"`
	Summary   string   `json:"summary"`
	Mentioned []string `json:"mentioned"`
}

// ParseCardSummary valida o resumo: não vazio, UTF-8, dentro do teto.
func ParseCardSummary(content string) (CardSummaryResult, error) {
	var r CardSummaryResult
	if err := decodeObject(content, &r); err != nil {
		return r, err
	}
	r.Title = Clip(strings.Join(strings.Fields(r.Title), " "), 80)
	r.Summary = strings.TrimSpace(r.Summary)
	if r.Summary == "" {
		return r, errors.New("o modelo não devolveu um resumo")
	}
	if !utf8.ValidString(r.Summary) {
		return r, errors.New("o resumo do modelo não é texto UTF-8")
	}
	if len(r.Summary) > MaxCardSummaryBytes {
		r.Summary = strings.ToValidUTF8(r.Summary[:MaxCardSummaryBytes], "")
	}
	if len(r.Mentioned) > 200 {
		r.Mentioned = r.Mentioned[:200]
	}
	return r, nil
}
