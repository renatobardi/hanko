package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestWorktreeBranch(t *testing.T) {
	ok := map[string]string{"HNK-123": "hnk-123", "hnk-7": "hnk-7", " ABC-1 ": "abc-1", "X9-42": "x9-42"}
	for in, want := range ok {
		got, err := WorktreeBranch(in)
		if err != nil || got != want {
			t.Errorf("WorktreeBranch(%q) = %q, %v; quer %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "HNK", "HNK-", "-1", "../x-1", "HNK-1/x", "hnk 1", "HNK--1", "HNK-1;rm"} {
		if _, err := WorktreeBranch(in); !errors.Is(err, ErrInvalid) {
			t.Errorf("WorktreeBranch(%q) deveria ser inválida: %v", in, err)
		}
	}
}

func TestWorktreeStateMachine(t *testing.T) {
	all := []WorktreeState{WorktreeProvisioning, WorktreeReady, WorktreeFailed, WorktreeDiverged, WorktreeRemoved}
	allowed := map[[2]WorktreeState]bool{
		{WorktreeProvisioning, WorktreeReady}:    true,
		{WorktreeProvisioning, WorktreeFailed}:   true,
		{WorktreeProvisioning, WorktreeRemoved}:  true,
		{WorktreeReady, WorktreeDiverged}:        true,
		{WorktreeReady, WorktreeRemoved}:         true,
		{WorktreeReady, WorktreeProvisioning}:    true,
		{WorktreeFailed, WorktreeProvisioning}:   true,
		{WorktreeFailed, WorktreeReady}:          true,
		{WorktreeFailed, WorktreeRemoved}:        true,
		{WorktreeDiverged, WorktreeProvisioning}: true,
		{WorktreeDiverged, WorktreeReady}:        true,
		{WorktreeDiverged, WorktreeRemoved}:      true,
		{WorktreeRemoved, WorktreeProvisioning}:  true,
	}
	for _, from := range all {
		if !from.Valid() {
			t.Errorf("%s deveria ser válido", from)
		}
		for _, to := range all {
			if got := from.CanTransition(to); got != allowed[[2]WorktreeState{from, to}] {
				t.Errorf("%s -> %s = %v", from, to, got)
			}
		}
	}
	if WorktreeState("x").Valid() || WorktreeState("x").CanTransition(WorktreeReady) {
		t.Error("estado desconhecido")
	}
	if WorktreeRemoved.Live() || !WorktreeReady.Live() || !WorktreeFailed.Live() {
		t.Error("Live")
	}
}

func TestMirrorStateAndRemoveReason(t *testing.T) {
	for _, m := range []MirrorState{MirrorIdle, MirrorSyncing, MirrorReady, MirrorFailed} {
		if !m.Valid() {
			t.Errorf("%s", m)
		}
	}
	if MirrorState("").Valid() {
		t.Error("vazio não é estado")
	}
	for _, r := range []RemoveReason{RemovedDone, RemovedCancelled, RemovedMerged, RemovedManual, RemovedWorkspace} {
		if !r.Valid() {
			t.Errorf("%s", r)
		}
	}
	if RemoveReason("").Valid() || RemoveReason("x").Valid() {
		t.Error("motivo desconhecido")
	}
}

func TestValidSHA(t *testing.T) {
	if !ValidSHA(strings.Repeat("a", 40)) || !ValidSHA(strings.Repeat("0", 40)) {
		t.Error("sha válido")
	}
	for _, s := range []string{"", "abc", strings.Repeat("A", 40), strings.Repeat("g", 40), strings.Repeat("a", 41)} {
		if ValidSHA(s) {
			t.Errorf("%q", s)
		}
	}
}

func TestCleanRepoPath(t *testing.T) {
	ok := map[string]string{"": "", ".": "", "/": "", "src": "src", "src/": "src", "a//b/./c": "a/b/c", `a\b`: "a/b", " src/x.go ": "src/x.go"}
	for in, want := range ok {
		got, err := CleanRepoPath(in)
		if err != nil || got != want {
			t.Errorf("CleanRepoPath(%q) = %q, %v; quer %q", in, got, err, want)
		}
	}
	for _, in := range []string{"/etc/passwd", "..", "../x", "a/../../x", ".git", ".GIT/config", "a/.git/b", "a\x00b", strings.Repeat("a", 501)} {
		if _, err := CleanRepoPath(in); !errors.Is(err, ErrInvalid) {
			t.Errorf("CleanRepoPath(%q) deveria falhar: %v", in, err)
		}
	}
}

func TestSessionUsable(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	if !SessionUsable(now, nil) {
		t.Error("sem pedido, a sessão vale")
	}
	if SessionUsable(now.Add(-time.Minute), &now) || SessionUsable(now, &now) {
		t.Error("sessão iniciada antes (ou junto) do pedido não vale")
	}
	if !SessionUsable(now.Add(time.Second), &now) {
		t.Error("sessão depois do pedido vale")
	}
}

func TestProjectDefaultsValidate(t *testing.T) {
	if err := (ProjectDefaults{Instructions: "ok", SkillsPath: "skills/x"}).Validate(); err != nil {
		t.Fatal(err)
	}
	bad := []ProjectDefaults{
		{Instructions: strings.Repeat("x", MaxProjectInstructions+1)},
		{Instructions: "a\x00b"},
		{SkillsPath: "../fora"},
		{SkillsPath: ".git/x"},
	}
	for i, d := range bad {
		if err := d.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("caso %d deveria falhar: %v", i, err)
		}
	}
}
