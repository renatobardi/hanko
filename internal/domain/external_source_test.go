package domain

import (
	"strings"
	"testing"
)

func TestNormalizeRepoURL(t *testing.T) {
	const want = "github.com/renatobardi/oute-agent"
	for _, in := range []string{
		"https://github.com/renatobardi/oute-agent",
		"https://github.com/renatobardi/oute-agent.git",
		"https://GitHub.com/renatobardi/oute-agent/",
		"ssh://git@github.com/renatobardi/oute-agent.git",
		"ssh://git@github.com:22/renatobardi/oute-agent",
		"git@github.com:renatobardi/oute-agent.git",
		"https://user:pass@github.com/renatobardi/oute-agent.git",
		"  https://github.com/renatobardi/oute-agent  ",
	} {
		got, ok := NormalizeRepoURL(in)
		if !ok || got != want {
			t.Errorf("NormalizeRepoURL(%q) = %q, %v; quer %q", in, got, ok, want)
		}
	}
	// O dono e o repositório mantêm a caixa; só o host vai para minúsculas.
	if got, _ := NormalizeRepoURL("git@GitHub.com:Dono/Repo.git"); got != "github.com/Dono/Repo" {
		t.Errorf("caixa do caminho = %q", got)
	}
	for _, in := range []string{"", "oute-agent", "https://", "https://github.com", "git@github.com:", "x y", strings.Repeat("a", RepoKeyMax+1)} {
		if got, ok := NormalizeRepoURL(in); ok {
			t.Errorf("NormalizeRepoURL(%q) = %q; quer recusar", in, got)
		}
	}
}

func TestValidateExternalName(t *testing.T) {
	for _, ok := range []string{"claude", "oute-server", "a", strings.Repeat("é", ExternalNameMax)} {
		if err := ValidateExternalName(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", " claude", "claude ", strings.Repeat("a", ExternalNameMax+1)} {
		if err := ValidateExternalName(bad); err == nil {
			t.Errorf("%q: deveria recusar", bad)
		}
	}
}

func TestExternalSourceEnums(t *testing.T) {
	for _, o := range []ConversationClassOrigin{ClassOriginOpening, ClassOriginLabel, ClassOriginSkill, ClassOriginRole, ClassOriginAction, ClassOriginDecisionModel, ClassOriginManual} {
		if !o.Valid() {
			t.Errorf("origem %q deveria ser válida", o)
		}
	}
	// Os valores antigos em português não valem (ADR-0078 item 4 e ADR-0079 item 3).
	for _, o := range []string{"abertura", "papel", "acao", "jev", ""} {
		if ConversationClassOrigin(o).Valid() {
			t.Errorf("origem %q não deveria ser válida", o)
		}
	}
	if CallRole("subagente").Valid() || !CallRoleSubagent.Valid() {
		t.Error("papel: subagent vale, subagente não")
	}
	if ClassConfidence("alta").Valid() || !ClassConfidenceLow.Valid() {
		t.Error("confiança: low vale, alta não")
	}
	if CallCostClass("paid").Valid() || !CallCostUnpriced.Valid() {
		t.Error("classe de custo: unpriced vale, paid não")
	}
	if RepoKeyKind("path").Valid() || !RepoKeyName.Valid() || !ClassInputManual.Valid() {
		t.Error("tipos de chave e de entrada")
	}
	if ValidateClass("") == nil || ValidateClass(strings.Repeat("a", ClassMax+1)) == nil || ValidateClass("build") != nil {
		t.Error("ValidateClass")
	}
}

func TestValidExternalConversationName(t *testing.T) {
	for _, ok := range []string{"Veloz_Raposa", "Veloz_Raposa_417"} {
		if !ValidExternalConversationName(ok) {
			t.Errorf("%q deveria valer", ok)
		}
	}
	for _, bad := range []string{"veloz_raposa", "Ágil_Raposa", "Veloz_Raposa_017", "Veloz_Raposa_41", "Veloz-Raposa", ""} {
		if ValidExternalConversationName(bad) {
			t.Errorf("%q não deveria valer", bad)
		}
	}
}
