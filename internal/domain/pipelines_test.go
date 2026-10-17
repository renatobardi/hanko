package domain

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestPipelineEnums(t *testing.T) {
	for _, k := range StageKinds {
		if !k.Valid() {
			t.Errorf("etapa %s", k)
		}
		if k.Terminal() != (k == StageDone || k == StageCancelled) {
			t.Errorf("Terminal(%s)", k)
		}
	}
	for _, s := range CaseStatuses {
		if !s.Valid() || s.Terminal() != (s == CaseDone || s == CaseCancelled) {
			t.Errorf("status %s", s)
		}
	}
	for _, f := range FieldTypes {
		if !f.Valid() {
			t.Errorf("campo %s", f)
		}
	}
	if StageKind("x").Valid() || CaseStatus("x").Valid() || FieldType("x").Valid() || CaseLinkRole("x").Valid() ||
		ReviewDecision("x").Valid() || DecisionSource("x").Valid() {
		t.Error("valor inválido aceito")
	}
	for _, r := range []CaseLinkRole{LinkOrigin, LinkWork, LinkAutomation, LinkConversation} {
		if !r.Valid() {
			t.Errorf("papel %s", r)
		}
	}
	for _, d := range []ReviewDecision{ReviewApprove, ReviewReject, ReviewRequestChanges} {
		if !d.Valid() {
			t.Errorf("decisão %s", d)
		}
	}
	if !DecisionFromGate.Valid() || !DecisionFromCaseReview.Valid() {
		t.Error("origem de decisão")
	}
	for k, want := range map[StageKind]CaseStatus{
		StageWorking: CaseInProgress, StageReview: CaseInReview, StageDone: CaseDone, StageCancelled: CaseCancelled, "x": CaseInProgress,
	} {
		if got := StatusForStage(k); got != want {
			t.Errorf("StatusForStage(%s) = %s", k, got)
		}
	}
}

func TestParseApprover(t *testing.T) {
	id := NewID()
	for _, s := range []string{"", "  ", ApproverAnyMember} {
		if r, err := ParseApprover(s); err != nil || !r.AnyMember {
			t.Errorf("ParseApprover(%q) = %+v, %v", s, r, err)
		}
	}
	if r, err := ParseApprover(MemberApprover(id)); err != nil || r.UserID == nil || *r.UserID != id {
		t.Errorf("member: %+v, %v", r, err)
	}
	if r, err := ParseApprover(PersonaApprover(id)); err != nil || r.PersonaID == nil || *r.PersonaID != id {
		t.Errorf("persona: %+v, %v", r, err)
	}
	for _, s := range []string{"member:xyz", "persona:", "alguem"} {
		if _, err := ParseApprover(s); !errors.Is(err, ErrInvalid) {
			t.Errorf("ParseApprover(%q) deveria ser inválido: %v", s, err)
		}
	}
}

func TestStageConfigHelpers(t *testing.T) {
	var nilRouting *ReviewRouting
	if !nilRouting.RejectReasonRequired() || !nilRouting.ChangesReasonRequired() {
		t.Error("padrão é exigir motivo")
	}
	f, tr := false, true
	r := &ReviewRouting{RequireRejectReason: &f, RequireChangesReason: &tr}
	if r.RejectReasonRequired() || !r.ChangesReasonRequired() {
		t.Error("valores explícitos")
	}
	if !(&ReviewRouting{}).RejectReasonRequired() {
		t.Error("ausente = true")
	}
	if (StageConfig{}).ApprovalRequired() || !(StageConfig{Approval: &StageApproval{Required: true}}).ApprovalRequired() {
		t.Error("ApprovalRequired")
	}
	if (StageConfig{}).RequireChildrenTerminal() || !(StageConfig{Gates: &StageGates{RequireChildrenTerminal: true}}).RequireChildrenTerminal() {
		t.Error("RequireChildrenTerminal")
	}
	var nilA *StageAutomation
	pid := NewID()
	if nilA.Enabled() || (&StageAutomation{}).Enabled() || !(&StageAutomation{PersonaID: &pid}).Enabled() {
		t.Error("StageAutomation.Enabled")
	}
}

func TestValidateStageConfig(t *testing.T) {
	keys := map[string]bool{"a": true, "b": true}
	pid := NewID()
	review := &ReviewRouting{ApproveTo: "a", RejectTo: "b"}
	manyFields := make([]StageField, MaxStageFields+1)
	manyOpts := make([]string, MaxFieldOptions+1)
	for i := range manyOpts {
		manyOpts[i] = "o"
	}
	tests := []struct {
		name    string
		kind    StageKind
		c       StageConfig
		wantErr string // vazio = válido; senão, trecho do campo
	}{
		{"vazio working", StageWorking, StageConfig{}, ""},
		{"review válida", StageReview, StageConfig{Review: review}, ""},
		{"campos demais", StageWorking, StageConfig{Fields: manyFields}, "config.fields"},
		{"chave ruim", StageWorking, StageConfig{Fields: []StageField{{Key: "Ruim Key", Label: "x", Type: FieldText}}}, ".key"},
		{"chave duplicada", StageWorking, StageConfig{Fields: []StageField{{Key: "k", Label: "x", Type: FieldText}, {Key: "k", Label: "y", Type: FieldText}}}, "duplicate"},
		{"rótulo vazio", StageWorking, StageConfig{Fields: []StageField{{Key: "k", Label: " ", Type: FieldText}}}, ".label"},
		{"tipo inválido", StageWorking, StageConfig{Fields: []StageField{{Key: "k", Label: "x", Type: "zzz"}}}, ".type"},
		{"select sem opções", StageWorking, StageConfig{Fields: []StageField{{Key: "k", Label: "x", Type: FieldSelect}}}, "needs options"},
		{"opções em não select", StageWorking, StageConfig{Fields: []StageField{{Key: "k", Label: "x", Type: FieldText, Options: []string{"a"}}}}, "only select"},
		{"opções demais", StageWorking, StageConfig{Fields: []StageField{{Key: "k", Label: "x", Type: FieldSelect, Options: manyOpts}}}, "at most"},
		{"opção vazia", StageWorking, StageConfig{Fields: []StageField{{Key: "k", Label: "x", Type: FieldSelect, Options: []string{" "}}}}, "each option"},
		{"select ok", StageWorking, StageConfig{Fields: []StageField{{Key: "k_1", Label: "x", Type: FieldSelect, Options: []string{"a", "b"}}}}, ""},
		{"o que acontece longo", StageWorking, StageConfig{WhatHappensHere: strings.Repeat("x", MaxWhatHappensHereSize+1)}, "what_happens_here"},
		{"motivo longo", StageWorking, StageConfig{DisabledReason: strings.Repeat("x", maxDisabledReasonRunes+1)}, "disabled_reason"},
		{"aprovador ruim", StageWorking, StageConfig{Approval: &StageApproval{Required: true, Approver: "xx"}}, "approver"},
		{"revisão por persona", StageReview, StageConfig{Review: review, Approval: &StageApproval{Approver: PersonaApprover(pid)}}, "human"},
		{"working com aprovador persona", StageWorking, StageConfig{Approval: &StageApproval{Approver: PersonaApprover(pid)}}, ""},
		{"automação longa", StageWorking, StageConfig{Automation: &StageAutomation{PersonaID: &pid, Instructions: strings.Repeat("x", MaxAutomationBytes+1)}}, "instructions"},
		{"prioridade ruim", StageWorking, StageConfig{Automation: &StageAutomation{PersonaID: &pid, Priority: "zzz"}}, "priority"},
		{"automação sem persona", StageWorking, StageConfig{Automation: &StageAutomation{Instructions: "faça"}}, "persona_id"},
		{"automação em etapa final", StageDone, StageConfig{Automation: &StageAutomation{PersonaID: &pid}}, "no automation"},
		{"automação ok", StageWorking, StageConfig{Automation: &StageAutomation{PersonaID: &pid, Instructions: "faça", Priority: PriorityHigh}}, ""},
		{"revisão sem destinos", StageReview, StageConfig{}, "needs approve_to"},
		{"revisão sem reject", StageReview, StageConfig{Review: &ReviewRouting{ApproveTo: "a"}}, "needs approve_to"},
		{"destino em não revisão", StageWorking, StageConfig{Review: &ReviewRouting{ApproveTo: "a"}}, "only review"},
		{"não revisão com review vazio", StageWorking, StageConfig{Review: &ReviewRouting{}}, ""},
		{"destino desconhecido", StageReview, StageConfig{Review: &ReviewRouting{ApproveTo: "a", RejectTo: "zz"}}, "unknown stage"},
		{"destino de mudanças desconhecido", StageReview, StageConfig{Review: &ReviewRouting{ApproveTo: "a", RejectTo: "b", RequestChangesTo: "zz"}}, "request_changes_to"},
	}
	for _, tt := range tests {
		err := ValidateStageConfig(tt.kind, tt.c, keys)
		switch {
		case tt.wantErr == "" && err != nil:
			t.Errorf("%s: erro inesperado %v", tt.name, err)
		case tt.wantErr != "" && err == nil:
			t.Errorf("%s: deveria falhar (%s)", tt.name, tt.wantErr)
		case tt.wantErr != "":
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("%s: erro = %v, quer ErrInvalid com %q", tt.name, err, tt.wantErr)
			}
		}
	}
}

func TestValidFieldKey(t *testing.T) {
	for s, want := range map[string]bool{"a": true, "a1_b-c": true, "": false, "_a": false, "-a": false, "A": false, strings.Repeat("a", 64): false, strings.Repeat("a", 63): true} {
		if validFieldKey(s) != want {
			t.Errorf("validFieldKey(%q) = %v", s, !want)
		}
	}
}

func TestCaseIdentifier(t *testing.T) {
	if got := CaseIdentifier("ACME", 12); got != "ACME-C12" {
		t.Errorf("CaseIdentifier = %s", got)
	}
	if p, n, ok := ParseCaseIdentifier("ACME-C12"); !ok || p != "ACME" || n != 12 {
		t.Errorf("parse = %s %d %v", p, n, ok)
	}
	if p, n, ok := ParseCaseIdentifier("A-B-C7"); !ok || p != "A-B" || n != 7 {
		t.Errorf("prefixo com traço = %s %d %v", p, n, ok)
	}
	for _, s := range []string{"", "-C1", "ACME-C", "ACME-C0", "ACME-Cx", "ACME-C-1", "ACME", "ACME-C99999999999999999999"} {
		if _, _, ok := ParseCaseIdentifier(s); ok {
			t.Errorf("ParseCaseIdentifier(%q) deveria falhar", s)
		}
	}
}

func TestVersionConflict(t *testing.T) {
	err := fmt.Errorf("contexto: %w", &VersionConflictError{Current: 7})
	if v, ok := CurrentVersion(err); !ok || v != 7 {
		t.Errorf("CurrentVersion = %d %v", v, ok)
	}
	if !errors.Is(err, ErrVersionConflict) || !errors.Is(err, ErrConflict) {
		t.Error("conflito de versão vale ErrConflict")
	}
	if !strings.Contains(err.Error(), "current version is 7") {
		t.Errorf("mensagem = %v", err)
	}
	if _, ok := CurrentVersion(errors.New("outro")); ok {
		t.Error("erro alheio")
	}
	if !errors.Is(ErrReviewOutdated, ErrConflict) || !errors.Is(ErrCaseBlocked, ErrConflict) {
		t.Error("erros de transição são conflitos")
	}
}
