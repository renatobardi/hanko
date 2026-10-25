package dotenv

import (
	"reflect"
	"testing"
)

func pairs(r Result) [][2]string {
	var out [][2]string
	for _, e := range r.Entries {
		out = append(out, [2]string{e.Key, e.Value})
	}
	return out
}

// Comentários, aspas, export e linhas vazias importam certo (#62).
func TestParseEnvCompleto(t *testing.T) {
	in := "# cabeçalho\n\nexport DB_HOST=db.local\nDB_PASS=\"p#ss w\\\"rd\"   # comentário\nAPI='lit\\n$x'\n" +
		"TOKEN=abc # resto\nVAZIA=\nSPACED =  v1  \n\n  # indentado\nCERT=\"linha1\nlinha2\"\nHASH=#nada\nURL=https://x/a#frag\n"
	r := Parse(in)
	want := [][2]string{
		{"DB_HOST", "db.local"},
		{"DB_PASS", `p#ss w"rd`},
		{"API", `lit\n$x`},
		{"TOKEN", "abc"},
		{"VAZIA", ""},
		{"SPACED", "v1"},
		{"CERT", "linha1\nlinha2"},
		{"HASH", ""},
		{"URL", "https://x/a#frag"},
	}
	if got := pairs(r); !reflect.DeepEqual(got, want) {
		t.Fatalf("entradas = %q, quer %q", got, want)
	}
	if len(r.Skipped) != 0 {
		t.Fatalf("nada devia ser ignorado: %+v", r.Skipped)
	}
	if r.Entries[0].Line != 3 || r.Entries[6].Line != 11 {
		t.Fatalf("linhas = %d e %d", r.Entries[0].Line, r.Entries[6].Line)
	}
}

func TestParseEscapesECRLF(t *testing.T) {
	r := Parse("A=\"x\\ny\\tz\\$\\q\"\r\nB=2\r\n")
	want := [][2]string{{"A", "x\ny\tz$\\q"}, {"B", "2"}}
	if got := pairs(r); !reflect.DeepEqual(got, want) {
		t.Fatalf("entradas = %q, quer %q", got, want)
	}
}

func TestParseLinhasRuins(t *testing.T) {
	r := Parse("SEMIGUAL\n1BAD=x\nOK=1\nQ=\"aberta\nC=3\n")
	if got, want := pairs(r), [][2]string{{"OK", "1"}, {"C", "3"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("entradas = %q, quer %q", got, want)
	}
	lines := []int{}
	for _, s := range r.Skipped {
		lines = append(lines, s.Line)
	}
	if !reflect.DeepEqual(lines, []int{1, 2, 4}) {
		t.Fatalf("linhas ignoradas = %v (%+v)", lines, r.Skipped)
	}
}

func TestParseTextoDepoisDasAspas(t *testing.T) {
	r := Parse("A=\"x\" y\nB=\"z\" # ok\n")
	if got, want := pairs(r), [][2]string{{"B", "z"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("entradas = %q", got)
	}
	if len(r.Skipped) != 1 || r.Skipped[0].Line != 1 {
		t.Fatalf("ignoradas = %+v", r.Skipped)
	}
}

func TestParseChaveRepetidaFicaComOUltimo(t *testing.T) {
	r := Parse("A=1\nB=2\nA=3\n")
	if got, want := pairs(r), [][2]string{{"A", "3"}, {"B", "2"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("entradas = %q", got)
	}
}

func TestParseBOMEVazio(t *testing.T) {
	if r := Parse("\ufeffA=1"); len(r.Entries) != 1 || r.Entries[0].Key != "A" {
		t.Fatalf("BOM: %+v", r)
	}
	if r := Parse(""); len(r.Entries) != 0 || len(r.Skipped) != 0 {
		t.Fatalf("vazio: %+v", r)
	}
}
