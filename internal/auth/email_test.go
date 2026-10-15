package auth

import "testing"

// U+212A (sinal de Kelvin) e U+0130 ("İ") têm minúscula ASCII: EqualFold/ToLower os confundem com "k"/"i".
const (
	kelvin = "K"
	dotI   = "İ"
)

func TestFoldEmailKeepsNonASCIIApart(t *testing.T) {
	for in, want := range map[string]string{
		"  Ana@Exemplo.COM ":       "ana@exemplo.com",
		kelvin + "@x.com":          kelvin + "@x.com", // não vira "k@x.com"
		"a" + dotI + "@x.com":      "a" + dotI + "@x.com",
		"É@x.com":                  "é@x.com", // É -> é continua minúscula normal
		"MAIÚSCULA@Exemplo.com.br": "maiúscula@exemplo.com.br",
	} {
		if got := FoldEmail(in); got != want {
			t.Errorf("FoldEmail(%q) = %q, quer %q", in, got, want)
		}
	}
}

func TestAdmissionDoesNotFoldKelvin(t *testing.T) {
	a := Admission{Emails: []string{"k@x.com"}, Domains: []string{"k.com"}}
	if !a.Allows("K@X.com") {
		t.Fatal("caixa ASCII continua valendo")
	}
	for _, e := range []string{kelvin + "@x.com", "a@" + kelvin + ".com"} {
		if a.Allows(e) {
			t.Errorf("%q passou pelas listas por aproximação de Kelvin", e)
		}
	}
}
