package auth

import (
	"strings"
	"unicode"
)

// FoldEmail aparece em toda comparação de e-mail (admissão, convite, dono): apara e põe em minúsculas
// SEM aproximar um caractere não ASCII de um ASCII. strings.EqualFold e strings.ToLower tratam o sinal de
// Kelvin (U+212A) como "k", e o "İ" (U+0130) como "i": quem controla um e-mail com esses caracteres não pode
// passar por dono de outro e-mail ASCII.
func FoldEmail(s string) string {
	return strings.Map(func(r rune) rune {
		if r < unicode.MaxASCII {
			if 'A' <= r && r <= 'Z' {
				return r + ('a' - 'A')
			}
			return r
		}
		if l := unicode.ToLower(r); l >= unicode.MaxASCII {
			return l
		}
		return r
	}, strings.TrimSpace(s))
}
