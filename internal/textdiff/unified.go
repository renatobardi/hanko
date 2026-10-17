package textdiff

import (
	"fmt"
	"strings"
)

// Unified escreve o diff em texto (linhas com "+ ", "- " e "  "), com n linhas de contexto, para ir dentro de um
// bloco de código (corpo de um gate). Passando de maxBytes, corta numa linha inteira e avisa quantas linhas faltam.
func Unified(r Result, n, maxBytes int) string {
	var b strings.Builder
	lines := Context(r.Lines, n)
	for i, l := range lines {
		var s string
		if l.Op == Skip {
			s = fmt.Sprintf("@@ %d linhas iguais @@\n", l.Skipped)
		} else {
			s = string(l.Op) + " " + l.Text + "\n"
		}
		if maxBytes > 0 && b.Len()+len(s) > maxBytes {
			fmt.Fprintf(&b, "@@ (diff cortado: mais %d linhas) @@\n", len(lines)-i)
			break
		}
		b.WriteString(s)
	}
	return b.String()
}
