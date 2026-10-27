package domain

import (
	"strings"
	"testing"
)

func TestRoundStepKindValid(t *testing.T) {
	for _, k := range []RoundStepKind{"triage", "merge", "kaizen", "closing", "cycle_summary"} {
		if !k.Valid() {
			t.Errorf("%q deveria ser válido", k)
		}
	}
	for _, k := range []RoundStepKind{"triagem", "fechamento", "ciclo", ""} {
		if k.Valid() {
			t.Errorf("%q é do agent-studio, não do contrato: deveria ser inválido", k)
		}
	}
}

func TestValidRoundStepKey(t *testing.T) {
	cases := []struct {
		kind RoundStepKind
		key  string
		want bool
	}{
		{RoundStepMerge, "42", true},
		{RoundStepMerge, "123456789", true},
		{RoundStepMerge, "", false},
		{RoundStepMerge, "0", false},
		{RoundStepMerge, "042", false},
		{RoundStepMerge, "1234567890", false},
		{RoundStepTriage, "", true},
		{RoundStepTriage, "7", false},
		{RoundStepCycleSummary, "", true},
	}
	for _, c := range cases {
		if got := ValidRoundStepKey(c.kind, c.key); got != c.want {
			t.Errorf("ValidRoundStepKey(%q, %q) = %v, quer %v", c.kind, c.key, got, c.want)
		}
	}
}

func TestValidActionID(t *testing.T) {
	for _, id := range []string{"a", "ab1", "a123456789abcdef"} {
		if !ValidActionID(id) {
			t.Errorf("%q deveria valer", id)
		}
	}
	for _, id := range []string{"", "1ab", "AB1", "a_b", "a1234567890abcdefg"} {
		if ValidActionID(id) {
			t.Errorf("%q deveria ser recusado", id)
		}
	}
}

func TestValidCycleIssueRef(t *testing.T) {
	if !ValidCycleIssueRef("renatobardi/oute-agent#12") {
		t.Error("issue com dono e repo deveria valer")
	}
	for _, ref := range []string{"oute-agent#12", "a/b", "a/b#", "a/b#x", strings.Repeat("a", 200) + "/b#1"} {
		if ValidCycleIssueRef(ref) {
			t.Errorf("%q deveria ser recusada", ref)
		}
	}
}

func TestValidRoundNameUsesTheGenerator(t *testing.T) {
	if !ValidRoundName("Brave_Otter") || !ValidRoundName("Brave_Otter_101") {
		t.Error("nome do gerador único deveria valer")
	}
	if ValidRoundName("brave") || ValidRoundName("Brave_otter") {
		t.Error("nome fora do gerador deveria ser recusado")
	}
}

func TestValidApprovalKey(t *testing.T) {
	if !ValidApprovalKey("20261027-080000-cafe") {
		t.Error("id de pedido no padrão deveria valer")
	}
	for _, id := range []string{"", "pedido", "20261027-080000-CAFE", "2026-10-27-cafe"} {
		if ValidApprovalKey(id) {
			t.Errorf("%q deveria ser recusado", id)
		}
	}
}

func TestApprovalEnumsAndSHA(t *testing.T) {
	if !ApprovalDecided.Valid() || ApprovalState("recusado").Valid() {
		t.Error("estado do pedido: pending ou decided")
	}
	if !DecisionExecuted.Valid() || ApprovalDecision("executado").Valid() {
		t.Error("decisão: rejected ou executed; a palavra do agent-studio é recusada")
	}
	if !ApproverHumanDark.Valid() || ApprovalApprover("human-dark").Valid() {
		t.Error("aprovador: human ou human_dark (com underline, como na migration)")
	}
	if !ValidSHA256(strings.Repeat("a", 64)) || ValidSHA256(strings.Repeat("A", 64)) || ValidSHA256("abc") {
		t.Error("sha256 deveria ser 64 dígitos hexadecimais minúsculos")
	}
}

func TestDBDiskDefaultsAndKeys(t *testing.T) {
	if DefaultDBDiskCriticalGiB >= DefaultDBDiskWarnGiB {
		t.Error("o limite crítico do disco tem que ficar abaixo do aviso")
	}
	if !strings.HasPrefix(LimitDBDiskWarnGiB, "limit.") || !strings.HasPrefix(LimitDBDiskCriticalGiB, "limit.") {
		t.Error("as chaves do disco seguem o formato limit.<nome> de company_alert_settings")
	}
}
