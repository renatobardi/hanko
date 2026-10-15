package domain

import (
	"regexp"
	"strings"
)

var verdictRe = regexp.MustCompile(`(?i)^[\s*_#>\-]*veredito[\s*_]*:[\s*_]*(aprovad[oa]|reprovad[oa]|approved|rejected)\b[\s*_.]*(.*)$`)

// ParseVerdict lê o veredito da resposta final do revisor: a primeira linha "VEREDITO: APROVADO" ou
// "VEREDITO: REPROVADO" (caixa livre, markdown em volta tolerado). O resto do texto é a justificativa.
// ok é falso quando nenhuma linha traz um veredito válido: nesse caso o Hanko não presume nada.
func ParseVerdict(text string) (v ReviewVerdict, justification string, ok bool) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i, l := range lines {
		m := verdictRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		switch strings.ToLower(m[1])[0] {
		case 'a':
			v = ReviewApproved
		default:
			v = ReviewRejected
		}
		rest := append([]string{m[2]}, lines[i+1:]...)
		before := strings.Join(lines[:i], "\n")
		just := strings.TrimSpace(strings.TrimSpace(before) + "\n" + strings.TrimSpace(strings.Join(rest, "\n")))
		return v, just, true
	}
	return "", strings.TrimSpace(text), false
}
