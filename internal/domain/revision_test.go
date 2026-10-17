package domain

import (
	"strings"
	"testing"
)

func TestRevisionSHA(t *testing.T) {
	// sha256("") e sha256("abc"): vetores conhecidos
	cases := map[string]string{
		"":    "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		"abc": "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
	}
	for in, want := range cases {
		if got := RevisionSHA(in); got != want {
			t.Errorf("RevisionSHA(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestValidRevisionReason(t *testing.T) {
	cases := []struct {
		name   string
		reason string
		want   bool
	}{
		{"vazio", "", true},
		{"curto", "ajuste de tom", true},
		{"no limite", strings.Repeat("é", MaxRevisionReason), true},
		{"acima do limite", strings.Repeat("é", MaxRevisionReason+1), false},
		{"utf8 inválido", "ab\xffcd", false},
	}
	for _, c := range cases {
		if got := ValidRevisionReason(c.reason); got != c.want {
			t.Errorf("%s: ValidRevisionReason = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestRevisionOriginValid(t *testing.T) {
	for _, o := range []RevisionOrigin{RevisionBackfill, RevisionHire, RevisionEdit, RevisionRestore, RevisionProposal, RevisionImport} {
		if !o.Valid() {
			t.Errorf("%q deveria ser válida", o)
		}
	}
	for _, o := range []RevisionOrigin{"", "merge", "EDIT"} {
		if o.Valid() {
			t.Errorf("%q não deveria ser válida", o)
		}
	}
}

func TestGateInstructionKind(t *testing.T) {
	if !GateInstruction.Valid() {
		t.Fatal("instruction deve ser um tipo de gate válido")
	}
	if GateInstruction.Blocking() {
		t.Fatal("a proposta de instruções não bloqueia a persona")
	}
}
