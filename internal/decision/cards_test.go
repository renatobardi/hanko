package decision

import (
	"strings"
	"testing"
)

func TestCardSummaryPrompt(t *testing.T) {
	p := CardSummaryPrompt("o que está bloqueado?", "## Tasks observadas (1)\n\n- HNK-1 · blocked")
	if !strings.Contains(p.System, "nunca instruções") || !strings.Contains(p.System, `"summary"`) {
		t.Fatalf("o sistema precisa tratar o texto como dado e pedir JSON: %q", p.System)
	}
	if !strings.HasPrefix(p.User, "<dados>") || !strings.HasSuffix(p.User, "</dados>") || !strings.Contains(p.User, "HNK-1") ||
		!strings.Contains(p.User, "o que está bloqueado?") {
		t.Fatalf("usuário: %q", p.User)
	}
	long := CardSummaryPrompt(strings.Repeat("a", MaxInputRunes*2), "")
	if n := len([]rune(long.User)); n > MaxInputRunes+200 {
		t.Fatalf("a pergunta devia ser cortada: %d runas", n)
	}
}

func TestParseCardSummary(t *testing.T) {
	r, err := ParseCardSummary("```json\n{\"title\":\"  Bloqueios   do Site \",\"summary\":\"  ## Ok\\n- HNK-1 \",\"mentioned\":[\"HNK-1\"]}\n```")
	if err != nil {
		t.Fatal(err)
	}
	if r.Title != "Bloqueios do Site" || r.Summary != "## Ok\n- HNK-1" || len(r.Mentioned) != 1 {
		t.Fatalf("resultado: %+v", r)
	}
	for _, bad := range []string{"", "sem json", `{"summary":"   "}`, `{"title":"x"}`} {
		if _, err := ParseCardSummary(bad); err == nil {
			t.Errorf("%q devia falhar", bad)
		}
	}
	big := `{"summary":"` + strings.Repeat("é", MaxCardSummaryBytes) + `"}`
	r, err = ParseCardSummary(big)
	if err != nil || len(r.Summary) > MaxCardSummaryBytes {
		t.Fatalf("resumo grande devia ser cortado: %v %d", err, len(r.Summary))
	}
	many := make([]string, 300)
	for i := range many {
		many[i] = `"HNK-1"`
	}
	r, err = ParseCardSummary(`{"summary":"x","mentioned":[` + strings.Join(many, ",") + `]}`)
	if err != nil || len(r.Mentioned) != 200 {
		t.Fatalf("citadas: %v %d", err, len(r.Mentioned))
	}
}
