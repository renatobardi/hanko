// Package textdiff compara dois textos linha a linha (menor subsequência comum), sem dependências. Serve ao diff entre
// versões de um artefato (ADR-0028). O custo é O(n·m) no trecho que mudou, com teto: acima dele o trecho vira "tudo
// removido, tudo adicionado" (correto, só menos fino).
package textdiff

import "strings"

// Op diz o que aconteceu com a linha.
type Op byte

const (
	Equal  Op = ' '
	Insert Op = '+'
	Delete Op = '-'
	// Skip marca um trecho de linhas iguais omitido por Context; Skipped diz quantas.
	Skip Op = '~'
)

// Line é uma linha do diff. OldNo/NewNo são números de linha (a partir de 1; 0 = não existe naquele lado).
type Line struct {
	Op      Op
	Text    string
	OldNo   int
	NewNo   int
	Skipped int
}

// Result é o diff completo.
type Result struct {
	Lines   []Line
	Added   int
	Removed int
	// Coarse é verdadeiro quando o trecho alterado passou do teto e não foi refinado.
	Coarse bool
}

// Changed diz se há alguma diferença.
func (r Result) Changed() bool { return r.Added+r.Removed > 0 }

// maxCells é o teto da tabela da LCS (linhas alteradas de um lado × do outro).
const maxCells = 4_000_000

func split(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	return strings.Split(s, "\n")
}

// Lines compara a com b.
func Lines(a, b string) Result {
	x, y := split(a), split(b)
	pre := 0
	for pre < len(x) && pre < len(y) && x[pre] == y[pre] {
		pre++
	}
	suf := 0
	for suf < len(x)-pre && suf < len(y)-pre && x[len(x)-1-suf] == y[len(y)-1-suf] {
		suf++
	}
	var res Result
	oldNo, newNo := 0, 0
	emit := func(op Op, text string) {
		l := Line{Op: op, Text: text}
		switch op {
		case Equal:
			oldNo++
			newNo++
			l.OldNo, l.NewNo = oldNo, newNo
		case Delete:
			oldNo++
			l.OldNo = oldNo
			res.Removed++
		case Insert:
			newNo++
			l.NewNo = newNo
			res.Added++
		}
		res.Lines = append(res.Lines, l)
	}
	for _, t := range x[:pre] {
		emit(Equal, t)
	}
	mx, my := x[pre:len(x)-suf], y[pre:len(y)-suf]
	switch {
	case len(mx) == 0:
		for _, t := range my {
			emit(Insert, t)
		}
	case len(my) == 0:
		for _, t := range mx {
			emit(Delete, t)
		}
	case len(mx)*len(my) > maxCells:
		res.Coarse = true
		for _, t := range mx {
			emit(Delete, t)
		}
		for _, t := range my {
			emit(Insert, t)
		}
	default:
		lcs(mx, my, emit)
	}
	for _, t := range x[len(x)-suf:] {
		emit(Equal, t)
	}
	return res
}

// lcs preenche a tabela de comprimentos de trás para frente e percorre-a para a frente, emitindo as linhas.
func lcs(x, y []string, emit func(Op, string)) {
	n, m := len(x), len(y)
	w := m + 1
	t := make([]int32, (n+1)*w)
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if x[i] == y[j] {
				t[i*w+j] = t[(i+1)*w+j+1] + 1
			} else {
				t[i*w+j] = max(t[(i+1)*w+j], t[i*w+j+1])
			}
		}
	}
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case x[i] == y[j]:
			emit(Equal, x[i])
			i++
			j++
		case t[(i+1)*w+j] >= t[i*w+j+1]:
			emit(Delete, x[i])
			i++
		default:
			emit(Insert, y[j])
			j++
		}
	}
	for ; i < n; i++ {
		emit(Delete, x[i])
	}
	for ; j < m; j++ {
		emit(Insert, y[j])
	}
}

// Context mantém só as linhas alteradas e n linhas iguais em volta, trocando cada trecho omitido por uma linha Skip.
func Context(lines []Line, n int) []Line {
	keep := make([]bool, len(lines))
	for i, l := range lines {
		if l.Op == Equal {
			continue
		}
		for k := max(0, i-n); k <= min(len(lines)-1, i+n); k++ {
			keep[k] = true
		}
	}
	var out []Line
	skipped := 0
	flush := func() {
		if skipped > 0 {
			out = append(out, Line{Op: Skip, Skipped: skipped})
			skipped = 0
		}
	}
	for i, l := range lines {
		if keep[i] {
			flush()
			out = append(out, l)
		} else {
			skipped++
		}
	}
	flush()
	return out
}
