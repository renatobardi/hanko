package decision

import (
	"fmt"
	"strings"
)

// MaxFastReplyRunes é o teto de uma resposta rápida.
const MaxFastReplyRunes = 600

// FastPrompt pede uma resposta curta a uma mensagem de canal externo (ADR-0046 §D.5). snapshot é o retrato da empresa
// (contagens e os gates pendentes da pessoa), montado pelo servidor: é o único dado que o modelo pode usar. O modelo
// responde ou escala para a persona; ele não decide, aprova nem executa nada.
func FastPrompt(text, snapshot string) Prompt {
	sys := "Você atende mensagens curtas de uma pessoa da empresa que chegam por um aplicativo de conversa. " +
		"Se a pergunta se responde só com o retrato da empresa abaixo (quantas tasks abertas, quais gates esperam decisão, " +
		"como usar o canal), responda em até 3 frases, no idioma da mensagem. Para qualquer pedido de trabalho, análise, " +
		"código, opinião, ou se faltar informação no retrato, escale para a persona. Você nunca aprova, rejeita nem executa " +
		"nada; para decidir um gate, diga que a pessoa use os botões da mensagem do gate ou o comando /pendentes. " +
		dataNotice + ` Responda só com um objeto JSON: {"action":"reply"|"escalate","reply":"<texto, vazio se escalar>"}.` +
		"\n\nRetrato da empresa:\n" + Clip(snapshot, 2000)
	return Prompt{System: sys, User: "<dados>\n" + Clip(text, MaxInputRunes) + "\n</dados>"}
}

// FastResult é a decisão do modelo: responder ou escalar.
type FastResult struct {
	Action string `json:"action"`
	Reply  string `json:"reply"`
}

// ParseFast valida a resposta. Resposta sem texto vira escalada.
func ParseFast(content string) (FastResult, error) {
	var r FastResult
	if err := decodeObject(content, &r); err != nil {
		return r, err
	}
	r.Action = strings.ToLower(strings.TrimSpace(r.Action))
	switch r.Action {
	case "reply":
		r.Reply = Clip(strings.TrimSpace(r.Reply), MaxFastReplyRunes)
		if r.Reply == "" {
			r.Action = "escalate"
		}
	case "escalate":
		r.Reply = ""
	default:
		return r, fmt.Errorf("o modelo sugeriu a ação %q, que não é reply nem escalate", Clip(r.Action, 30))
	}
	return r, nil
}
