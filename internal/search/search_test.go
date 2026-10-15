package search

import (
	"errors"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct{ in, want string }{
		{"deploy", `('deploy':*)`},
		{"fix  bug", `('fix' & 'bug':*)`},
		{"Ação do Usuário", `('acao' & 'do' & 'usuario':*)`},
		{`"erro de rede" api`, `(('erro' <-> 'de' <-> 'rede') & 'api':*)`},
		{"foo-bar", `(('foo' <-> 'bar':*))`},
		{"deploy -staging", `('deploy':* & !'staging')`},
		{"login OR logout", `('login') | ('logout':*)`},
		{"a'; drop table x; --", `('a' & 'drop' & 'table' & 'x':*)`},
	}
	for _, c := range cases {
		q, err := Parse(c.in)
		if err != nil {
			t.Errorf("Parse(%q): %v", c.in, err)
			continue
		}
		if q.TSQuery != c.want {
			t.Errorf("Parse(%q) = %s, quer %s", c.in, q.TSQuery, c.want)
		}
	}
}

func TestParseVazia(t *testing.T) {
	for _, in := range []string{"", "   ", "---", "-staging", `"" ()`, "!!! ???"} {
		if _, err := Parse(in); !errors.Is(err, ErrEmpty) {
			t.Errorf("Parse(%q) err = %v, quer ErrEmpty", in, err)
		}
	}
}

func TestParseLimites(t *testing.T) {
	q, err := Parse(strings.Repeat("palavra ", 100))
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Terms) != maxWords {
		t.Errorf("termos = %d, quer %d", len(q.Terms), maxWords)
	}
	long, err := Parse(strings.Repeat("x", 1000))
	if err != nil || len([]rune(long.Terms[0].Text)) != maxWordLen {
		t.Errorf("palavra longa não foi cortada: %v", err)
	}
}

func TestFold(t *testing.T) {
	if got := Fold("AÇÃO, Ünï-Ñ src/a.go"); got != "acao  uni n src a go" {
		t.Errorf("Fold = %q", got)
	}
}

func TestSnippetMarcaSemHTML(t *testing.T) {
	q, _ := Parse("acao")
	segs := Snippet("Fizemos a <b>AÇÃO</b> corretiva & seguimos.", q, 200)
	var marked []string
	for _, s := range segs {
		if s.Match {
			marked = append(marked, s.Text)
		}
	}
	if len(marked) != 1 || marked[0] != "AÇÃO" {
		t.Fatalf("marcas = %q (segs %+v)", marked, segs)
	}
	if got := Plain(segs); !strings.Contains(got, "<b>") {
		t.Errorf("o texto original (com <b>) deve ser preservado para o templ escapar: %q", got)
	}
}

func TestSnippetJanelaEPrefixo(t *testing.T) {
	long := strings.Repeat("lorem ipsum dolor ", 50) + "o deploy falhou na produção " + strings.Repeat("sit amet ", 50)
	q, _ := Parse("deplo")
	segs := Snippet(long, q, 80)
	got := Plain(segs)
	if !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "…") || !strings.Contains(got, "deploy") {
		t.Errorf("janela inesperada: %q", got)
	}
	if len([]rune(got)) > 200 {
		t.Errorf("trecho longo demais: %d", len([]rune(got)))
	}
	// sem casamento: início do texto, sem marcas
	q2, _ := Parse("zzz")
	for _, s := range Snippet(long, q2, 40) {
		if s.Match {
			t.Error("não deveria haver marca")
		}
	}
	if Snippet("", q, 40) != nil {
		t.Error("texto vazio deve dar nil")
	}
}

func TestMarkTitulo(t *testing.T) {
	q, _ := Parse("api rate")
	segs := Mark("Limitar  rate da API pública", q)
	if Plain(segs) != "Limitar rate da API pública" {
		t.Errorf("texto: %q", Plain(segs))
	}
	n := 0
	for _, s := range segs {
		if s.Match {
			n++
		}
	}
	if n != 2 {
		t.Errorf("marcas = %d, quer 2", n)
	}
}
