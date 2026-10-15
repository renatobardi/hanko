package service_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/search"
	"github.com/renatobardi/hanko/internal/service"
)

// A dobra de Go e a de SQL precisam concordar em TODO ponto de código que ambas prometem tratar: o Latin com acento
// (tabela), a pontuação ASCII (vira espaço) e os caracteres de formatação Cf (somem). Igualdade estrita, sem EqualFold.
func TestFoldParidadeGoSQLTodosOsPontos(t *testing.T) {
	e := newEnv(t)
	rows, err := e.svc.Pool.Query(context.Background(),
		`SELECT i, hanko_fold(chr(i)) FROM generate_series(1, 1114111) i WHERE i NOT BETWEEN 55296 AND 57343`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var bad []string
	n := 0
	for rows.Next() {
		var i int
		var sqlOut string
		if err := rows.Scan(&i, &sqlOut); err != nil {
			t.Fatal(err)
		}
		n++
		r := rune(i) //nolint:gosec // i vem de generate_series(1, 1114111): cabe em rune
		goOut := search.Fold(string(r))
		if !promised(r) {
			continue
		}
		if goOut != sqlOut {
			bad = append(bad, fmt.Sprintf("U+%04X go=%q sql=%q", r, goOut, sqlOut))
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if n < 1_000_000 {
		t.Fatalf("só %d pontos de código lidos", n)
	}
	if len(bad) > 0 {
		t.Errorf("%d pontos divergem (primeiros): %v", len(bad), bad[:min(len(bad), 10)])
	}
}

// promised: pontos em que Go e SQL prometem o mesmo resultado — Latin (U+0001..U+024F, fora os que dependem de
// locale), pontuação ASCII e os Cf. O resto (grego, cirílico, CJK...) só sofre o lower() de cada lado.
func promised(r rune) bool {
	if r == 'ß' || r == 'œ' || r == 'İ' || r == 'Œ' || r == 'ẞ' { // lower() depende do locale do banco
		return false
	}
	return r <= 0x24F || unicode.Is(unicode.Cf, r)
}

// Textos exóticos: o que o banco tokeniza (to_tsvector sobre hanko_fold) é o que o Go entende como palavras.
func TestTokenizacaoParidadeGoSQL(t *testing.T) {
	e := newEnv(t)
	samples := []string{
		"co\u00ADoperar em equipe",               // hífen suave
		"fa\u200Dlha\u200Bcurta \uFEFFinicio",    // ZWJ, ZWSP, BOM
		"\u200Fdireita\u200E esquerda",           // marcas de direção
		"Capítulo Ⅷ e Ⅻ, ⅷ romano",               // números-letra (Nl)
		"cafe\u0301 e\u0300 acento solto",        // marcas combinantes
		"Ⓐbc ⓓef círculo",                        // letras circuladas
		"não\u2019é d\u2019água “aspas” «fr»",    // pontuação tipográfica
		"Ação São João ÇÃO ÀÉÎÕÜ",                // Latin acentuado
		"src/main.go a@b.com HNK-12 foo_bar x.y", // pontuação ASCII
		"tab\tnova\nlinha  espaços",
		"100% 3,14 R$ 5 U+00AD",
	}
	for _, in := range samples {
		var sqlWords []string
		err := e.svc.Pool.QueryRow(context.Background(),
			`SELECT coalesce(array_agg(lexeme ORDER BY lexeme), '{}') FROM unnest(to_tsvector('simple', hanko_fold($1)))`, in).Scan(&sqlWords)
		if err != nil {
			t.Fatal(err)
		}
		// Parse devolve as palavras positivas na ordem; sem aspas/hífen no começo da palavra elas são as do texto
		q, err := search.Parse(strings.ReplaceAll(strings.ReplaceAll(in, `"`, " "), "“", " "))
		var goWords []string
		if err == nil {
			for _, tm := range q.Terms {
				goWords = append(goWords, tm.Text)
			}
		}
		slices.Sort(goWords)
		goWords = slices.Compact(goWords)
		if strings.Join(goWords, "|") != strings.Join(sqlWords, "|") {
			t.Errorf("tokens divergem para %q:\n go  = %q\n sql = %q", in, goWords, sqlWords)
		}
	}
}

// Textos exóticos de ponta a ponta: o que o usuário digita acha o texto armazenado, e o trecho não vaza o invisível.
func TestSearchTextosExoticos(t *testing.T) {
	s := newSearchSeed(t)
	ctx := context.Background()
	mk := func(title string) service.TaskView {
		return s.e.task(t, service.CreateTaskInput{Title: title, Description: "texto exótico"})
	}
	soft := mk("Vamos co\u00ADoperar no trimestre")
	zwj := mk("fa\u200Dlha\u200Bcurta")
	bom := mk("\uFEFFinicio do arquivo")
	rom := mk("Capítulo Ⅷ revisado")
	dec := mk("Menu do cafe\u0301 novo")
	hit := func(q string, want service.TaskView) {
		t.Helper()
		got := refs(group(s.search(t, q, service.SearchTask), service.SearchTask))
		if !slices.Contains(got, fmt.Sprintf("%s-%d", s.e.co.TaskPrefix, want.Number)) {
			t.Errorf("busca %q não achou %q: %v", q, want.Title, got)
		}
	}
	hit("cooperar", soft)
	hit("co\u00ADoperar", soft) // o usuário também digita o invisível
	hit("falhacurta", zwj)
	hit("inicio", bom)
	hit("ⅷ", rom)
	hit("Ⅷ", rom)
	hit("cafe", dec)
	hit("café", dec)

	r := s.search(t, "cooperar", service.SearchTask)
	for _, h := range group(r, service.SearchTask) {
		for _, sg := range h.Title {
			if strings.ContainsRune(sg.Text, '\u00AD') {
				t.Errorf("hífen suave vazou para o título mostrado: %q", sg.Text)
			}
		}
	}
	_ = ctx
}

// Prefixo de uma letra não é aplicado (varre o índice inteiro): "c" casa só a palavra exata "c"; "co" casa o prefixo.
func TestSearchPrefixoMinimoNoBanco(t *testing.T) {
	s := newSearchSeed(t)
	if r := group(s.search(t, "cert", service.SearchComment), service.SearchComment); len(r) != 1 {
		t.Errorf("prefixo de 4 runas em comentários: %v", refs(r))
	}
	// "ce" tem 2 runas: vale para tasks, mas comentários exigem 3 (o comentário seed tem "certificado")
	if r := group(s.search(t, "ce", service.SearchComment), service.SearchComment); len(r) != 0 {
		t.Errorf("comentários não casam prefixo de 2 runas: %v", refs(r))
	}
	if r := group(s.search(t, "con", service.SearchTask), service.SearchTask); len(r) == 0 {
		t.Errorf("tasks casam prefixo de 3 runas ('Configuração')")
	}
	if r := group(s.search(t, "co", service.SearchTask), service.SearchTask); len(r) == 0 {
		t.Errorf("tasks casam prefixo de 2 runas ('Configuração')")
	}
	if r := group(s.search(t, "c", service.SearchTask), service.SearchTask); len(r) != 0 {
		t.Errorf("1 runa não casa por prefixo: %v", refs(r))
	}
}

// statement_timeout da transação da busca: consulta lenta vira ErrSearchTooSlow (422), e a conexão volta limpa ao pool.
func TestSearchStatementTimeout(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.svc.SearchStatementTimeout = 50 * time.Millisecond
	start := time.Now()
	err := e.svc.SearchTxForTest(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT pg_sleep(5)`)
		return err
	})
	if !errors.Is(err, service.ErrSearchTooSlow) || !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("err = %v, quer ErrSearchTooSlow (ErrInvalid)", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("o teto não cortou a consulta: %v", d)
	}
	// o SET LOCAL morreu com a transação: a conexão do pool não ficou com o teto
	var v string
	if err := e.svc.Pool.QueryRow(ctx, `SHOW statement_timeout`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v == "50ms" {
		t.Errorf("statement_timeout vazou para o pool: %s", v)
	}
	// dentro do teto, a transação segue normal e é somente leitura
	e.svc.SearchStatementTimeout = 0
	if err := e.svc.SearchTxForTest(ctx, func(tx pgx.Tx) error {
		var st string
		if err := tx.QueryRow(ctx, `SHOW statement_timeout`).Scan(&st); err != nil {
			return err
		}
		if st != "2s" {
			t.Errorf("teto padrão = %s, quer 2s", st)
		}
		_, err := tx.Exec(ctx, `UPDATE tasks SET title = title`)
		if err == nil {
			t.Error("a transação da busca deveria ser somente leitura")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
