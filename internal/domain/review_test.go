package domain

import "testing"

func TestParseVerdict(t *testing.T) {
	cases := []struct {
		in      string
		want    ReviewVerdict
		just    string
		wantOK  bool
		comment string
	}{
		{"VEREDITO: APROVADO\nTudo certo.", ReviewApproved, "Tudo certo.", true, "forma canônica"},
		{"VEREDITO: REPROVADO\nFalta o teste.\nE a doc.", ReviewRejected, "Falta o teste.\nE a doc.", true, "justificativa multilinha"},
		{"veredito: reprovado - sem cobertura", ReviewRejected, "- sem cobertura", true, "caixa livre, texto na mesma linha"},
		{"**VEREDITO: APROVADO**\n\nOk.", ReviewApproved, "Ok.", true, "markdown em volta"},
		{"## Veredito: Aprovada", ReviewApproved, "", true, "feminino e cabeçalho"},
		{"Analisei tudo.\nVEREDITO: REPROVADO\nMotivo X.", ReviewRejected, "Analisei tudo.\nMotivo X.", true, "texto antes do veredito entra na justificativa"},
		{"VEREDITO: approved\nfine", ReviewApproved, "fine", true, "inglês"},
		{"Acho que está aprovado.", "", "Acho que está aprovado.", false, "sem a linha de veredito não presume nada"},
		{"VEREDITO: TALVEZ", "", "VEREDITO: TALVEZ", false, "veredito inválido"},
		{"", "", "", false, "vazio"},
	}
	for _, c := range cases {
		v, just, ok := ParseVerdict(c.in)
		if v != c.want || ok != c.wantOK || just != c.just {
			t.Errorf("%s: ParseVerdict(%q) = (%q, %q, %v), quero (%q, %q, %v)", c.comment, c.in, v, just, ok, c.want, c.just, c.wantOK)
		}
	}
}
