package domain

import (
	"strings"
	"testing"
	"time"
)

// Tabelas dos validadores e classificadores puros do domínio: cada enum aceita só os seus valores.

func TestEnumsValid(t *testing.T) {
	cases := []struct {
		name string
		ok   []bool // resultado esperado para cada entrada
		got  []bool
	}{
		{"AlertKind", []bool{true, true, true, false}, []bool{AlertPersonaSilent.Valid(), AlertRunStuck.Valid(), AlertBudgetNearLimit.Valid(), AlertKind("x").Valid()}},
		{"AnchorTarget", []bool{true, true, false}, []bool{AnchorTaskDescription.Valid(), AnchorRunSummary.Valid(), AnchorTarget("x").Valid()}},
		{"ArtifactKind", []bool{true, true, true, true, false}, []bool{KindMarkdown.Valid(), KindText.Valid(), KindImage.Valid(), KindOther.Valid(), ArtifactKind("x").Valid()}},
		{"ConversationStatus", []bool{true, true, false}, []bool{ConversationOpen.Valid(), ConversationArchived.Valid(), ConversationStatus("x").Valid()}},
		{"MessageAuthor", []bool{true, true, true, false}, []bool{MessageHuman.Valid(), MessagePersona.Valid(), MessageSystem.Valid(), MessageAuthor("x").Valid()}},
		{"GoalStatus", []bool{true, true, true, false}, []bool{GoalActive.Valid(), GoalAchieved.Valid(), GoalAbandoned.Valid(), GoalStatus("x").Valid()}},
		{"IntegrationKind", []bool{true, true, false}, []bool{IntegrationMCP.Valid(), IntegrationWebhook.Valid(), IntegrationKind("x").Valid()}},
		{"MemberRole", []bool{true, true, true, true, false}, []bool{MemberOwner.Valid(), MemberAdmin.Valid(), MemberMember.Valid(), MemberViewer.Valid(), MemberRole("x").Valid()}},
		{"MemoryProposalKind", []bool{true, true, true, false}, []bool{ProposalMerge.Valid(), ProposalDelete.Valid(), ProposalRewrite.Valid(), MemoryProposalKind("x").Valid()}},
		{"MemoryProposalStatus", []bool{true, true, true, true, true, false}, []bool{ProposalPending.Valid(), ProposalApproved.Valid(), ProposalRejected.Valid(), ProposalApplied.Valid(), ProposalFailed.Valid(), MemoryProposalStatus("x").Valid()}},
		{"MemoryAccess", []bool{true, true, false}, []bool{MemoryRead.Valid(), MemoryWrite.Valid(), MemoryAccess("x").Valid()}},
		{"UserRole", []bool{true, true, true, false}, []bool{RoleOwner.Valid(), RoleAdmin.Valid(), RoleMember.Valid(), UserRole("x").Valid()}},
		{"ProjectStatus", []bool{true, true, false}, []bool{ProjectActive.Valid(), ProjectArchived.Valid(), ProjectStatus("x").Valid()}},
		{"GateKind", []bool{true, true, true, true, false}, []bool{GateApproval.Valid(), GateTaskReview.Valid(), GateHire.Valid(), GateMemory.Valid(), GateKind("x").Valid()}},
		{"GateStatus", []bool{true, true, true, true, true, false}, []bool{GatePending.Valid(), GateApproved.Valid(), GateRejected.Valid(), GateChangesRequested.Valid(), GateExpired.Valid(), GateStatus("x").Valid()}},
		{"ReviewVerdict", []bool{true, true, false}, []bool{ReviewApproved.Valid(), ReviewRejected.Valid(), ReviewVerdict("").Valid()}},
		{"ValidWebhookEvent", []bool{true, true, false, false}, []bool{ValidWebhookEvent(EventTaskCreated), ValidWebhookEvent(EventAlertOpened), ValidWebhookEvent(EventPing), ValidWebhookEvent("x")}},
		{"ValidReviewerAgent", []bool{true, true, true, false}, []bool{ValidReviewerAgent("claude"), ValidReviewerAgent("codex"), ValidReviewerAgent("fake"), ValidReviewerAgent("acp")}},
		{"ValidPersonaIcon", []bool{true, true, false}, []bool{ValidPersonaIcon(DefaultPersonaIcon), ValidPersonaIcon("rocket"), ValidPersonaIcon("nope")}},
	}
	for _, c := range cases {
		for i := range c.ok {
			if c.got[i] != c.ok[i] {
				t.Errorf("%s[%d] = %v, quer %v", c.name, i, c.got[i], c.ok[i])
			}
		}
	}
}

func TestMemberRolePowers(t *testing.T) {
	roles := []MemberRole{MemberViewer, MemberMember, MemberAdmin, MemberOwner}
	for i, r := range roles {
		if r.Rank() != i+1 {
			t.Errorf("Rank(%s) = %d", r, r.Rank())
		}
		for j, min := range roles {
			if got, want := r.AtLeast(min), i >= j; got != want {
				t.Errorf("%s.AtLeast(%s) = %v", r, min, got)
			}
		}
	}
	if MemberRole("x").AtLeast(MemberViewer) || MemberRole("x").Rank() != 0 {
		t.Error("papel inválido não pode ter poder")
	}
	for _, target := range roles {
		if !MemberOwner.Manages(target) {
			t.Errorf("owner deve gerir %s", target)
		}
		wantAdmin := target == MemberMember || target == MemberViewer
		if MemberAdmin.Manages(target) != wantAdmin {
			t.Errorf("admin.Manages(%s) = %v", target, !wantAdmin)
		}
		if MemberMember.Manages(target) || MemberViewer.Manages(target) {
			t.Errorf("member/viewer não gerem %s", target)
		}
	}
	if MemberOwner.Manages("x") {
		t.Error("owner não gere papel inválido")
	}
}

func TestInviteExpired(t *testing.T) {
	now := time.Now()
	i := Invite{ExpiresAt: now}
	if !i.Expired(now) || !i.Expired(now.Add(time.Second)) || i.Expired(now.Add(-time.Second)) {
		t.Error("Expired deve valer a partir do instante de expiração")
	}
}

func TestArtifactClassification(t *testing.T) {
	tests := []struct {
		mime    string
		kind    ArtifactKind
		inline  bool
		text    bool
		preview Preview
	}{
		{"text/markdown", KindMarkdown, true, true, PreviewMarkdown},
		{"text/plain", KindText, true, true, PreviewText},
		{"text/html", KindText, false, true, PreviewText},
		{"image/png", KindImage, true, false, PreviewImage},
		{"image/svg+xml", KindImage, false, true, PreviewText}, // SVG nunca é desenhado: executa script
		{"application/json", KindOther, false, true, PreviewText},
		{"application/pdf", KindOther, false, false, PreviewNone},
	}
	for _, tt := range tests {
		if got := KindOf(tt.mime); got != tt.kind {
			t.Errorf("KindOf(%s) = %s", tt.mime, got)
		}
		if got := InlineSafe(tt.mime); got != tt.inline {
			t.Errorf("InlineSafe(%s) = %v", tt.mime, got)
		}
		if got := IsText(tt.mime); got != tt.text {
			t.Errorf("IsText(%s) = %v", tt.mime, got)
		}
		if got := PreviewOf(tt.mime); got != tt.preview {
			t.Errorf("PreviewOf(%s) = %s", tt.mime, got)
		}
	}
}

func TestTitleFromMessageAndIsChat(t *testing.T) {
	if got := TitleFromMessage("\n  \n## Olá mundo\nresto"); got != "Olá mundo" {
		t.Errorf("título = %q", got)
	}
	if got := TitleFromMessage(" \n\n"); got != "" {
		t.Errorf("sem linha útil deveria dar vazio, deu %q", got)
	}
	long := TitleFromMessage(strings.Repeat("ã", 100))
	if n := len([]rune(long)); n != 60 || !strings.HasSuffix(long, "…") {
		t.Errorf("título longo: %d runas, %q", n, long)
	}
	if !(Run{Trigger: TriggerChat}).IsChat() || (Run{Trigger: TriggerAssign}).IsChat() {
		t.Error("IsChat segue o gatilho")
	}
}

func TestGoalProgressAndReaches(t *testing.T) {
	tests := []struct {
		start, target, cur, want float64
	}{
		{0, 100, 50, 0.5}, {0, 100, -5, 0}, {0, 100, 150, 1},
		{100, 0, 25, 0.75}, {100, 0, 120, 0}, // meta de redução
		{5, 5, 9, 0}, // intervalo vazio
	}
	for _, tt := range tests {
		if got := GoalProgress(tt.start, tt.target, tt.cur); got != tt.want {
			t.Errorf("GoalProgress(%v,%v,%v) = %v, quer %v", tt.start, tt.target, tt.cur, got, tt.want)
		}
	}
	up := Goal{StartValue: 0, TargetValue: 10, CurrentValue: 5}
	if up.Progress() != 0.5 || !up.Reaches(10) || up.Reaches(9) {
		t.Error("meta de alta")
	}
	down := Goal{StartValue: 10, TargetValue: 2}
	if !down.Reaches(2) || !down.Reaches(1) || down.Reaches(3) {
		t.Error("meta de redução")
	}
}

func TestResolveModelAndCovers(t *testing.T) {
	for _, tt := range []struct {
		task, persona, want string
		src                 ModelSource
	}{
		{"a", "b", "a", ModelFromTask}, {"", "b", "b", ModelFromPersona}, {"", "", "", ModelFromHarnessDefault},
	} {
		if m, s := ResolveModel(tt.task, tt.persona); m != tt.want || s != tt.src {
			t.Errorf("ResolveModel(%q,%q) = %q,%s", tt.task, tt.persona, m, s)
		}
	}
	sub := Subscription{Active: true, Agent: "claude", ModelPrefixes: []string{"claude-opus"}}
	if !sub.Covers("claude", "claude-opus-4") || sub.Covers("claude", "claude-haiku") || sub.Covers("codex", "claude-opus-4") {
		t.Error("prefixos de modelo")
	}
	if !sub.Covers("claude", "") {
		t.Error("modelo vazio (default do harness) pertence ao plano")
	}
	if !(Subscription{Active: true, Agent: "claude"}).Covers("claude", "qualquer") {
		t.Error("sem prefixos cobre todo modelo do harness")
	}
	sub.Active = false
	if sub.Covers("claude", "claude-opus-4") {
		t.Error("assinatura inativa não cobre")
	}
}

func TestHasReviewer(t *testing.T) {
	id := NewID()
	if HasReviewer(nil, "") || !HasReviewer(&id, "") || !HasReviewer(nil, "codex") {
		t.Error("HasReviewer")
	}
	if (Persona{}).HasReviewer() || !(Persona{ReviewerAgent: "codex"}).HasReviewer() {
		t.Error("Persona.HasReviewer")
	}
}

func TestMemoryAccessAllows(t *testing.T) {
	if !MemoryWrite.Allows(MemoryRead) || !MemoryWrite.Allows(MemoryWrite) || !MemoryRead.Allows(MemoryRead) {
		t.Error("write cobre read; read cobre read")
	}
	if MemoryRead.Allows(MemoryWrite) || MemoryAccess("x").Allows(MemoryRead) {
		t.Error("read não cobre write; inválido não cobre nada")
	}
}

func TestSkillMDQuoting(t *testing.T) {
	md := SkillMD("rev", "Faz: \"coisas\"\n  com   espaço", "\n corpo \n")
	want := "---\nname: rev\ndescription: \"Faz: \\\"coisas\\\" com espaço\"\n---\n\ncorpo\n"
	if md != want {
		t.Errorf("SkillMD = %q, quer %q", md, want)
	}
	if got := (Skill{Slug: "rev", Description: "d", Body: "b"}).MD(); !strings.HasPrefix(got, "---\nname: rev\n") {
		t.Errorf("Skill.MD = %q", got)
	}
}

func TestUserHelpers(t *testing.T) {
	u := User{ID: NewID(), Email: "ana@acme.io", Name: "  "}
	if u.DisplayName() != "ana" {
		t.Errorf("DisplayName = %q", u.DisplayName())
	}
	u.Name = " Ana "
	if u.DisplayName() != "Ana" {
		t.Errorf("DisplayName = %q", u.DisplayName())
	}
	if a := u.Actor(); a.Type != ActorHuman || a.ID == nil || *a.ID != u.ID {
		t.Errorf("Actor = %+v", a)
	}
	if !(APIKey{}).Active() {
		t.Error("chave sem revogação está ativa")
	}
	now := time.Now()
	if (APIKey{RevokedAt: &now}).Active() {
		t.Error("chave revogada não está ativa")
	}
}

func TestAnchorRuneSlice(t *testing.T) {
	if _, ok := runeSlice("abc", -1, 2); ok {
		t.Error("início negativo")
	}
	if _, ok := runeSlice("abc", 2, 1); ok {
		t.Error("fim antes do início")
	}
	if _, ok := runeSlice("abc", 0, 9); ok {
		t.Error("fim além do texto")
	}
	if s, ok := runeSlice("ação", 1, 3); !ok || s != "çã" {
		t.Errorf("runeSlice = %q %v", s, ok)
	}
}
