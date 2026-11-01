package domain

import (
	"regexp"
	"strings"
)

// Ações de uma etapa da rodada (ADR-0080 item 4; `acoes.py` e `etapas.py` do agent-studio `@845764f`). A ação é uma linha de
// lista da seção `## Ações` do texto da etapa que abre com o id curto e `: ` (`a1: rodar o deploy`). Só a revisão vigente
// conta, id repetido vale o primeiro, e linha sem id ou fora da seção não é ação. Ação que cita `pedido:<id>` em código não
// tem caixa: o estado dela é o do pedido do canal. O texto é dado não confiável; aqui só se leem ids.

// StepActionMaxText é o teto do texto da etapa, o mesmo do `oute-swarm step publish`.
const StepActionMaxText = 32768

// StepAction é uma ação lida do texto de uma etapa.
type StepAction struct {
	ID       string
	Approval string // id do pedido do canal quando a ação o cita; vazio se não cita ou se o id não tem o formato do canal
}

var (
	actionLead    = regexp.MustCompile(`^([a-z][a-z0-9]{0,15}): `)
	actionItem    = regexp.MustCompile(`^(?:[-*]|\d{1,3}[.)]) (.*)$`)
	actionOrdered = regexp.MustCompile(`^\d{1,3}[.)] `)
	actionToken   = regexp.MustCompile("`[^`\\n]{1,500}`|\\*\\*[^*\\n]{1,500}\\*\\*|\\[[^\\]\\n]{1,500}\\]\\(https://[^\\s()<>\"'`]{1,1000}\\)")
	actionPedido  = regexp.MustCompile(`^pedido:([A-Za-z0-9][A-Za-z0-9._-]{0,199})$`)
	channelID     = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[a-z0-9-]+$`)
)

// stepControl são os controles e as marcas de direção que o agent-studio tira do texto antes de ler.
var stepControl = regexp.MustCompile(`[\x00-\x08\x0b-\x1f\x7f\x{200b}\x{200c}\x{200e}\x{200f}\x{2028}-\x{202e}\x{2060}-\x{2069}\x{feff}]`)

// ParseStepActions lê as ações do texto de uma etapa, na ordem do texto.
func ParseStepActions(text string) []StepAction {
	if len(text) > StepActionMaxText {
		return nil
	}
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	text = stepControl.ReplaceAllString(strings.ReplaceAll(text, "\t", "    "), "")
	var (
		out     []StepAction
		seen    = map[string]bool{}
		inSect  bool
		fence   bool
		items   []string
		ordered bool
	)
	flush := func() {
		if inSect {
			for _, it := range items {
				if a, ok := parseActionItem(it); ok && !seen[a.ID] {
					seen[a.ID] = true
					out = append(out, a)
				}
			}
		}
		items = nil
	}
	for _, line := range strings.Split(text, "\n") {
		s := strings.TrimSpace(line)
		switch {
		case fence:
			fence = !strings.HasPrefix(s, "```")
		case strings.HasPrefix(s, "```"):
			flush()
			fence = true
		case strings.HasPrefix(s, "## ") && isStepSection(strings.TrimSpace(s[3:])):
			flush()
			inSect = strings.TrimSpace(s[3:]) == "Ações"
		case strings.HasPrefix(s, "### ") && strings.TrimSpace(s[4:]) != "":
			flush()
		case s == "":
			flush()
		case actionItem.MatchString(s) && !strings.HasPrefix(line, "    "):
			isOrd := actionOrdered.MatchString(s)
			if len(items) > 0 && ordered != isOrd {
				flush()
			}
			ordered = isOrd
			items = append(items, strings.TrimSpace(actionItem.FindStringSubmatch(s)[1]))
		case len(items) > 0 && strings.HasPrefix(line, " "):
			items[len(items)-1] += " " + s
		default:
			flush() // parágrafo
		}
	}
	flush()
	return out
}

func isStepSection(name string) bool {
	return name == "Decisão" || name == "Ações" || name == "Detalhe"
}

func parseActionItem(item string) (StepAction, bool) {
	lead := actionLead.FindStringSubmatch(item)
	if lead == nil {
		return StepAction{}, false
	}
	a := StepAction{ID: lead[1]}
	for _, tok := range actionToken.FindAllString(item, -1) {
		if !strings.HasPrefix(tok, "`") {
			continue
		}
		if m := actionPedido.FindStringSubmatch(tok[1 : len(tok)-1]); m != nil {
			if channelID.MatchString(m[1]) {
				a.Approval = m[1]
			}
			break
		}
	}
	return a, true
}
