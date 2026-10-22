package decision

import (
	"strings"
	"testing"
)

func TestParseFast(t *testing.T) {
	r, err := ParseFast("```json\n{\"action\":\"reply\",\"reply\":\"  Há 3 tasks abertas.  \"}\n```")
	if err != nil || r.Action != "reply" || r.Reply != "Há 3 tasks abertas." {
		t.Fatalf("%+v %v", r, err)
	}
	if r, _ = ParseFast(`{"action":"reply","reply":"   "}`); r.Action != "escalate" {
		t.Errorf("reply vazio deveria escalar: %+v", r)
	}
	if r, _ = ParseFast(`{"action":"ESCALATE","reply":"ignorado"}`); r.Action != "escalate" || r.Reply != "" {
		t.Errorf("escalate: %+v", r)
	}
	for _, bad := range []string{``, `nada`, `{"action":"approve","reply":"feito"}`, `{"reply":"x"}`} {
		if _, err := ParseFast(bad); err == nil {
			t.Errorf("%q aceito", bad)
		}
	}
	long := strings.Repeat("a", MaxFastReplyRunes+100)
	if r, _ = ParseFast(`{"action":"reply","reply":"` + long + `"}`); len([]rune(r.Reply)) > MaxFastReplyRunes+1 {
		t.Errorf("resposta sem teto: %d", len([]rune(r.Reply)))
	}
	p := FastPrompt("quantas tasks?", "tasks abertas: 3")
	if !strings.Contains(p.System, "tasks abertas: 3") || !strings.Contains(p.System, "nunca aprova") || !strings.Contains(p.User, "<dados>") {
		t.Errorf("prompt: %+v", p)
	}
}
