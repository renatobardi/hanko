// Package search traduz o texto digitado na busca (ADR-0024) em uma tsquery do Postgres e marca os trechos que
// casaram, sem HTML: quem renderiza escapa. Não toca no banco; a dobra de caixa/acentos daqui (Fold) é a mesma da função
// SQL hanko_fold (migration 00015) e um teste de integração compara as duas.
package search

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ErrEmpty: a consulta não tem nenhum termo positivo (vazia, só pontuação ou só exclusões).
var ErrEmpty = errors.New("consulta sem termos")

const (
	maxWords   = 12 // palavras positivas e negativas somadas; o resto é ignorado
	maxWordLen = 64 // runas por palavra
	maxRaw     = 512

	// MinPrefixRunes: a última palavra só casa por prefixo (:*) com este tamanho em runas. Um prefixo de uma letra
	// ("a:*") varre uma fatia enorme do índice e não ajuda quem digita (ADR-0024, adendo da revisão).
	MinPrefixRunes = 2
)

// Segment é um pedaço de texto; Match marca o que casou com a consulta.
type Segment struct {
	Text  string `json:"text"`
	Match bool   `json:"match,omitempty"`
}

// Term é uma palavra positiva da consulta, já dobrada. Prefix: casa por prefixo (a última palavra digitada).
type Term struct {
	Text   string
	Prefix bool
}

// Query é a consulta analisada.
type Query struct {
	// TSQuery é o texto para to_tsquery('simple', …): palavras entre aspas simples, & | ! <-> e :* — nunca
	// texto do usuário cru, então não há como quebrar a sintaxe.
	TSQuery string
	// Terms são as palavras positivas (para marcar trechos).
	Terms []Term

	groups       [][]item // plano da consulta, para refazer o texto com outro tamanho mínimo de prefixo
	lastG, lastI int
}

// WithMinPrefix refaz TSQuery e Terms exigindo n runas na última palavra para casar por prefixo (n <
// MinPrefixRunes vale MinPrefixRunes). Serve a tipos de texto muito denso, onde um prefixo curto custa caro.
func (q Query) WithMinPrefix(n int) Query {
	if q.groups == nil {
		return q
	}
	return build(q.groups, q.lastG, q.lastI, max(n, MinPrefixRunes))
}

// foldTable espelha o translate() de hanko_fold: acentos latinos comuns e pontuação ASCII → espaço.
var foldTable = func() map[rune]rune {
	const from = "ÁÀÂÃÄÅÉÈÊËÍÌÎÏÓÒÔÕÖÚÙÛÜÇÑÝáàâãäåéèêëíìîïóòôõöúùûüçñýÿ"
	const to = "aaaaaaeeeeiiiiooooouuuucnyaaaaaaeeeeiiiiooooouuuucnyy"
	const punct = ".,;:/\\-_@#()[]{}<>\"'!?*+=&%$|^~`"
	m := make(map[rune]rune, len(from)+len(punct))
	fr, tr := []rune(from), []rune(to)
	for i, r := range fr {
		m[r] = tr[i]
	}
	for _, r := range punct {
		m[r] = ' '
	}
	return m
}()

// formatRunes são os caracteres de formatação (categoria Cf do Unicode 17: hífen suave, ZWJ/ZWNJ/ZWSP, marcas de
// direção, BOM, tags...). O tokenizador do Postgres trata alguns como letra e outros como separador, e eles são
// invisíveis para quem lê; a dobra os remove dos dois lados (hanko_fold, migration 00024). A lista é fixa, como a
// do SQL: um Go com Unicode mais novo não muda o que a dobra faz.
var formatRunes = &unicode.RangeTable{
	R16: []unicode.Range16{
		{Lo: 0x00AD, Hi: 0x00AD, Stride: 1}, {Lo: 0x0600, Hi: 0x0605, Stride: 1}, {Lo: 0x061C, Hi: 0x061C, Stride: 1},
		{Lo: 0x06DD, Hi: 0x06DD, Stride: 1}, {Lo: 0x070F, Hi: 0x070F, Stride: 1}, {Lo: 0x0890, Hi: 0x0891, Stride: 1},
		{Lo: 0x08E2, Hi: 0x08E2, Stride: 1}, {Lo: 0x180E, Hi: 0x180E, Stride: 1}, {Lo: 0x200B, Hi: 0x200F, Stride: 1},
		{Lo: 0x202A, Hi: 0x202E, Stride: 1}, {Lo: 0x2060, Hi: 0x2064, Stride: 1}, {Lo: 0x2066, Hi: 0x206F, Stride: 1},
		{Lo: 0xFEFF, Hi: 0xFEFF, Stride: 1}, {Lo: 0xFFF9, Hi: 0xFFFB, Stride: 1},
	},
	R32: []unicode.Range32{
		{Lo: 0x110BD, Hi: 0x110BD, Stride: 1}, {Lo: 0x110CD, Hi: 0x110CD, Stride: 1}, {Lo: 0x13430, Hi: 0x1343F, Stride: 1},
		{Lo: 0x1BCA0, Hi: 0x1BCA3, Stride: 1}, {Lo: 0x1D173, Hi: 0x1D17A, Stride: 1}, {Lo: 0xE0001, Hi: 0xE0001, Stride: 1},
		{Lo: 0xE0020, Hi: 0xE007F, Stride: 1},
	},
}

func isFormat(r rune) bool { return unicode.Is(formatRunes, r) }

func foldRune(r rune) rune {
	if f, ok := foldTable[r]; ok {
		return f
	}
	return unicode.ToLower(r)
}

// Fold dobra caixa e acentos, troca pontuação ASCII por espaço e remove os caracteres de formatação (Cf), como
// hanko_fold. Fora os Cf removidos, o comprimento em runas é preservado.
func Fold(s string) string {
	rs := make([]rune, 0, len(s))
	for _, r := range s {
		if isFormat(r) {
			continue
		}
		rs = append(rs, foldRune(r))
	}
	return string(rs)
}

// isWordRune diz se r compõe uma palavra para o tokenizador do Postgres (classe alfanumérica da libc em UTF-8):
// letras, dígitos, números-letra (Nl, "Ⅷ"), marcas combinantes (Mn/Mc/Me, o acento solto de um "é" decomposto) e as
// letras "Other_Alphabetic" (círculos de "Ⓐ"). Os Cf já saíram na dobra.
func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) || unicode.Is(unicode.Nl, r) ||
		unicode.Is(unicode.Other_Alphabetic, r)
}

// words divide s (já dobrada) em palavras de letras e dígitos.
func words(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return !isWordRune(r) })
}

func clip(w string) string {
	if r := []rune(w); len(r) > maxWordLen {
		return string(r[:maxWordLen])
	}
	return w
}

// item é um termo ou frase (várias palavras adjacentes), possivelmente negado.
type item struct {
	words []string
	neg   bool
}

// Parse analisa a consulta no estilo websearch: palavras separadas por espaço = E; "frase entre aspas"; -excluir;
// OR (maiúsculo) entre alternativas. A última palavra positiva casa por prefixo ("depl" acha "deploy").
// Uma palavra com pontuação ("foo-bar", "src/a.go") vira frase de palavras adjacentes.
func Parse(raw string) (Query, error) {
	rs := []rune(strings.TrimSpace(raw))
	if len(rs) > maxRaw {
		rs = rs[:maxRaw]
	}
	// grupos de itens ligados por OR
	groups := [][]item{{}}
	n := 0
	i := 0
	for i < len(rs) && n < maxWords {
		for i < len(rs) && unicode.IsSpace(rs[i]) {
			i++
		}
		if i >= len(rs) {
			break
		}
		neg := false
		if rs[i] == '-' && i+1 < len(rs) && !unicode.IsSpace(rs[i+1]) {
			neg = true
			i++
		}
		var tok string
		quote := false
		if rs[i] == '"' {
			quote = true
			j := i + 1
			for j < len(rs) && rs[j] != '"' {
				j++
			}
			tok = string(rs[i+1 : j])
			i = j + 1
		} else {
			j := i
			for j < len(rs) && !unicode.IsSpace(rs[j]) {
				j++
			}
			tok = string(rs[i:j])
			i = j
		}
		if !quote && !neg && tok == "OR" {
			if len(groups[len(groups)-1]) > 0 {
				groups = append(groups, []item{})
			}
			continue
		}
		ws := words(Fold(tok))
		if len(ws) == 0 {
			continue
		}
		for k := range ws {
			ws[k] = clip(ws[k])
		}
		if len(ws) > maxWords-n {
			ws = ws[:maxWords-n]
		}
		n += len(ws)
		g := &groups[len(groups)-1]
		*g = append(*g, item{words: ws, neg: neg})
	}

	// a última palavra positiva do último grupo não vazio recebe o prefixo (se tiver runas suficientes)
	lastG, lastI := -1, -1
	for gi := len(groups) - 1; gi >= 0 && lastG < 0; gi-- {
		for ii := len(groups[gi]) - 1; ii >= 0; ii-- {
			if !groups[gi][ii].neg {
				lastG, lastI = gi, ii
				break
			}
		}
	}
	if lastG < 0 {
		return Query{}, ErrEmpty
	}
	q := build(groups, lastG, lastI, MinPrefixRunes)
	if q.TSQuery == "" {
		return Query{}, ErrEmpty
	}
	return q, nil
}

// build monta a tsquery e os termos a partir do plano; a palavra (lastG, lastI) leva o prefixo se tiver minPrefix runas.
func build(groups [][]item, lastG, lastI, minPrefix int) Query {
	q := Query{groups: groups, lastG: lastG, lastI: lastI}
	var parts []string
	for gi, g := range groups {
		var conj []string
		hasPos := false
		for ii, it := range g {
			prefix := gi == lastG && ii == lastI
			var lex []string
			for wi, w := range it.words {
				p := prefix && wi == len(it.words)-1 && utf8.RuneCountInString(w) >= minPrefix
				l := "'" + w + "'"
				if p {
					l += ":*"
				}
				lex = append(lex, l)
				if !it.neg {
					q.Terms = append(q.Terms, Term{Text: w, Prefix: p})
				}
			}
			expr := strings.Join(lex, " <-> ")
			if len(lex) > 1 {
				expr = "(" + expr + ")"
			}
			if it.neg {
				expr = "!" + expr
			} else {
				hasPos = true
			}
			conj = append(conj, expr)
		}
		if !hasPos { // um grupo só de exclusões não é uma alternativa válida
			continue
		}
		parts = append(parts, "("+strings.Join(conj, " & ")+")")
	}
	q.TSQuery = strings.Join(parts, " | ")
	return q
}

// span é um intervalo [a,b) de runas.
type span struct{ a, b int }

// prep normaliza espaços (corridas viram um espaço) e devolve as runas do texto e as dobradas, de mesmo tamanho.
func prep(text string, maxRunes int) (orig, folded []rune) {
	orig = make([]rune, 0, min(len(text), maxRunes))
	space := true
	for _, r := range text {
		if isFormat(r) { // invisível e removido da dobra do banco: sai do texto mostrado, mantendo os offsets
			continue
		}
		if unicode.IsSpace(r) {
			if !space {
				orig = append(orig, ' ')
			}
			space = true
			continue
		}
		space = false
		orig = append(orig, r)
		if len(orig) >= maxRunes {
			break
		}
	}
	for len(orig) > 0 && orig[len(orig)-1] == ' ' {
		orig = orig[:len(orig)-1]
	}
	folded = make([]rune, len(orig))
	for i, r := range orig {
		folded[i] = foldRune(r)
	}
	return orig, folded
}

// hits devolve os intervalos das palavras do texto dobrado que casam com algum termo.
func hits(folded []rune, terms []Term) []span {
	var out []span
	for i := 0; i < len(folded); {
		if !isWordRune(folded[i]) {
			i++
			continue
		}
		j := i
		for j < len(folded) && isWordRune(folded[j]) {
			j++
		}
		w := string(folded[i:j])
		for _, t := range terms {
			if w == t.Text || (t.Prefix && strings.HasPrefix(w, t.Text)) {
				out = append(out, span{i, j})
				break
			}
		}
		i = j
	}
	return out
}

func segments(orig []rune, hs []span, from, to int) []Segment {
	var out []Segment
	pos := from
	emit := func(a, b int, m bool) {
		if b > a {
			out = append(out, Segment{Text: string(orig[a:b]), Match: m})
		}
	}
	for _, h := range hs {
		if h.b <= from || h.a >= to {
			continue
		}
		emit(pos, max(h.a, from), false)
		emit(max(h.a, from), min(h.b, to), true)
		pos = min(h.b, to)
	}
	emit(pos, to, false)
	return out
}

// Mark devolve o texto inteiro (espaços normalizados) com as palavras que casaram marcadas — para títulos e nomes.
func Mark(text string, q Query) []Segment {
	orig, folded := prep(text, 4000)
	if len(orig) == 0 {
		return nil
	}
	return segments(orig, hits(folded, q.Terms), 0, len(orig))
}

// Snippet devolve um trecho de até ~width runas em torno da primeira palavra que casou (com "…" nas pontas cortadas).
// Sem nenhuma palavra no texto, devolve o início dele sem marcas.
func Snippet(text string, q Query, width int) []Segment {
	orig, folded := prep(text, 20000)
	if len(orig) == 0 {
		return nil
	}
	if width < 20 {
		width = 20
	}
	hs := hits(folded, q.Terms)
	start := 0
	if len(hs) > 0 {
		start = max(0, hs[0].a-width/4)
		for start > 0 && orig[start-1] != ' ' { // não começa no meio de uma palavra
			start--
		}
	}
	end := min(len(orig), start+width)
	for end < len(orig) && orig[end] != ' ' && end-start < width+40 {
		end++
	}
	for end > start && orig[end-1] == ' ' {
		end--
	}
	var out []Segment
	if start > 0 {
		out = append(out, Segment{Text: "…"})
	}
	out = append(out, segments(orig, hs, start, end)...)
	if end < len(orig) {
		out = append(out, Segment{Text: "…"})
	}
	return out
}

// Plain concatena os segmentos (sem marcas).
func Plain(segs []Segment) string {
	var b strings.Builder
	for _, s := range segs {
		b.WriteString(s.Text)
	}
	return b.String()
}
