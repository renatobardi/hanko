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
		{"a'; drop table x; --", `('a' & 'drop' & 'table' & 'x')`}, // "x" tem 1 runa: sem prefixo
		{"deploy a", `('deploy' & 'a')`},
		{"deploy ab", `('deploy' & 'ab':*)`},
		{"co\u00ADoperar", `('cooperar':*)`},
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

// Prefixo de uma letra varre o índice inteiro: só vale a partir de MinPrefixRunes; comentários pedem mais (WithMinPrefix).
func TestPrefixoMinimo(t *testing.T) {
	q, err := Parse("erro de")
	if err != nil {
		t.Fatal(err)
	}
	if q.TSQuery != `('erro' & 'de':*)` || !q.Terms[1].Prefix {
		t.Fatalf("2 runas ainda casam por prefixo: %s %+v", q.TSQuery, q.Terms)
	}
	c := q.WithMinPrefix(3)
	if c.TSQuery != `('erro' & 'de')` || c.Terms[1].Prefix {
		t.Errorf("com mínimo 3 'de' é exato (e não é destacado como prefixo): %s %+v", c.TSQuery, c.Terms)
	}
	if q.TSQuery != `('erro' & 'de':*)` {
		t.Error("WithMinPrefix não pode alterar a consulta original")
	}
	if got := c.WithMinPrefix(0).TSQuery; got != `('erro' & 'de':*)` {
		t.Errorf("mínimo abaixo do piso vale o piso: %s", got)
	}
	// runa, não byte: "só" tem 3 bytes e 2 runas
	if q, _ := Parse("só"); q.TSQuery != `('so':*)` {
		t.Errorf("duas runas: %s", q.TSQuery)
	}
	// frase com prefixo no fim
	if q, _ := Parse(`"erro de r"`); q.TSQuery != `(('erro' <-> 'de' <-> 'r'))` {
		t.Errorf("frase terminando em 1 runa: %s", q.TSQuery)
	}
}

// Caracteres de formatação (Cf) somem da dobra e do texto mostrado; offsets do destaque seguem alinhados.
func TestFoldRemoveFormatacao(t *testing.T) {
	in := "co\u00ADoperar a\u200Db \uFEFFx\u200Bz"
	if got := Fold(in); got != "cooperar ab xz" {
		t.Errorf("Fold = %q", got)
	}
	q, _ := Parse("cooperar")
	segs := Snippet("vamos co\u00ADoperar já", q, 100)
	if got := Plain(segs); got != "vamos cooperar já" {
		t.Errorf("texto mostrado = %q", got)
	}
	var marked []string
	for _, sg := range segs {
		if sg.Match {
			marked = append(marked, sg.Text)
		}
	}
	if len(marked) != 1 || marked[0] != "cooperar" {
		t.Errorf("marcas = %q", marked)
	}
}

// Números-letra (Nl), marcas combinantes e letras circuladas são palavra, como no tokenizador do Postgres.
func TestPalavrasUnicode(t *testing.T) {
	cases := map[string][]string{
		"Ⅷ capítulo":  {"ⅷ", "capitulo"},
		"e\u0301cole": {"e\u0301cole"}, // "é" decomposto não parte a palavra
		"Ⓐbc def":     {"ⓐbc", "def"},
		"a\u2019b":    {"a", "b"}, // apóstrofo tipográfico separa
		"日本語のテキスト":    {"日本語のテキスト"},
	}
	for in, want := range cases {
		got := words(Fold(in))
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("words(%q) = %q, quer %q", in, got, want)
		}
	}
}
